package code

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestOutputDeterministic pins `sf code`'s byte-for-byte stability: the same
// input must yield the same output on every run. Deterministic output is a
// cache feature — a stable, append-friendly prefix maximises provider
// prompt-cache hits (and the KV-cache multiplier) — so a regression here
// silently erodes a documented differentiator: a map iterated straight into
// output order, an unsorted directory walk, or the parallel multi-file
// aggregation reordered by goroutine completion. The dir input exercises both
// the sorted expansion and the parallel render; Force skips the dedup stub so
// each run re-emits the real output.
func TestOutputDeterministic(t *testing.T) {
	t.Setenv("SOFIA_CODE_RAW_BELOW", "0") // force structural rendering, not the small-file raw passthrough

	dir := t.TempDir()
	files := map[string]string{
		"a.go":  "package a\n\nimport \"fmt\"\n\ntype T struct{ X, Y int }\n\nfunc F(a, b int) int { return a + b }\n\nfunc (t T) M() string { return fmt.Sprint(t.X) }\n",
		"b.py":  "import os\nfrom typing import List\n\nCONST = 1\n\nclass Widget(Base, Mixin):\n    def __init__(self, n):\n        self.n = n\n\n    def run(self, x: int) -> bool:\n        return True\n\ndef helper(a, b=2):\n    return a\n",
		"c.php": "<?php\nnamespace App;\n\nclass Svc\n{\n    public function __construct(private Dep $d) {}\n    public function handle(int $x): bool { return true; }\n}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run := func(brief bool) []byte {
		var buf bytes.Buffer
		if err := Run(Options{Inputs: []string{dir}, Format: "toon", Brief: brief, Force: true}, &buf); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return buf.Bytes()
	}

	// Repeat many times: the parallel per-file render only reveals an ordering
	// bug intermittently, so one comparison isn't enough.
	for _, brief := range []bool{false, true} {
		first := run(brief)
		for i := 1; i <= 20; i++ {
			if got := run(brief); !bytes.Equal(got, first) {
				t.Fatalf("sf code output not deterministic (brief=%v, run %d):\n--- first ---\n%s\n--- run %d ---\n%s", brief, i, first, i, got)
			}
		}
	}
}
