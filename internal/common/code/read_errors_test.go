package code

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingFileFailsWithoutDiscardingOtherFiles(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.go")
	missing := filepath.Join(dir, "missing.go")
	if err := os.WriteFile(good, []byte("package example\nfunc Useful() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(Options{Inputs: []string{missing, good}, Format: "toon"}, &out); err == nil {
		t.Fatal("missing file reported as success")
	}
	if !strings.Contains(out.String(), "Useful") || !strings.Contains(out.String(), "missing.go") {
		t.Fatalf("partial result lost: %s", out.String())
	}
}

func TestMissingFileJSONIsAnErrorDocument(t *testing.T) {
	var out bytes.Buffer
	if err := Run(Options{Inputs: []string{filepath.Join(t.TempDir(), "missing.php")}, Format: "json"}, &out); err == nil {
		t.Fatal("missing file reported as success")
	}
	var result map[string]string
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["error"] == "" {
		t.Fatalf("no error: %s", out.String())
	}
}
