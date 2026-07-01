package code

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// When the structural summary can't be built (PHP the backend can't parse,
// or a file with no type declaration), `sf code` must fall back to the raw
// file (== cat) rather than erroring, so the agent still gets the content.
func TestRunPHPFallbackOnParseError(t *testing.T) {
	src := "<?php\n$$$ not parseable as a declaration $$$\n"
	p := filepath.Join(t.TempDir(), "Broken.php")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err != nil {
		t.Fatalf("expected graceful fallback, got error: %v", err)
	}
	if !strings.Contains(buf.String(), "not parseable") {
		t.Errorf("fallback should emit raw file content, got:\n%s", buf.String())
	}
}

// An unsupported extension is a usage error, not a fallback case.
func TestRunUnsupportedExtErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.rb")
	if err := os.WriteFile(p, []byte("puts 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Run(Options{Inputs: []string{p}, Format: "toon"}, &buf); err == nil {
		t.Errorf("unsupported ext should error, not cat the file")
	}
}
