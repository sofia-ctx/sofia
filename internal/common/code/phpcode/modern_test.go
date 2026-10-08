package phpcode

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModernSummaryFormats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Example.php")
	if err := os.WriteFile(path, []byte(`<?php class Example { public private(set) string $name { get => 'x'; set(string $value) => $value; } public function __construct(final public string $title) {} }`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"toon", "md", "json"} {
		var b bytes.Buffer
		if _, err := Summarize(&b, path, format, false, false, false); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"private(set)", "get", "set", "string", "final"} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("%s missing %q: %s", format, want, b.String())
			}
		}
	}
}

func TestPartialSummaryFormats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Broken.php")
	if err := os.WriteFile(path, []byte(`<?php class Broken { public function run() { $x = ; } }`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"toon", "md", "json"} {
		var b bytes.Buffer
		if _, err := Summarize(&b, path, format, false, false, false); err != nil {
			t.Fatal(err)
		}
		out := strings.ToLower(b.String())
		if !strings.Contains(out, "partial") || !strings.Contains(out, "syntax error") {
			t.Errorf("unmarked recovery (%s): %s", format, b.String())
		}
	}
}
