package gocode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleGo = `package server

import (
	"context"
	"net/http"

	chi "github.com/go-chi/chi/v5"
)

const defaultLimit = 50

var ErrNotFound = http.ErrNotFound

// Server holds HTTP deps.
type Server struct {
	router *chi.Mux
	svc    *Search ` + "`json:\"-\"`" + `
}

type ContactSource interface {
	Fetch(ctx context.Context, inn string) ([]Contact, error)
	Name() string
}

type RegionCode = string

func New(db *DB) (*Server, error) { return nil, nil }

func (s *Server) Routes() http.Handler { return s.router }

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {}
`

func readSample(t *testing.T) *GoFile {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server.go")
	if err := os.WriteFile(p, []byte(sampleGo), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := ReadGo(p)
	if err != nil {
		t.Fatalf("ReadGo: %v", err)
	}
	return g
}

func TestReadGo(t *testing.T) {
	g := readSample(t)

	if g.Package != "server" {
		t.Errorf("package = %q, want server", g.Package)
	}
	if len(g.Imports) != 3 {
		t.Errorf("imports = %v, want 3", g.Imports)
	}
	// aliased import keeps its name
	wantImport := "chi github.com/go-chi/chi/v5"
	found := false
	for _, im := range g.Imports {
		if im == wantImport {
			found = true
		}
	}
	if !found {
		t.Errorf("imports missing %q: %v", wantImport, g.Imports)
	}

	types := map[string]GoType{}
	for _, ty := range g.Types {
		types[ty.Name] = ty
	}
	if s := types["Server"]; s.Kind != "struct" || s.Detail == "" {
		t.Errorf("Server = %+v, want struct with fields", s)
	}
	// struct field tag preserved
	if d := types["Server"].Detail; !strings.Contains(d, "`json:\"-\"`") {
		t.Errorf("Server detail should keep field tag: %q", d)
	}
	if c := types["ContactSource"]; c.Kind != "interface" || !strings.Contains(c.Detail, "Fetch(ctx context.Context, inn string) ([]Contact, error)") {
		t.Errorf("ContactSource = %+v, want interface with method sig", c)
	}
	if r := types["RegionCode"]; r.Kind != "alias" || r.Detail != "string" {
		t.Errorf("RegionCode = %+v, want alias string", r)
	}

	funcs := map[string]GoFunc{}
	for _, fn := range g.Funcs {
		funcs[fn.Name] = fn
	}
	if n := funcs["New"]; n.Recv != "" || n.Sig != "(db *DB) (*Server, error)" {
		t.Errorf("New = %+v, want free func sig (db *DB) (*Server, error)", n)
	}
	if r := funcs["Routes"]; r.Recv != "*Server" || r.Sig != "() http.Handler" {
		t.Errorf("Routes = %+v, want method on *Server", r)
	}
	if !funcs["Routes"].Exported || funcs["handleSearch"].Exported {
		t.Errorf("exported flags wrong: Routes=%v handleSearch=%v", funcs["Routes"].Exported, funcs["handleSearch"].Exported)
	}

	if len(g.Consts) != 1 || g.Consts[0].Name != "defaultLimit" {
		t.Errorf("consts = %+v, want [defaultLimit]", g.Consts)
	}
	if len(g.Vars) != 1 || g.Vars[0].Name != "ErrNotFound" {
		t.Errorf("vars = %+v, want [ErrNotFound]", g.Vars)
	}
}

func TestSliceGoMethodWithDoc(t *testing.T) {
	src := []byte("package x\n\n// Doc line.\nfunc (s *S) Foo(a int) bool { return a > 0 }\n\nfunc Bar() {}\n")
	text, _, err := Slice(src, "S.Foo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "func (s *S) Foo(a int) bool") || !strings.Contains(text, "// Doc line.") {
		t.Errorf("slice missing func body/doc:\n%s", text)
	}
	if strings.Contains(text, "func Bar") {
		t.Errorf("slice leaked another symbol:\n%s", text)
	}
}

func TestSliceGoNotFoundListsNames(t *testing.T) {
	src := []byte("package x\nfunc Bar() {}\n")
	_, names, err := Slice(src, "Nope")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	found := false
	for _, n := range names {
		if n == "Bar" {
			found = true
		}
	}
	if !found {
		t.Errorf("names should list Bar, got %v", names)
	}
}

func TestFilterExported(t *testing.T) {
	g := readSample(t)
	g.FilterExported()
	for _, fn := range g.Funcs {
		if !fn.Exported {
			t.Errorf("unexported func leaked: %s", fn.Name)
		}
	}
	for _, c := range g.Consts {
		if !c.Exported {
			t.Errorf("unexported const leaked: %s", c.Name)
		}
	}
	// defaultLimit (unexported) gone, ErrNotFound (exported) stays
	if len(g.Consts) != 0 {
		t.Errorf("consts after filter = %+v, want none", g.Consts)
	}
	if len(g.Vars) != 1 {
		t.Errorf("vars after filter = %+v, want [ErrNotFound]", g.Vars)
	}
}
