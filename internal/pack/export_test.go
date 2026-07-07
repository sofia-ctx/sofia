package pack

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sofia-ctx/sofia/internal/plugin"
)

// installPluginFixture installs a minimal managed plugin named name (its
// directory's own basename becomes the plugin name, same convention
// plugin.Install uses) and refreshes the discovery cache.
func installPluginFixture(t *testing.T, name string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	mustWriteFile(t, filepath.Join(dir, "plugin.yaml"), "schema: 1\nprotocol: \"1.0.0\"\n", 0o644)
	mustWriteFile(t, filepath.Join(dir, name), "#!/bin/sh\necho hi\n", 0o755)
	if _, err := plugin.Install(dir); err != nil {
		t.Fatalf("plugin.Install: %v", err)
	}
	if _, err := plugin.Update(); err != nil {
		t.Fatalf("plugin.Update: %v", err)
	}
}

func TestExportCapturesAgentsAndPlugins(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "# Agents\n", 0o644)
	installPluginFixture(t, "crm")

	out := t.TempDir()
	res, err := Export(ExportOptions{Name: "demo", Project: project, Out: out})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !res.HasAgents {
		t.Error("HasAgents = false, want true")
	}
	if len(res.Plugins) != 1 || res.Plugins[0] != "crm" {
		t.Errorf("Plugins = %v, want [crm]", res.Plugins)
	}

	data, err := os.ReadFile(filepath.Join(out, "demo", "pack.yaml"))
	if err != nil {
		t.Fatalf("pack.yaml missing: %v", err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.Instructions) != 1 || m.Instructions[0].Src != filepath.FromSlash("instructions/AGENTS.md") || m.Instructions[0].Dest != "AGENTS.md" {
		t.Errorf("instructions = %+v", m.Instructions)
	}
	if len(m.Plugins) != 1 || m.Plugins[0].Path != filepath.FromSlash("plugins/crm") {
		t.Errorf("plugins = %+v", m.Plugins)
	}

	if _, err := os.Stat(filepath.Join(out, "demo", "plugins", "crm", "plugin.yaml")); err != nil {
		t.Errorf("plugin.yaml not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "demo", "plugins", "crm", originFile)); !os.IsNotExist(err) {
		t.Errorf(".sf-origin.json should be absent, stat err = %v", err)
	}
}

// TestExportRoundTrips proves the exported pack is actually installable:
// after uninstalling the plugin captured above, installing the exported pack
// into a fresh project is the only thing that brings AGENTS.md and the
// plugin back.
func TestExportRoundTrips(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "# Agents\n", 0o644)
	installPluginFixture(t, "crm")

	out := t.TempDir()
	if _, err := Export(ExportOptions{Name: "demo", Project: project, Out: out}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	if err := plugin.Uninstall("crm"); err != nil {
		t.Fatalf("plugin.Uninstall: %v", err)
	}
	if _, err := plugin.Update(); err != nil {
		t.Fatalf("plugin.Update: %v", err)
	}

	target := t.TempDir()
	res, err := Install(InstallOptions{Src: filepath.Join(out, "demo"), Project: target})
	if err != nil {
		t.Fatalf("Install(exported pack): %v", err)
	}
	if res.Name != "demo" {
		t.Errorf("Name = %q, want %q", res.Name, "demo")
	}
	if got, err := os.ReadFile(filepath.Join(target, "AGENTS.md")); err != nil || string(got) != "# Agents\n" {
		t.Errorf("AGENTS.md = %q, %v", got, err)
	}
	if d, ok := plugin.Find(plugin.Load(), "crm"); !ok || !d.Enabled {
		t.Errorf("plugin crm not reinstalled/enabled: %+v", d)
	}
}

func TestExportNothingToCapture(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	out := t.TempDir()

	if _, err := Export(ExportOptions{Name: "demo", Project: project, Out: out}); err == nil {
		t.Fatal("expected an error when there is nothing to capture")
	}
}

func TestExportRejectsExistingOutWithoutForce(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "# Agents\n", 0o644)

	out := t.TempDir()
	mustWriteFile(t, filepath.Join(out, "demo", "sentinel"), "keep me\n", 0o644)

	if _, err := Export(ExportOptions{Name: "demo", Project: project, Out: out}); err == nil {
		t.Fatal("expected an error exporting over an existing directory")
	}
	if _, err := os.Stat(filepath.Join(out, "demo", "sentinel")); err != nil {
		t.Errorf("existing dir should not have been touched: %v", err)
	}
}

func TestExportForceOverwrites(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "# Agents\n", 0o644)

	out := t.TempDir()
	mustWriteFile(t, filepath.Join(out, "demo", "sentinel"), "stale\n", 0o644)

	if _, err := Export(ExportOptions{Name: "demo", Project: project, Out: out, Force: true}); err != nil {
		t.Fatalf("Export --force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "demo", "sentinel")); !os.IsNotExist(err) {
		t.Errorf("stale sentinel should have been removed by --force, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "demo", "pack.yaml")); err != nil {
		t.Errorf("pack.yaml missing after forced export: %v", err)
	}
}

// TestExportStripsOrigin installs a plugin from a local git repo (so it
// carries .sf-origin.json, same as any real `sf plugin install <git-url>`)
// and proves Export strips that marker from its copy — the exported pack
// references the plugin by path, not git, so the marker would be stale.
func TestExportStripsOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	isolate(t)

	// The repo's own directory name becomes the plugin name.
	repo := filepath.Join(t.TempDir(), "crm")
	mustWriteFile(t, filepath.Join(repo, "plugin.yaml"), "schema: 1\nprotocol: \"1.0.0\"\n", 0o644)
	mustWriteFile(t, filepath.Join(repo, "crm"), "#!/bin/sh\necho hi\n", 0o755)
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "--quiet", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if _, err := plugin.InstallFromGit("file://"+repo, ""); err != nil {
		t.Fatalf("InstallFromGit: %v", err)
	}
	if _, err := plugin.Update(); err != nil {
		t.Fatalf("plugin.Update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plugin.PluginsDir(), "crm", originFile)); err != nil {
		t.Fatalf("origin marker missing before export (test setup broken): %v", err)
	}

	project := t.TempDir()
	out := t.TempDir()
	if _, err := Export(ExportOptions{Name: "demo", Project: project, Out: out}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "demo", "plugins", "crm", originFile)); !os.IsNotExist(err) {
		t.Errorf("origin marker should be stripped from the exported copy, stat err = %v", err)
	}
}
