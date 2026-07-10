package walker

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// drainFiles consumes the producer channels and returns the relative paths
// (POSIX-style) of every file the walker emitted, plus any walk error.
func drainFiles(t *testing.T, opts Options) []string {
	t.Helper()
	out, errs := Files(opts)
	var got []string
	for p := range out {
		rel, _ := filepath.Rel(opts.Root, p)
		got = append(got, filepath.ToSlash(rel))
	}
	for err := range errs {
		t.Errorf("walk error: %v", err)
	}
	sort.Strings(got)
	return got
}

func mkfile(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWalker_ExtensionFilter(t *testing.T) {
	root := t.TempDir()
	for _, r := range []string{
		"a.php", "b.ts", "c.txt", "d.go", "sub/e.ini", "sub/f.bin",
	} {
		mkfile(t, root, r)
	}
	got := drainFiles(t, Options{
		Root: root,
		Exts: map[string]bool{".php": true, ".ts": true, ".ini": true},
	})
	want := []string{"a.php", "b.ts", "sub/e.ini"}
	if !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWalker_IgnoreDirs(t *testing.T) {
	root := t.TempDir()
	for _, r := range []string{
		"keep.php", "vendor/skip.php", "node_modules/x.ts", "src/keep.ts",
	} {
		mkfile(t, root, r)
	}
	got := drainFiles(t, Options{
		Root:       root,
		IgnoreDirs: map[string]bool{"vendor": true, "node_modules": true},
		Exts:       map[string]bool{".php": true, ".ts": true},
	})
	want := []string{"keep.php", "src/keep.ts"}
	if !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWalker_IgnoreRels(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "a.php")
	mkfile(t, root, "src/Model/keep.php")
	mkfile(t, root, "src/Model/Raw/skip.php")
	mkfile(t, root, "scripts/dto/skip.ts")
	mkfile(t, root, "scripts/keep.ts")

	got := drainFiles(t, Options{
		Root:       root,
		IgnoreRels: map[string]bool{"src/Model/Raw": true, "scripts/dto": true},
		Exts:       map[string]bool{".php": true, ".ts": true},
	})
	want := []string{"a.php", "scripts/keep.ts", "src/Model/keep.php"}
	if !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWalker_NoExtsAcceptsAll(t *testing.T) {
	root := t.TempDir()
	for _, r := range []string{"a.x", "b.y", "c"} {
		mkfile(t, root, r)
	}
	got := drainFiles(t, Options{Root: root})
	want := []string{"a.x", "b.y", "c"}
	if !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
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
