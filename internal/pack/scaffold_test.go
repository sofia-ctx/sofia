package pack

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPackNewInstallable proves the scaffold's pack.yaml actually validates
// and round-trips: a fresh `sf pack new` skeleton installs into a target
// project without any edits.
func TestPackNewInstallable(t *testing.T) {
	isolate(t)
	parent := t.TempDir()

	dst, err := Scaffold("demo", parent)
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if want := filepath.Join(parent, "demo"); dst != want {
		t.Errorf("Scaffold returned %q, want %q", dst, want)
	}

	project := t.TempDir()
	res, err := Install(InstallOptions{Src: dst, Project: project})
	if err != nil {
		t.Fatalf("Install(scaffold): %v", err)
	}
	if res.Name != "demo" {
		t.Errorf("Name = %q, want %q", res.Name, "demo")
	}
	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md not installed: %v", err)
	}
}

func TestPackNewRejectsExisting(t *testing.T) {
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold("demo", parent); err == nil {
		t.Fatal("expected an error scaffolding over an already-existing directory")
	}
	entries, err := os.ReadDir(filepath.Join(parent, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("existing dir was written into: %v", entries)
	}
}

func TestPackNewRejectsBadName(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"", ".", "..", "Demo", "a/b", "-demo"} {
		if _, err := Scaffold(name, parent); err == nil {
			t.Errorf("Scaffold(%q, ...): expected an error", name)
		}
	}
}
