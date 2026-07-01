package composer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealDeps(t *testing.T) {
	got := realDeps(map[string]string{
		"php":                 ">=8.1",
		"ext-json":            "*",
		"ext-openssl":         "*",
		"acme/dsse":           "^1.0",
		"phpseclib/phpseclib": "^3.0",
	})
	want := []string{"acme/dsse", "phpseclib/phpseclib"}
	if !eq(got, want) {
		t.Errorf("realDeps = %v, want %v (php/ext-* must be dropped, sorted)", got, want)
	}
}

func TestPhpstanLevel(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "phpstan.neon"), "parameters:\n    level: 9\n    paths:\n        - src\n")
	if got := phpstanLevel(dir); got != "9" {
		t.Errorf("phpstanLevel(.neon) = %q, want 9", got)
	}

	dist := t.TempDir()
	write(t, filepath.Join(dist, "phpstan.neon.dist"), "parameters:\n  level: max\n")
	if got := phpstanLevel(dist); got != "max" {
		t.Errorf("phpstanLevel(.neon.dist) = %q, want max", got)
	}

	if got := phpstanLevel(t.TempDir()); got != "" {
		t.Errorf("phpstanLevel(none) = %q, want empty", got)
	}
}

func TestParsePkg(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "array-reader")
	write(t, filepath.Join(pkgDir, "composer.json"), `{
  "name": "acme/array-reader",
  "type": "library",
  "require": {"php": ">=8.1"},
  "require-dev": {"phpunit/phpunit": "^12", "phpstan/phpstan": "^2.1"},
  "autoload": {"psr-4": {"Acme\\ArrayReader\\": "src/"}},
  "scripts": {"test": "phpunit", "check": ["@test", "@phpstan"], "phpstan": "phpstan analyse"}
}`)
	write(t, filepath.Join(pkgDir, "phpstan.neon"), "parameters:\n    level: 9\n")

	p, err := parsePkg(root, filepath.Join(pkgDir, "composer.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "acme/array-reader" {
		t.Errorf("Name = %q", p.Name)
	}
	if p.Dir != "array-reader" {
		t.Errorf("Dir = %q, want array-reader", p.Dir)
	}
	if p.Type != "library" || p.PHP != ">=8.1" || p.PHPStan != "9" {
		t.Errorf("type/php/phpstan = %q/%q/%q", p.Type, p.PHP, p.PHPStan)
	}
	if p.Namespace != `Acme\ArrayReader\` {
		t.Errorf("Namespace = %q", p.Namespace)
	}
	if !eq(p.Scripts, []string{"check", "phpstan", "test"}) {
		t.Errorf("Scripts = %v, want sorted [check phpstan test]", p.Scripts)
	}
	if !eq(p.RequireDev, []string{"phpstan/phpstan", "phpunit/phpunit"}) {
		t.Errorf("RequireDev = %v", p.RequireDev)
	}
}

func TestRenderLsSplitsDeps(t *testing.T) {
	pkgs := []Pkg{{
		Name:       "acme/array-reader",
		Dir:        "array-reader",
		Version:    "v1.0.0",
		Type:       "library",
		PHP:        ">=8.1",
		Require:    []string{"acme/enum"},
		RequireDev: []string{"phpstan/phpstan", "phpunit/phpunit"},
	}}

	var toonBuf bytes.Buffer
	if err := renderTOON(&toonBuf, pkgs); err != nil {
		t.Fatal(err)
	}
	toonOut := toonBuf.String()
	if !strings.Contains(toonOut, "deps,dev}:") {
		t.Errorf("TOON header missing dev column: %q", toonOut)
	}
	// The runtime dep is in the deps column, the dev deps in their own column.
	if !strings.Contains(toonOut, "acme/enum,phpstan/phpstan|phpunit/phpunit") {
		t.Errorf("TOON row did not split deps/dev: %q", toonOut)
	}

	var mdBuf bytes.Buffer
	if err := renderMarkdown(&mdBuf, pkgs); err != nil {
		t.Fatal(err)
	}
	mdOut := mdBuf.String()
	if !strings.Contains(mdOut, "| Deps | Dev deps |") {
		t.Errorf("markdown header missing Dev deps column: %q", mdOut)
	}
	if !strings.Contains(mdOut, "| acme/enum | phpstan/phpstan phpunit/phpunit |") {
		t.Errorf("markdown row did not split deps/dev: %q", mdOut)
	}
}

func TestParsePkg_TypeDefaultsToLibrary(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "composer.json"), `{"name":"acme/x","require":{"php":">=8.1"}}`)
	p, err := parsePkg(root, filepath.Join(root, "composer.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "library" {
		t.Errorf("Type = %q, want library (default)", p.Type)
	}
	if p.Dir != "." {
		t.Errorf("Dir = %q, want .", p.Dir)
	}
}

func TestCollect_SkipsVendorAndSorts(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "zeta", "composer.json"), `{"name":"acme/zeta","require":{"php":">=8.1"}}`)
	write(t, filepath.Join(root, "alpha", "composer.json"), `{"name":"acme/alpha","require":{"php":">=8.1"}}`)
	// A vendored composer.json must be ignored.
	write(t, filepath.Join(root, "alpha", "vendor", "acme", "lib", "composer.json"), `{"name":"acme/lib"}`)

	pkgs, err := Collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("Collect found %d packages, want 2 (vendor ignored): %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Name != "acme/alpha" || pkgs[1].Name != "acme/zeta" {
		t.Errorf("not sorted by name: %q, %q", pkgs[0].Name, pkgs[1].Name)
	}
}

func TestRelDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "enum")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := relDir(root, sub); got != "enum" {
		t.Errorf("abs root + abs sub: got %q, want enum", got)
	}
	if got := relDir(root, root); got != "." {
		t.Errorf("dir == root: got %q, want .", got)
	}

	// Regression: `composer show <bareName> --root <abs>` from inside the
	// collection used to print "." — findOne returns a cwd-relative match,
	// which filepath.Rel could not relate to the absolute root.
	t.Chdir(root)
	if got := relDir(root, "enum"); got != "enum" {
		t.Errorf("abs root + cwd-relative dir: got %q, want enum", got)
	}
}

func TestScriptValue(t *testing.T) {
	if got := scriptValue([]byte(`"phpunit"`)); got != "phpunit" {
		t.Errorf("string script = %q", got)
	}
	if got := scriptValue([]byte(`["@test","@phpstan"]`)); got != "@test && @phpstan" {
		t.Errorf("array script = %q", got)
	}
}

func TestFirstFailLine(t *testing.T) {
	out := "PHPUnit 12\n.....\nOK (5 tests)\n[ERROR] Found 2 errors\nmore\n"
	if got := firstFailLine(out); got != "[ERROR] Found 2 errors" {
		t.Errorf("firstFailLine = %q", got)
	}
	// No failure marker: fall back to the last non-empty line.
	if got := firstFailLine("line one\nline two\n\n"); got != "line two" {
		t.Errorf("fallback firstFailLine = %q", got)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
