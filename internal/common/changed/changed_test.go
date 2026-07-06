package changed

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseNameStatus(t *testing.T) {
	out := "M\tREADME.md\nA\tcmd/x/main.go\nD\told.go\nR100\told/p.go\tnew/p.go\n"
	got := parseNameStatus(out)
	want := []statusLine{
		{"M", "README.md"},
		{"A", "cmd/x/main.go"},
		{"D", "old.go"},
		{"R", "new/p.go"}, // rename → new path, status R
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseNameStatus = %+v, want %+v", got, want)
	}
}

func TestParseNumstat(t *testing.T) {
	out := "28\t7\tREADME.md\n17\t0\tcmd/x/main.go\n-\t-\tlogo.png\n0\t0\tsrc/{old => new}/p.go\n"
	got := parseNumstat(out)
	if got["README.md"] != (churnEntry{28, 7}) {
		t.Errorf("README churn = %+v", got["README.md"])
	}
	if got["logo.png"] != (churnEntry{0, 0}) { // binary "-"
		t.Errorf("binary churn = %+v, want 0/0", got["logo.png"])
	}
	if _, ok := got["src/new/p.go"]; !ok { // brace rename keyed by new path
		t.Errorf("brace rename not keyed by new path: %v", got)
	}
}

func TestParseHunkSymbols(t *testing.T) {
	out := `diff --git a/internal/x.go b/internal/x.go
--- a/internal/x.go
+++ b/internal/x.go
@@ -31 +31,3 @@ type Route struct {
+	Firewall string
@@ -45,0 +48,6 @@ func Run(opts Options) error {
+	x()
diff --git a/del.go b/del.go
--- a/del.go
+++ /dev/null
@@ -1,5 +0,0 @@ func Gone() {
`
	got := parseHunkSymbols(out)
	syms := got["internal/x.go"]
	if len(syms) != 2 || syms[0] != "type Route struct {" || syms[1] != "func Run(opts Options) error {" {
		t.Errorf("symbols = %#v, want [type Route…, func Run…]", syms)
	}
	if _, ok := got["del.go"]; ok { // deleted file (+++ /dev/null) → no current path
		t.Errorf("deleted file should yield no symbols: %v", got)
	}
}

func TestParseUntracked(t *testing.T) {
	out := " M tracked.go\n?? new.go\n?? a/b.txt\n"
	got := parseUntracked(out)
	want := []string{"new.go", "a/b.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseUntracked = %v, want %v", got, want)
	}
}

// TestRunFooter drives Run against a real scratch repo: the output must end
// with the plain cost footer (changed has no single raw baseline), and
// SOFIA_FOOTER=off must remove it.
func TestRunFooter(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	gitT(t, dir, "init")
	gitT(t, dir, "config", "user.email", "t@example.com")
	gitT(t, dir, "config", "user.name", "t")
	f := filepath.Join(dir, "main.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "init")
	if err := os.WriteFile(f, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func() string {
		var buf bytes.Buffer
		if err := Run(Options{Root: dir, Symbols: false, Format: "toon"}, &buf); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return buf.String()
	}

	t.Setenv("SOFIA_FOOTER", "")
	out := run()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "# sf ≈") || !strings.HasSuffix(last, " tok") {
		t.Errorf("plain footer expected as the last line, got %q in:\n%s", last, out)
	}

	t.Setenv("SOFIA_FOOTER", "off")
	if out := run(); strings.Contains(out, "# sf ≈") {
		t.Errorf("SOFIA_FOOTER=off must remove the footer:\n%s", out)
	}
}

// TestChangedDeterministic: git's own output ordering is what Collect relies
// on (no extra sort besides the explicit sort.Strings(order) in Collect), so
// two identical runs against the same repo state must produce byte-identical
// output, footer included — the same invariant TestFooterDeterministic pins
// for `sf code`.
func TestChangedDeterministic(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("SOFIA_FOOTER", "")
	dir := t.TempDir()
	gitT(t, dir, "init")
	gitT(t, dir, "config", "user.email", "t@example.com")
	gitT(t, dir, "config", "user.name", "t")
	for _, name := range []string{"a.go", "b.php", "c.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("original\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "init")

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.php"), []byte("<?php\nfunction b() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func() []byte {
		var buf bytes.Buffer
		if err := Run(Options{Root: dir, Symbols: true, Format: "toon"}, &buf); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return buf.Bytes()
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Errorf("two identical runs differ:\n--- first\n%s\n--- second\n%s", a, b)
	}
}

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string][2]string{
		"internal/server/server.go":       {"source", "go"},
		"internal/server/server_test.go":  {"test", "go"},
		"tests/User/LoginTest.php":        {"test", "php"},
		"config/packages/security.yaml":   {"config", ""},
		"docs/measurements/tools/code.md": {"docs", ""},
		"go.mod":                          {"build", ""},
		"composer.json":                   {"build", ""},
		"migrations/Version20260101.php":  {"migration", "php"},
		"README.md":                       {"docs", ""},
		"web/static/app.js":               {"source", "js"},
		"Makefile":                        {"build", ""},
		"weird.bin":                       {"other", ""},
	}
	for path, want := range cases {
		cat, lang := classify(path)
		if cat != want[0] || lang != want[1] {
			t.Errorf("classify(%q) = (%q,%q), want (%q,%q)", path, cat, lang, want[0], want[1])
		}
	}
}
