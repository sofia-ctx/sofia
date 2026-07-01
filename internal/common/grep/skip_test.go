package grep

import (
	"os"
	"path/filepath"
	"testing"
)

// One pathological file (minified blob / binary) must not abort the whole
// search — it is skipped and counted, the rest still matches.
func TestScanSkipsBadFilesNotFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.txt"), []byte("find NeedleX here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 5*1024*1024)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(dir, "huge.min.js"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte("\x00NeedleX"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := scan(Options{Patterns: []string{"NeedleX"}, CaseSensitive: true, WordBound: true}, dir)
	if err != nil {
		t.Fatalf("scan must not fail because of skippable files: %v", err)
	}
	if res.Skipped < 2 {
		t.Errorf("want >=2 skipped (huge + binary), got %d", res.Skipped)
	}
	var hits int
	for _, pr := range res.Patterns {
		hits += len(pr.Hits)
	}
	if hits != 1 {
		t.Errorf("want 1 hit from good.txt, got %d", hits)
	}
}
