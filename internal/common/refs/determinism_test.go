package refs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestOutputDeterministic pins `sf refs`'s byte-for-byte stability across runs
// on the same tree: the same symbol map must come back identically every time,
// hits ordered from a sorted walk rather than OS directory order. Deterministic
// output keeps the result a stable, cacheable prefix — see the code package's
// equivalent for the rationale.
func TestOutputDeterministic(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go":     "package a\n\ntype Widget struct{ N int }\n\nfunc New() Widget { return Widget{} }\n",
		"b.go":     "package a\n\nfunc Use(w Widget) Widget { return w }\n",
		"sub/c.go": "package sub\n\nimport \"example/a\"\n\nfunc C() a.Widget { return a.Widget{} }\n",
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
		if err := Run(Options{Symbol: "Widget", Root: dir, Format: "toon"}, &buf); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return buf.Bytes()
	}

	first := run()
	for i := 1; i <= 16; i++ {
		if got := run(); !bytes.Equal(got, first) {
			t.Fatalf("sf refs output not deterministic (run %d):\n--- first ---\n%s\n--- run %d ---\n%s", i, first, i, got)
		}
	}
}
