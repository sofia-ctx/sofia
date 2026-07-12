package grep

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestOutputDeterministic pins `sf grep`'s byte-for-byte stability across runs
// on the same tree. A cross-file scan must order its hits from a sorted walk,
// not from OS directory order — deterministic output is what makes the result
// a stable, cacheable prefix. See the code package's equivalent for why this
// matters.
func TestOutputDeterministic(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go":     "package a\n\ntype Widget struct{}\n\nfunc UseWidget() Widget { return Widget{} }\n",
		"b.go":     "package a\n\nfunc AlsoWidget(w Widget) {}\n",
		"sub/c.go": "package sub\n\nvar w = \"Widget here\"\n",
		"sub/d.go": "package sub\n\n// Widget in a comment\nfunc D() {}\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run := func() []byte {
		var buf bytes.Buffer
		if err := Run(Options{Root: dir, Patterns: []string{"Widget"}, Format: "toon", WordBound: true}, &buf); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return buf.Bytes()
	}

	first := run()
	for i := 1; i <= 16; i++ {
		if got := run(); !bytes.Equal(got, first) {
			t.Fatalf("sf grep output not deterministic (run %d):\n--- first ---\n%s\n--- run %d ---\n%s", i, first, i, got)
		}
	}
}
