package matcher

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScanFileSkipsOverLongLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bundle.min.js")
	big := make([]byte, 5*1024*1024) // one line past the 4MB scanner cap
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(p, big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ScanFile(p, Options{Patterns: []string{"x"}}); !errors.Is(err, ErrSkip) {
		t.Fatalf("over-long line: want ErrSkip, got %v", err)
	}
}

func TestScanFileSkipsBinary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(p, []byte("abc\x00def needle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ScanFile(p, Options{Patterns: []string{"needle"}}); !errors.Is(err, ErrSkip) {
		t.Fatalf("binary file: want ErrSkip, got %v", err)
	}
}

func TestScanFileNormalStillMatches(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(p, []byte("hello needle world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hits, _, err := ScanFile(p, Options{Patterns: []string{"needle"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(hits))
	}
}
