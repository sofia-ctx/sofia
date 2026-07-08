package refs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setLogDir(t *testing.T) {
	t.Helper()
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
}

func refByFile(refs []Ref, file string) (Ref, bool) {
	for _, r := range refs {
		if r.File == file {
			return r, true
		}
	}
	return Ref{}, false
}

// TestScan_Exported checks the exported Scan wrapper: it validates the symbol,
// defaults the root, and returns the same structured result the unexported scan
// yields (minus the raw-token estimate) — the entry point the adapter refs
// command builds on.
func TestScan_Exported(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package sample\n\nfunc Handle(w int) error {\n\treturn nil\n}\n")
	writeFile(t, dir, "b.go", "package sample\n\nfunc Serve() {\n\tHandle(1)\n}\n")

	res, err := Scan(Options{Symbol: "Handle", Root: dir})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Defs != 1 || res.Uses != 1 {
		t.Fatalf("Defs/Uses = %d/%d, want 1/1", res.Defs, res.Uses)
	}
	if _, ok := refByFile(res.Refs, "a.go"); !ok {
		t.Errorf("a.go ref missing: %+v", res.Refs)
	}

	if _, err := Scan(Options{Symbol: "not a symbol", Root: dir}); err == nil {
		t.Error("Scan should reject a non-identifier symbol")
	}
}

func TestFindsDefAndUses(t *testing.T) {
	setLogDir(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package sample\n\nfunc Handle(w int) error {\n\treturn nil\n}\n")
	writeFile(t, dir, "b.go", "package sample\n\nfunc Serve() {\n\tHandle(1)\n}\n")

	res, _, err := scan(Options{Symbol: "Handle"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Defs != 1 || res.Uses != 1 {
		t.Fatalf("Defs/Uses = %d/%d, want 1/1", res.Defs, res.Uses)
	}
	def, ok := refByFile(res.Refs, "a.go")
	if !ok || def.Kind != "def" {
		t.Fatalf("a.go ref missing or not a def: %+v", def)
	}
	if def.Enclosing != "func Handle(w int) error" {
		t.Errorf("def enclosing = %q", def.Enclosing)
	}
	use, ok := refByFile(res.Refs, "b.go")
	if !ok || use.Kind != "use" {
		t.Fatalf("b.go ref missing or not a use: %+v", use)
	}
	if use.Enclosing != "func Serve()" {
		t.Errorf("use enclosing = %q", use.Enclosing)
	}
}

func TestWordBoundary(t *testing.T) {
	setLogDir(t)
	dir := t.TempDir()
	writeFile(t, dir, "c.go", "package sample\n\nfunc GetUser() {}\n\nfunc Getter() {}\n")

	res, _, err := scan(Options{Symbol: "Get"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refs) != 0 || res.Defs != 0 || res.Uses != 0 {
		t.Errorf("expected no hits for \"Get\" inside GetUser/Getter, got %+v", res.Refs)
	}
}

func TestEnclosingGo(t *testing.T) {
	setLogDir(t)
	dir := t.TempDir()
	writeFile(t, dir, "server.go", `package sample

type Server struct{}

func (s *Server) Process(id int) error {
	return validate(id)
}

func validate(id int) error { return nil }
`)

	res, _, err := scan(Options{Symbol: "validate"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Defs != 1 || res.Uses != 1 {
		t.Fatalf("Defs/Uses = %d/%d, want 1/1", res.Defs, res.Uses)
	}
	for _, r := range res.Refs {
		switch r.Kind {
		case "def":
			if r.Enclosing != "func validate(id int) error" {
				t.Errorf("def enclosing = %q", r.Enclosing)
			}
		case "use":
			if r.Enclosing != "func (s *Server) Process(id int) error" {
				t.Errorf("use enclosing = %q", r.Enclosing)
			}
		}
	}
}

func TestEnclosingPHP(t *testing.T) {
	setLogDir(t)
	dir := t.TempDir()
	writeFile(t, dir, "UserService.php", "<?php\nclass UserService {\n    public function deleteUser($id) {\n        $this->repo->remove($id);\n    }\n}\n")

	res, _, err := scan(Options{Symbol: "remove"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refs) != 1 {
		t.Fatalf("want 1 ref, got %+v", res.Refs)
	}
	r := res.Refs[0]
	if r.Kind != "use" {
		t.Errorf("kind = %q, want use", r.Kind)
	}
	if r.Enclosing != "function deleteUser" {
		t.Errorf("enclosing = %q, want \"function deleteUser\"", r.Enclosing)
	}
}

func TestEnclosingTS(t *testing.T) {
	setLogDir(t)
	dir := t.TempDir()
	writeFile(t, dir, "app.ts", "function outer() {\n  return helper();\n}\n\nfunction helper() {\n  return 1;\n}\n")

	res, _, err := scan(Options{Symbol: "helper"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Defs != 1 || res.Uses != 1 {
		t.Fatalf("Defs/Uses = %d/%d, want 1/1", res.Defs, res.Uses)
	}
	for _, r := range res.Refs {
		if r.Kind == "use" && r.Enclosing != "function outer" {
			t.Errorf("use enclosing = %q, want \"function outer\"", r.Enclosing)
		}
	}
}

func TestDefFirstOrdering(t *testing.T) {
	setLogDir(t)
	dir := t.TempDir()
	// "a.go" sorts before "b.go", but b.go holds the def — defs must still
	// come first, ahead of file order.
	writeFile(t, dir, "a.go", "package sample\n\nfunc caller() {\n\tTarget()\n}\n")
	writeFile(t, dir, "b.go", "package sample\n\nfunc Target() {}\n")

	res, _, err := scan(Options{Symbol: "Target"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refs) != 2 {
		t.Fatalf("want 2 refs, got %d: %+v", len(res.Refs), res.Refs)
	}
	if res.Refs[0].Kind != "def" {
		t.Errorf("first ref kind = %q, want def (defs sort first regardless of file order)", res.Refs[0].Kind)
	}
	if res.Refs[1].Kind != "use" {
		t.Errorf("second ref kind = %q, want use", res.Refs[1].Kind)
	}
}

func TestCapTruncates(t *testing.T) {
	setLogDir(t)
	dir := t.TempDir()
	const n = 35
	for i := 0; i < n; i++ {
		writeFile(t, dir, fmt.Sprintf("f%02d.go", i), "package sample\n\nfunc caller() {\n\tTarget()\n}\n")
	}

	res, _, err := scan(Options{Symbol: "Target"}, dir) // Max=0 → default cap 30
	if err != nil {
		t.Fatal(err)
	}
	if res.Uses != n {
		t.Errorf("Uses = %d, want %d (true total, uncapped)", res.Uses, n)
	}
	if len(res.Refs) != 30 {
		t.Errorf("len(Refs) = %d, want 30 (capped)", len(res.Refs))
	}
	if res.Truncated != n-30 {
		t.Errorf("Truncated = %d, want %d", res.Truncated, n-30)
	}

	var buf bytes.Buffer
	if err := Run(Options{Symbol: "Target", Root: dir, Format: "toon"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "more, --max to widen") {
		t.Errorf("header should note truncation, got:\n%s", buf.String())
	}
}

func TestUnparseableFileSkipped(t *testing.T) {
	setLogDir(t)
	dir := t.TempDir()
	writeFile(t, dir, "broken.go", "package sample\n\nfunc Broken( {\n\tTarget()\n}\n")

	res, _, err := scan(Options{Symbol: "Target"}, dir)
	if err != nil {
		t.Fatalf("unparseable Go file must not fail the scan: %v", err)
	}
	if len(res.Refs) != 1 {
		t.Fatalf("want 1 textual hit despite the syntax error, got %+v", res.Refs)
	}
	if res.Refs[0].Enclosing != "" {
		t.Errorf("enclosing = %q, want empty (parse failed, no AST to derive it from)", res.Refs[0].Enclosing)
	}
}

func TestBinarySkipped(t *testing.T) {
	setLogDir(t)
	dir := t.TempDir()
	writeFile(t, dir, "good.go", "package sample\n\nfunc Target() {}\n")
	if err := os.WriteFile(filepath.Join(dir, "blob.dat"), []byte("\x00Target"), 0o644); err != nil {
		t.Fatal(err)
	}
	// blob.dat has no recognised extension by default, so widen Exts to make
	// sure it's actually walked (and then rejected as binary) rather than
	// filtered out by extension.
	res, _, err := scan(Options{Symbol: "Target", Exts: []string{"go", "dat"}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "blob.dat" {
		t.Errorf("Skipped = %v, want [blob.dat]", res.Skipped)
	}
	if res.Defs != 1 {
		t.Errorf("Defs = %d, want 1 (good.go must still be found)", res.Defs)
	}
}

func TestDeterministic(t *testing.T) {
	setLogDir(t)
	t.Setenv("SOFIA_FOOTER", "")
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package sample\n\nfunc caller() {\n\tTarget()\n}\n")
	writeFile(t, dir, "b.go", "package sample\n\nfunc Target() {}\n")

	run := func() []byte {
		var buf bytes.Buffer
		if err := Run(Options{Symbol: "Target", Root: dir, Format: "toon"}, &buf); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return buf.Bytes()
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Errorf("two identical runs differ:\n--- first\n%s\n--- second\n%s", a, b)
	}
}

func TestJSONShape(t *testing.T) {
	setLogDir(t)
	t.Setenv("SOFIA_FOOTER", "off") // the trailing cost footer isn't part of the JSON payload
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package sample\n\nfunc Target() {}\n")

	var buf bytes.Buffer
	if err := Run(Options{Symbol: "Target", Root: dir, Format: "json"}, &buf); err != nil {
		t.Fatal(err)
	}
	var res Result
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if res.Symbol != "Target" || res.Defs != 1 || res.Uses != 0 || len(res.Refs) != 1 {
		t.Errorf("unexpected shape: %+v", res)
	}
}

func TestRejectsRegexInput(t *testing.T) {
	setLogDir(t)
	dir := t.TempDir()
	for _, sym := range []string{"foo bar", "foo.*", "foo(bar)", ""} {
		err := Run(Options{Symbol: sym, Root: dir, Format: "toon"}, &bytes.Buffer{})
		if err == nil {
			t.Errorf("symbol %q: expected error, got nil", sym)
			continue
		}
		if sym != "" && !strings.Contains(err.Error(), "bare identifier") {
			t.Errorf("symbol %q: error = %v, want mention of \"bare identifier\"", sym, err)
		}
	}
}

// TestFooterShowsRawBaseline pins that refs, unlike grep (which has no
// single raw equivalent and passes rawTok=0), always feeds emit.Footer a
// real `grep -rn` baseline — the footer mentions "raw" one way or another
// (a numeric "raw ≈N · saved ≈K" when the compact form wins, else an honest
// "raw passthrough"; refs' extra kind/enclosing columns mean it usually
// costs a little more than bare `grep -rn`, so passthrough is the common
// case — see docs/measurements/tools/refs.md). When a numeric raw is shown
// it must be >= the emitted cost, by Footer's own contract.
func TestFooterShowsRawBaseline(t *testing.T) {
	setLogDir(t)
	t.Setenv("SOFIA_FOOTER", "")
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package sample\n\nfunc Target() {}\n\nfunc caller() {\n\tTarget()\n}\n")

	var buf bytes.Buffer
	if err := Run(Options{Symbol: "Target", Root: dir, Format: "toon"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	footer := lines[len(lines)-1]
	if !strings.HasPrefix(footer, "# sf ≈") {
		t.Fatalf("expected footer line, got %q\nfull output:\n%s", footer, out)
	}
	if !strings.Contains(footer, "raw") {
		t.Fatalf("expected the footer to mention a raw baseline (refs always has one), got %q", footer)
	}
	if idx := strings.Index(footer, "raw ≈"); idx >= 0 {
		var sf, raw int64
		if _, err := fmt.Sscanf(footer, "# sf ≈%d tok", &sf); err != nil {
			t.Fatalf("parse sf tok from %q: %v", footer, err)
		}
		if _, err := fmt.Sscanf(footer[idx:], "raw ≈%d", &raw); err != nil {
			t.Fatalf("parse raw tok from %q: %v", footer, err)
		}
		if raw < sf {
			t.Errorf("raw %d < sf %d, want raw >= sf", raw, sf)
		}
	}
}
