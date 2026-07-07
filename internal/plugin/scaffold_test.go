package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewScaffoldInstallable(t *testing.T) {
	isolate(t)
	parent := t.TempDir()

	dst, err := Scaffold("hello", parent)
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if want := filepath.Join(parent, "hello"); dst != want {
		t.Errorf("Scaffold returned %q, want %q", dst, want)
	}

	// The stub must actually be executable — managedExec checks the bit.
	info, err := os.Stat(filepath.Join(dst, "hello"))
	if err != nil {
		t.Fatalf("stub missing: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("scaffolded stub is not executable: %v", info.Mode())
	}

	name, err := Install(dst)
	if err != nil {
		t.Fatalf("Install(scaffold): %v", err)
	}
	d, ok := Find(Load(), name)
	if !ok {
		t.Fatal("scaffolded plugin not discovered after install")
	}
	if !d.Enabled {
		t.Errorf("scaffolded plugin not enabled: %+v", d)
	}
	if !d.IsGroup() {
		t.Errorf("scaffolded plugin should expose the example command: %+v", d)
	}
}

func TestNewRejectsExistingDir(t *testing.T) {
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "hello"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold("hello", parent); err == nil {
		t.Fatal("expected an error scaffolding over an already-existing directory")
	}
	// And it must not have touched what was there.
	entries, err := os.ReadDir(filepath.Join(parent, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("existing dir was written into: %v", entries)
	}
}

func TestNewRejectsBadName(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"", ".", "..", "a/b", "a/", "/etc"} {
		if _, err := Scaffold(name, parent); err == nil {
			t.Errorf("Scaffold(%q, ...): expected an error", name)
		}
	}
}
