package pack_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2E_PackLifecycle drives the real sf binary through a pack's full
// lifecycle: install from a local git repo (a pack.yaml with one instruction
// file and one bundled plugin), verify the project file and the plugin both
// landed, verify `sf pack status` reports clean, then uninstall and verify
// the project file is gone again. Nothing here is mocked — sf, git and the
// plugin machinery are all real, same load-bearing role as
// internal/plugin/e2e_test.go's TestEndToEnd_FixturePlugin.
func TestE2E_PackLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the sf binary; skipped under -short")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := buildSF(t)
	repo := gitPackRepo(t)

	dataDir, claudeDir, logDir := t.TempDir(), t.TempDir(), t.TempDir()
	project := t.TempDir()

	res := runSF(t, bin, dataDir, claudeDir, logDir, "pack", "install", "file://"+repo, "--project", project)
	if res.exit != 0 {
		t.Fatalf("install: exit=%d stderr=%q stdout=%q", res.exit, res.stderr, res.stdout)
	}
	if !strings.Contains(res.stdout, "installed acme") {
		t.Errorf("install output unexpected:\n%s", res.stdout)
	}
	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md not installed: %v", err)
	}

	res = runSF(t, bin, dataDir, claudeDir, logDir, "plugin", "list")
	if res.exit != 0 {
		t.Fatalf("plugin list: exit=%d stderr=%q", res.exit, res.stderr)
	}
	if !strings.Contains(res.stdout, "widget") || !strings.Contains(res.stdout, "enabled") {
		t.Errorf("`sf plugin list` did not show the pack's plugin:\n%s", res.stdout)
	}

	res = runSF(t, bin, dataDir, claudeDir, logDir, "pack", "status", "acme")
	if res.exit != 0 {
		t.Fatalf("status: exit=%d stderr=%q", res.exit, res.stderr)
	}
	if !strings.Contains(res.stdout, "ok") {
		t.Errorf("status did not report ok:\n%s", res.stdout)
	}

	res = runSF(t, bin, dataDir, claudeDir, logDir, "pack", "uninstall", "acme", "--project", project)
	if res.exit != 0 {
		t.Fatalf("uninstall: exit=%d stderr=%q", res.exit, res.stderr)
	}
	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); err == nil {
		t.Error("AGENTS.md should have been removed by uninstall")
	}
}

// gitPackRepo commits a small pack (pack.yaml + one instruction file + one
// bundled plugin) into a fresh git repo and returns its path, for cloning
// over file://.
func gitPackRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "pack.yaml"), `schema: 1
name: acme
description: e2e test pack
plugins:
  - path: plugins/widget
instructions:
  - src: instructions/AGENTS.md
`, 0o644)
	writeFixture(t, filepath.Join(dir, "instructions", "AGENTS.md"), "# Agents\n", 0o644)
	writeFixture(t, filepath.Join(dir, "plugins", "widget", "plugin.yaml"), "schema: 1\nprotocol: \"1.0.0\"\n", 0o644)
	writeFixture(t, filepath.Join(dir, "plugins", "widget", "widget"), "#!/bin/sh\necho hi\n", 0o755)

	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "--quiet", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func writeFixture(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

func buildSF(t *testing.T) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	bin := filepath.Join(t.TempDir(), "sf")
	build := exec.Command(goBin, "build", "-o", bin, "github.com/sofia-ctx/sofia/cmd/sf")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sf: %v\n%s", err, out)
	}
	return bin
}

type result struct {
	stdout, stderr string
	exit           int
}

func runSF(t *testing.T, bin, dataDir, claudeDir, logDir string, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = childEnv(map[string]string{
		"XDG_DATA_HOME": dataDir,
		"CLAUDE_DIR":    claudeDir,
		"SOFIA_LOG_DIR": logDir,
		"SOFIA_SOURCE":  "test",
		"SOFIA_TAG":     "e2e",
	})
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("run sf %v: %v", args, err)
		}
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), exit: exit}
}

// childEnv starts from the current environment, drops any keys we override
// (glibc getenv returns the first match, so a shadowed duplicate would win),
// and appends the overrides.
func childEnv(overrides map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, ok := overrides[key]; ok {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}
