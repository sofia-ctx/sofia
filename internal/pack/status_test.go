package pack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatus_Drift(t *testing.T) {
	isolate(t)
	src := t.TempDir()
	fullPack(t, src, "xcraft", "# Agents\n")
	project := t.TempDir()

	if _, err := Install(InstallOptions{Src: src, Project: project}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Untouched: status should be all-ok right after install.
	st, err := Status("xcraft")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Modified != 0 || st.Missing != 0 || st.Ok == 0 {
		t.Fatalf("fresh install status = %+v, want all ok", st)
	}

	// Hand-edit a project file, delete a claude file.
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "edited\n", 0o644)
	if err := os.Remove(filepath.Join(claudeDir(), "skills", "my-skill", "run.sh")); err != nil {
		t.Fatal(err)
	}

	st, err = Status("xcraft")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Modified != 1 {
		t.Errorf("Modified = %d, want 1", st.Modified)
	}
	if st.Missing != 1 {
		t.Errorf("Missing = %d, want 1", st.Missing)
	}
	if st.Ok != 1 {
		t.Errorf("Ok = %d, want 1", st.Ok)
	}
}

func TestStatus_NotInstalled(t *testing.T) {
	isolate(t)
	if _, err := Status("nope"); err == nil {
		t.Fatal("expected an error for a pack with no receipt")
	}
}
