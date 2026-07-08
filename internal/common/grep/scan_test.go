package grep

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaffoldTree builds a tiny project tree for grep integration tests.
//
//	root/
//	├── src/UserService.php  — function deleteUser containing target
//	├── src/other.php        — also contains target, in plain top-level code
//	├── vendor/skip.php      — should be ignored by default
//	└── notes.txt            — wrong extension if --ext=php
func scaffoldTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("src/UserService.php", "<?php\nclass UserService {\n  public function deleteUser() {\n    if ($status === TARGET) {\n      return;\n    }\n  }\n}\n")
	mk("src/other.php", "<?php\n// note: TARGET\n")
	mk("vendor/skip.php", "<?php\n// TARGET here too but vendor is ignored\n")
	mk("notes.txt", "TARGET is in a text file\n")
	return root
}

func TestRun_LiteralPHPOnly(t *testing.T) {
	root := scaffoldTree(t)
	var buf bytes.Buffer
	err := Run(Options{
		Root:          root,
		Patterns:      []string{"TARGET"},
		Format:        "toon",
		CaseSensitive: true,
		WordBound:     true,
		Exts:          []string{"php"},
	}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "src/UserService.php,4") {
		t.Errorf("expected UserService.php:4 hit, got:\n%s", out)
	}
	if !strings.Contains(out, "src/other.php,2") {
		t.Errorf("expected other.php:2 hit, got:\n%s", out)
	}
	if strings.Contains(out, "vendor/skip.php") {
		t.Errorf("vendor/ should be ignored by default, got:\n%s", out)
	}
	if strings.Contains(out, "notes.txt") {
		t.Errorf("notes.txt should be excluded by --ext=php, got:\n%s", out)
	}
	if !strings.Contains(out, "function deleteUser") {
		t.Errorf("expected enclosing context, got:\n%s", out)
	}
}

func TestRun_RegexCaseInsensitive(t *testing.T) {
	root := scaffoldTree(t)
	var buf bytes.Buffer
	err := Run(Options{
		Root:          root,
		Patterns:      []string{`target`},
		Format:        "toon",
		CaseSensitive: false,
		Regex:         true,
		Exts:          []string{"php"},
	}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "UserService.php") || !strings.Contains(out, "other.php") {
		t.Errorf("expected case-insensitive regex to find both files, got:\n%s", out)
	}
}

func TestRun_ExtraIgnoreDir(t *testing.T) {
	root := scaffoldTree(t)
	var buf bytes.Buffer
	err := Run(Options{
		Root:          root,
		Patterns:      []string{"TARGET"},
		Format:        "toon",
		CaseSensitive: true,
		WordBound:     true,
		Exts:          []string{"php"},
		ExtraIgnore:   []string{"src"},
	}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "src/") {
		t.Errorf("--ignore-dir=src should drop src/ hits, got:\n%s", out)
	}
	if !strings.Contains(out, "empty[1]") {
		t.Errorf("expected empty marker, got:\n%s", out)
	}
}

// TestScan_ReturnsStructuredResult checks the exported Scan wrapper hands back
// the same hits Run would render, without touching an io.Writer — the entry
// point the adapter commands build on.
func TestScan_ReturnsStructuredResult(t *testing.T) {
	root := scaffoldTree(t)
	res, err := Scan(Options{
		Root:          root,
		Patterns:      []string{"TARGET"},
		CaseSensitive: true,
		WordBound:     true,
		Exts:          []string{"php"},
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Patterns) != 1 || res.Patterns[0].Pattern != "TARGET" {
		t.Fatalf("unexpected pattern result: %+v", res.Patterns)
	}
	var files []string
	for _, h := range res.Patterns[0].Hits {
		files = append(files, h.File)
	}
	got := strings.Join(files, ",")
	if !strings.Contains(got, "src/UserService.php") || !strings.Contains(got, "src/other.php") {
		t.Errorf("Scan hits = %q, want both src files", got)
	}
	if strings.Contains(got, "vendor/") {
		t.Errorf("vendor/ should be ignored, got %q", got)
	}
}

func TestScan_EmptyPatternsErrors(t *testing.T) {
	if _, err := Scan(Options{Root: t.TempDir()}); err == nil {
		t.Error("expected error with no patterns")
	}
}

func TestRun_EmptyPatternsErrors(t *testing.T) {
	err := Run(Options{Root: t.TempDir(), Format: "toon"}, &bytes.Buffer{})
	if err == nil {
		t.Error("expected error with no patterns")
	}
}

func TestRun_InvalidRegex(t *testing.T) {
	root := scaffoldTree(t)
	err := Run(Options{
		Root:     root,
		Patterns: []string{"[invalid"},
		Format:   "toon",
		Regex:    true,
		Exts:     []string{"php"},
	}, &bytes.Buffer{})
	if err == nil {
		t.Error("expected regex compile error")
	}
}

// TestNewCommand_WordDefaultsOff locks the fix for "sf grep returns empty
// for Cyrillic": the CLI defaults to substring matching (grep-style), so a
// Russian stem hits inside its inflected forms. --word is opt-in.
func TestNewCommand_WordDefaultsOff(t *testing.T) {
	cmd := NewCommand()
	f := cmd.Flags().Lookup("word")
	if f == nil {
		t.Fatal("expected --word flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--word default = %q, want \"false\" (substring is the default)", f.DefValue)
	}
}

// TestRun_CyrillicStemSubstring is the end-to-end regression for the bug:
// with WordBound off (the new default) a stem matches inside an inflected
// Cyrillic word, where whole-word matching returned empty.
func TestRun_CyrillicStemSubstring(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("технологии обнаружения\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Run(Options{
		Root:     root,
		Patterns: []string{"технолог"},
		Format:   "toon",
		// WordBound omitted → false, the CLI default.
	}, &buf); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "doc.md,1") {
		t.Errorf("expected Cyrillic stem hit in doc.md, got:\n%s", out)
	}
}

// TestNewCommand_MaxPerPatternDefault30 locks the P0-4 token-tail fix: the CLI
// caps hits per pattern at 30 by default (broad patterns like "public function"
// otherwise dumped hundreds of context-rich hits). 0 stays the unlimited escape.
func TestNewCommand_MaxPerPatternDefault30(t *testing.T) {
	cmd := NewCommand()
	f := cmd.Flags().Lookup("max-per-pattern")
	if f == nil {
		t.Fatal("expected --max-per-pattern flag")
	}
	if f.DefValue != "30" {
		t.Errorf("--max-per-pattern default = %q, want \"30\"", f.DefValue)
	}
}

func TestRun_MaxPerPattern(t *testing.T) {
	root := scaffoldTree(t)
	var buf bytes.Buffer
	err := Run(Options{
		Root:          root,
		Patterns:      []string{"TARGET"},
		Format:        "toon",
		CaseSensitive: true,
		WordBound:     true,
		Exts:          []string{"php"},
		MaxPerPattern: 1,
	}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "+1 more truncated") {
		t.Errorf("expected truncation marker, got:\n%s", out)
	}
}

// TestRun_Footer: grep has no single raw baseline, so its cost footer is the
// plain one-field form; SOFIA_FOOTER=off removes it.
func TestRun_Footer(t *testing.T) {
	root := scaffoldTree(t)
	run := func() string {
		var buf bytes.Buffer
		if err := Run(Options{
			Root:          root,
			Patterns:      []string{"TARGET"},
			Format:        "toon",
			CaseSensitive: true,
			Exts:          []string{"php"},
		}, &buf); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	t.Setenv("SOFIA_FOOTER", "")
	out := run()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "# sf ≈") || !strings.HasSuffix(last, " tok") {
		t.Errorf("plain footer expected as the last line, got %q", last)
	}
	if strings.Contains(last, "raw") {
		t.Errorf("grep footer must not carry a raw comparison, got %q", last)
	}

	t.Setenv("SOFIA_FOOTER", "off")
	if out := run(); strings.Contains(out, "# sf ≈") {
		t.Errorf("SOFIA_FOOTER=off must remove the footer:\n%s", out)
	}
}

// TestScanDeterministic: the file walk fans out across goroutines (scan's
// worker pool), but the result is sorted by (file, line) before rendering —
// two identical runs must still produce byte-identical output, footer
// included, the same invariant TestFooterDeterministic pins for `sf code`.
func TestScanDeterministic(t *testing.T) {
	t.Setenv("SOFIA_FOOTER", "")
	root := scaffoldTree(t)
	run := func() []byte {
		var buf bytes.Buffer
		if err := Run(Options{
			Root:          root,
			Patterns:      []string{"TARGET"},
			Format:        "toon",
			CaseSensitive: true,
			WordBound:     true,
		}, &buf); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return buf.Bytes()
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Errorf("two identical runs differ:\n--- first\n%s\n--- second\n%s", a, b)
	}
}
