package plugin

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written. upgradeCmd (like the rest of this package's commands) prints
// straight to os.Stdout rather than through cobra's OutOrStdout, so this is
// the only way to assert on its reported message.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = orig
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestUpgradeReclones(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	repo := gitPluginRepo(t)

	name, err := InstallFromGit("file://"+repo, "")
	if err != nil {
		t.Fatalf("InstallFromGit: %v", err)
	}
	before, err := readOrigin(name)
	if err != nil {
		t.Fatalf("readOrigin before upgrade: %v", err)
	}

	// Give the fixture repo a new commit for upgrade to pick up.
	if err := os.WriteFile(filepath.Join(repo, "NEWFILE"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "commit", "--quiet", "-m", "add NEWFILE")

	c := upgradeCmd()
	c.SetArgs([]string{name})
	var runErr error
	out := captureStdout(t, func() { runErr = c.Execute() })
	if runErr != nil {
		t.Fatalf("upgrade: %v", runErr)
	}

	after, err := readOrigin(name)
	if err != nil {
		t.Fatalf("readOrigin after upgrade: %v", err)
	}
	if after.Commit == before.Commit {
		t.Fatalf("commit did not change: %s", after.Commit)
	}
	if _, err := os.Stat(filepath.Join(PluginsDir(), name, "NEWFILE")); err != nil {
		t.Errorf("NEWFILE missing from the re-installed tree: %v", err)
	}
	want := "upgraded " + name + ": " + before.Commit[:7] + " → " + after.Commit[:7]
	if !strings.Contains(out, want) {
		t.Errorf("output = %q, want it to contain %q", out, want)
	}
}

func TestUpgradeUpToDate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	repo := gitPluginRepo(t)

	name, err := InstallFromGit("file://"+repo, "")
	if err != nil {
		t.Fatalf("InstallFromGit: %v", err)
	}

	c := upgradeCmd()
	c.SetArgs([]string{name})
	var runErr error
	out := captureStdout(t, func() { runErr = c.Execute() })
	if runErr != nil {
		t.Fatalf("upgrade: %v", runErr)
	}
	if !strings.Contains(out, name+" is up to date") {
		t.Errorf("output = %q, want an up-to-date message", out)
	}
}

func TestUpgradeNotGitInstall(t *testing.T) {
	isolate(t)
	writeManaged(t, "local", "schema: 1\nprotocol: \"1.0.0\"\n", "echo hi\n")

	c := upgradeCmd()
	c.SetArgs([]string{"local"})
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	if err := c.Execute(); err == nil {
		t.Fatal("expected an error upgrading a locally-installed plugin")
	}
}

func TestUpgradeAllSkipsLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	repo := gitPluginRepo(t)

	gitName, err := InstallFromGit("file://"+repo, "")
	if err != nil {
		t.Fatalf("InstallFromGit: %v", err)
	}
	writeManaged(t, "local", "schema: 1\nprotocol: \"1.0.0\"\n", "echo hi\n")

	c := upgradeCmd()
	c.SetArgs(nil)
	var runErr error
	out := captureStdout(t, func() { runErr = c.Execute() })
	if runErr != nil {
		t.Fatalf("upgrade (all): %v", runErr)
	}
	if !strings.Contains(out, "skipping local (not a git install)") {
		t.Errorf("output = %q, want a skip line for the local install", out)
	}
	if strings.Contains(out, "skipping "+gitName) {
		t.Errorf("output = %q, the git-installed plugin should not be skipped", out)
	}

	ds := Load()
	if _, ok := Find(ds, "local"); !ok {
		t.Error("local-only plugin should remain installed, untouched")
	}
	if d, ok := Find(ds, gitName); !ok || !d.Enabled {
		t.Errorf("git-installed plugin not enabled after upgrade-all: %+v", d)
	}
}
