package gocode

import (
	"strings"
	"testing"
)

const enclosingFixture = `package sample

import "fmt"

// Greet says hello.
func Greet(name string) string {
	return "hello " + name
}

type Server struct {
	addr string
}

// Handle serves one request.
func (s *Server) Handle(w int) error {
	fmt.Println(w)
	return nil
}

type Fetcher interface {
	Fetch(id int) (string, error)
}

const (
	StatusOK  = 200
	StatusErr = 500
)
`

func declsByName(t *testing.T, src string) map[string]Decl {
	t.Helper()
	decls := EnclosingDecls([]byte(src))
	if decls == nil {
		t.Fatal("EnclosingDecls returned nil for valid source")
	}
	byName := make(map[string]Decl, len(decls))
	for _, d := range decls {
		byName[d.Name] = d
	}
	return byName
}

func TestEnclosingDeclsFunc(t *testing.T) {
	byName := declsByName(t, enclosingFixture)
	greet, ok := byName["Greet"]
	if !ok {
		t.Fatal("Greet not found")
	}
	if greet.Label != "func Greet(name string) string" {
		t.Errorf("Greet label = %q", greet.Label)
	}
}

func TestEnclosingDeclsMethodWithReceiver(t *testing.T) {
	byName := declsByName(t, enclosingFixture)
	handle, ok := byName["Handle"]
	if !ok {
		t.Fatal("Handle not found")
	}
	if handle.Label != "func (s *Server) Handle(w int) error" {
		t.Errorf("Handle label = %q", handle.Label)
	}

	// A line inside the method body must map into its span.
	lines := strings.Split(enclosingFixture, "\n")
	bodyLine := -1
	for i, l := range lines {
		if strings.Contains(l, "fmt.Println") {
			bodyLine = i + 1 // 1-based
			break
		}
	}
	if bodyLine < 0 {
		t.Fatal("fixture missing expected body line")
	}
	if bodyLine < handle.StartLine || bodyLine > handle.EndLine {
		t.Errorf("body line %d outside Handle span [%d,%d]", bodyLine, handle.StartLine, handle.EndLine)
	}
}

func TestEnclosingDeclsStructType(t *testing.T) {
	byName := declsByName(t, enclosingFixture)
	server, ok := byName["Server"]
	if !ok {
		t.Fatal("Server not found")
	}
	if server.Label != "type Server struct" {
		t.Errorf("Server label = %q", server.Label)
	}
}

func TestEnclosingDeclsInterfaceType(t *testing.T) {
	byName := declsByName(t, enclosingFixture)
	fetcher, ok := byName["Fetcher"]
	if !ok {
		t.Fatal("Fetcher not found")
	}
	if fetcher.Label != "type Fetcher interface" {
		t.Errorf("Fetcher label = %q", fetcher.Label)
	}
}

func TestEnclosingDeclsConstBlock(t *testing.T) {
	byName := declsByName(t, enclosingFixture)
	ok1, ok := byName["StatusOK"]
	if !ok {
		t.Fatal("StatusOK not found")
	}
	if ok1.Label != "const StatusOK" {
		t.Errorf("StatusOK label = %q", ok1.Label)
	}
	errConst, ok := byName["StatusErr"]
	if !ok {
		t.Fatal("StatusErr not found")
	}
	if errConst.Label != "const StatusErr" {
		t.Errorf("StatusErr label = %q", errConst.Label)
	}
}

func TestEnclosingDeclsParseErrorReturnsNil(t *testing.T) {
	decls := EnclosingDecls([]byte("package x\nfunc ( { this is not go\n"))
	if decls != nil {
		t.Errorf("expected nil for unparseable input, got %+v", decls)
	}
}
