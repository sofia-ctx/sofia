package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sofia-ctx/sofia/internal/plugin"
)

// isolate points both XDG_DATA_HOME and CLAUDE_DIR at fresh temp dirs, so a
// pack test never touches the real machine's plugin/claude shelves.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("CLAUDE_DIR", t.TempDir())
}

func mustWriteFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

// fullPack writes a pack source tree at dir with one instruction file, one
// claude skill (holding an executable script), and one bundled plugin —
// enough to exercise every apply step (plugins, claude, project files, canon,
// receipt) from a single Install call.
func fullPack(t *testing.T, dir, name, agentsBody string) {
	t.Helper()
	mustWriteFile(t, filepath.Join(dir, "pack.yaml"), `schema: 1
name: `+name+`
description: test pack
plugins:
  - path: plugins/widget
instructions:
  - src: instructions/AGENTS.md
claude:
  skills: [ { src: skills/my-skill } ]
`, 0o644)
	mustWriteFile(t, filepath.Join(dir, "instructions", "AGENTS.md"), agentsBody, 0o644)
	mustWriteFile(t, filepath.Join(dir, "skills", "my-skill", "SKILL.md"), "# my skill\n", 0o644)
	mustWriteFile(t, filepath.Join(dir, "skills", "my-skill", "run.sh"), "#!/bin/sh\necho hi\n", 0o755)
	mustWriteFile(t, filepath.Join(dir, "plugins", "widget", "plugin.yaml"), "schema: 1\nprotocol: \"1.0.0\"\n", 0o644)
	mustWriteFile(t, filepath.Join(dir, "plugins", "widget", "widget"), "#!/bin/sh\necho hi\n", 0o755)
}

func TestInstall_ProjectFiles(t *testing.T) {
	isolate(t)
	src := t.TempDir()
	fullPack(t, src, "acme", "# Agents\n")
	project := t.TempDir()

	res, err := Install(InstallOptions{Src: src, Project: project})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Name != "acme" {
		t.Errorf("name = %q", res.Name)
	}
	got, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md missing: %v", err)
	}
	if string(got) != "# Agents\n" {
		t.Errorf("AGENTS.md content = %q", got)
	}
}

// TestInstall_ConflictUnmanaged proves the two-phase contract: a project file
// sf doesn't own blocks the whole install, and — crucially — nothing else
// from the plan (the claude skill, the bundled plugin) gets written either.
func TestInstall_ConflictUnmanaged(t *testing.T) {
	isolate(t)
	src := t.TempDir()
	fullPack(t, src, "acme", "# Agents\n")
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "hand-written\n", 0o644)

	_, err := Install(InstallOptions{Src: src, Project: project})
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	if !strings.Contains(err.Error(), "1 conflict") || !strings.Contains(err.Error(), "AGENTS.md (exists, not managed by sf)") {
		t.Errorf("unexpected error: %v", err)
	}

	if got, _ := os.ReadFile(filepath.Join(project, "AGENTS.md")); string(got) != "hand-written\n" {
		t.Errorf("AGENTS.md was overwritten despite the conflict: %q", got)
	}
	if _, err := os.Stat(filepath.Join(claudeDir(), "skills", "my-skill", "SKILL.md")); err == nil {
		t.Error("claude skill was written despite a project-file conflict")
	}
	if _, ok := plugin.Find(plugin.Load(), "widget"); ok {
		t.Error("plugin was installed despite a project-file conflict")
	}
}

func TestInstall_ConflictDrifted(t *testing.T) {
	isolate(t)
	src := t.TempDir()
	fullPack(t, src, "acme", "# Agents\n")
	project := t.TempDir()

	if _, err := Install(InstallOptions{Src: src, Project: project}); err != nil {
		t.Fatalf("initial Install: %v", err)
	}
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "edited by hand\n", 0o644)

	_, err := Install(InstallOptions{Src: src, Project: project})
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	if !strings.Contains(err.Error(), "modified since install") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestInstall_ForceOverwrites(t *testing.T) {
	isolate(t)
	src := t.TempDir()
	fullPack(t, src, "acme", "# Agents\n")
	project := t.TempDir()

	if _, err := Install(InstallOptions{Src: src, Project: project}); err != nil {
		t.Fatalf("initial Install: %v", err)
	}
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "edited by hand\n", 0o644)

	if _, err := Install(InstallOptions{Src: src, Project: project, Force: true}); err != nil {
		t.Fatalf("forced Install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil || string(got) != "# Agents\n" {
		t.Errorf("AGENTS.md = %q, %v; want the pack's content restored", got, err)
	}
}

func TestInstall_Idempotent(t *testing.T) {
	isolate(t)
	src := t.TempDir()
	fullPack(t, src, "acme", "# Agents\n")
	project := t.TempDir()

	if _, err := Install(InstallOptions{Src: src, Project: project}); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	if _, err := Install(InstallOptions{Src: src, Project: project}); err != nil {
		t.Fatalf("second (idempotent) Install: %v", err)
	}
}

// TestInstall_Claude checks that a claude skill's executable file keeps its
// exec bit through the copy.
func TestInstall_Claude(t *testing.T) {
	isolate(t)
	src := t.TempDir()
	fullPack(t, src, "acme", "# Agents\n")
	project := t.TempDir()

	if _, err := Install(InstallOptions{Src: src, Project: project}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	script := filepath.Join(claudeDir(), "skills", "my-skill", "run.sh")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("run.sh missing: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("run.sh lost its exec bit: %v", info.Mode())
	}
}

// TestInstall_Plugins checks that a bundled plugin lands in the managed
// plugins tree and is visible to plugin.Load() — i.e. the cache was actually
// refreshed by Install's single plugin.Update() call.
func TestInstall_Plugins(t *testing.T) {
	isolate(t)
	src := t.TempDir()
	fullPack(t, src, "acme", "# Agents\n")
	project := t.TempDir()

	res, err := Install(InstallOptions{Src: src, Project: project})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Plugins) != 1 || res.Plugins[0] != "widget" {
		t.Errorf("Plugins = %v, want [widget]", res.Plugins)
	}
	d, ok := plugin.Find(plugin.Load(), "widget")
	if !ok || !d.Enabled {
		t.Errorf("plugin widget not visible/enabled after Install: %+v", d)
	}
}
