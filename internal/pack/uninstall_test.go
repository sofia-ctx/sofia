package pack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sofia-ctx/sofia/internal/plugin"
)

func TestUninstall_RemovesCleanKeepsModified(t *testing.T) {
	isolate(t)
	src := t.TempDir()
	fullPack(t, src, "xcraft", "# Agents\n")
	project := t.TempDir()

	if _, err := Install(InstallOptions{Src: src, Project: project}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "edited by hand\n", 0o644)

	res, err := Uninstall("xcraft", project)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !res.Global {
		t.Error("expected the last project's uninstall to tear down globals")
	}
	found := false
	for _, w := range res.Warnings {
		if w == "modified, left in place: AGENTS.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want a modified-left-in-place notice for AGENTS.md", res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); err != nil {
		t.Error("edited AGENTS.md should have been left in place")
	}

	if _, err := os.Stat(filepath.Join(claudeDir(), "skills", "my-skill", "SKILL.md")); err == nil {
		t.Error("untouched claude skill file should have been removed")
	}
	if _, ok := plugin.Find(plugin.Load(), "crm"); ok {
		t.Error("plugin should have been uninstalled")
	}
	if _, err := os.Stat(canonDir("xcraft")); err == nil {
		t.Error("canon copy should have been removed")
	}
	if _, found, err := loadReceipt("xcraft"); err != nil || found {
		t.Errorf("receipt should be gone: found=%v err=%v", found, err)
	}
}

func TestUninstall_SecondProjectKeepsGlobals(t *testing.T) {
	isolate(t)
	src := t.TempDir()
	fullPack(t, src, "xcraft", "# Agents\n")
	projectA := t.TempDir()
	projectB := t.TempDir()

	if _, err := Install(InstallOptions{Src: src, Project: projectA}); err != nil {
		t.Fatalf("Install A: %v", err)
	}
	if _, err := Install(InstallOptions{Src: src, Project: projectB}); err != nil {
		t.Fatalf("Install B: %v", err)
	}

	res, err := Uninstall("xcraft", projectA)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if res.Global {
		t.Error("globals should be kept while another project still references the pack")
	}
	if _, err := os.Stat(filepath.Join(projectA, "AGENTS.md")); err == nil {
		t.Error("project A's AGENTS.md should have been removed")
	}
	if _, err := os.Stat(filepath.Join(projectB, "AGENTS.md")); err != nil {
		t.Error("project B's AGENTS.md must survive")
	}
	if _, err := os.Stat(filepath.Join(claudeDir(), "skills", "my-skill", "SKILL.md")); err != nil {
		t.Error("claude files must survive while project B still references the pack")
	}
	if _, ok := plugin.Find(plugin.Load(), "crm"); !ok {
		t.Error("plugin must survive while project B still references the pack")
	}

	r, found, err := loadReceipt("xcraft")
	if err != nil || !found {
		t.Fatalf("receipt should still exist: found=%v err=%v", found, err)
	}
	if _, ok := r.Projects[projectA]; ok {
		t.Error("project A's entry should be gone from the receipt")
	}
	if _, ok := r.Projects[projectB]; !ok {
		t.Error("project B's entry should remain in the receipt")
	}
}
