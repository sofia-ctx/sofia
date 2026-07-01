package changed

import (
	"reflect"
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
