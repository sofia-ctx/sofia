package plugin_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEnd_FixturePlugin is the load-bearing verification for the whole
// track: it builds the real `sf` binary and drives it as a subprocess against a
// managed fixture plugin. It proves discovery finds the plugin, the manifest
// parses, `sf hello greet` actually execs the plugin binary with the SOFIA_*
// env and argv, its stdout is captured, and exactly one correct calls.jsonl
// line is written — including on the plugin's non-zero-exit path. Nothing here
// is mocked; both sf and the plugin are real processes.
func TestEndToEnd_FixturePlugin(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the sf binary; skipped under -short")
	}
	bin := buildSF(t)

	t.Run("greet: real exec, env, stdout, one clean log line", func(t *testing.T) {
		dataDir, logDir := t.TempDir(), t.TempDir()
		installHello(t, dataDir)

		res := runSF(t, bin, dataDir, logDir, "hello", "greet", "there")
		if res.exit != 0 {
			t.Fatalf("exit=%d stderr=%q", res.exit, res.stderr)
		}
		if !strings.Contains(res.stdout, "greeting: Hello there") {
			t.Errorf("stdout missing greeting:\n%s", res.stdout)
		}
		if !strings.Contains(res.stdout, "format=toon") || !strings.Contains(res.stdout, "source=") {
			t.Errorf("plugin did not receive SOFIA_* env:\n%s", res.stdout)
		}

		lines := readLog(t, logDir)
		if len(lines) != 1 {
			t.Fatalf("want exactly 1 log line, got %d: %+v", len(lines), lines)
		}
		if lines[0].Tool != "hello.greet" || lines[0].Exit != 0 {
			t.Errorf("log entry = %+v, want tool=hello.greet exit=0", lines[0])
		}
		if lines[0].OutBytes <= 0 {
			t.Errorf("output not metered: %+v", lines[0])
		}
	})

	t.Run("boom: non-zero exit still yields one log line with the real code", func(t *testing.T) {
		dataDir, logDir := t.TempDir(), t.TempDir()
		installHello(t, dataDir)

		res := runSF(t, bin, dataDir, logDir, "hello", "boom")
		if res.exit == 0 {
			t.Fatalf("expected a non-zero sf exit for a crashing plugin")
		}
		lines := readLog(t, logDir)
		if len(lines) != 1 {
			t.Fatalf("want exactly 1 log line on crash, got %d: %+v", len(lines), lines)
		}
		if lines[0].Tool != "hello.boom" || lines[0].Exit != 3 {
			t.Errorf("log entry = %+v, want tool=hello.boom exit=3", lines[0])
		}
	})

	t.Run("plugin list surfaces the fixture as enabled", func(t *testing.T) {
		dataDir, logDir := t.TempDir(), t.TempDir()
		installHello(t, dataDir)

		res := runSF(t, bin, dataDir, logDir, "plugin", "list")
		if res.exit != 0 {
			t.Fatalf("exit=%d stderr=%q", res.exit, res.stderr)
		}
		if !strings.Contains(res.stdout, "hello") || !strings.Contains(res.stdout, "enabled") {
			t.Errorf("`sf plugin list` did not show the fixture enabled:\n%s", res.stdout)
		}
	})

	// Fork-bomb guard at the real-binary level: with a plugin whose executable
	// writes a sentinel when run, `sf --help` must list it (from its manifest)
	// without ever executing it — even with the cache cold.
	t.Run("sf --help lists plugins without forking them", func(t *testing.T) {
		dataDir, logDir := t.TempDir(), t.TempDir()
		sentinel := filepath.Join(t.TempDir(), "ran")
		writeWatcher(t, dataDir, sentinel)

		res := runSF(t, bin, dataDir, logDir, "--help")
		if res.exit != 0 {
			t.Fatalf("exit=%d stderr=%q", res.exit, res.stderr)
		}
		if !strings.Contains(res.stdout, "watcher") {
			t.Errorf("`sf --help` did not list the plugin:\n%s", res.stdout)
		}
		if _, err := os.Stat(sentinel); err == nil {
			t.Fatal("`sf --help` executed a plugin (fork bomb)")
		}
	})
}

// TestE2E_InstallFromGitURL drives the real sf binary through `sf plugin
// install <git-url>`: a real local git repo, cloned over file://, must land in
// the managed plugins dir and be visible to `sf plugin list` right after.
func TestE2E_InstallFromGitURL(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the sf binary; skipped under -short")
	}
	bin := buildSF(t)
	repo := gitPluginRepo(t)
	name := filepath.Base(repo)

	dataDir, logDir := t.TempDir(), t.TempDir()
	res := runSF(t, bin, dataDir, logDir, "plugin", "install", "file://"+repo)
	if res.exit != 0 {
		t.Fatalf("exit=%d stderr=%q", res.exit, res.stderr)
	}
	if !strings.Contains(res.stdout, "installed "+name) {
		t.Errorf("install output unexpected:\n%s", res.stdout)
	}

	res = runSF(t, bin, dataDir, logDir, "plugin", "list")
	if res.exit != 0 {
		t.Fatalf("exit=%d stderr=%q", res.exit, res.stderr)
	}
	if !strings.Contains(res.stdout, name) || !strings.Contains(res.stdout, "enabled") {
		t.Errorf("`sf plugin list` did not show the git-installed plugin:\n%s", res.stdout)
	}
}

// gitPluginRepo commits the hello fixture (plugin.yaml + executable) into a
// fresh git repo and returns its path, for cloning over file://. The repo dir
// is named "hello" to match the fixture's executable — managedExec defaults
// to the plugin dir's own name, and InstallFromGit names the plugin after the
// repo (gitclone.RepoName).
func gitPluginRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := filepath.Join(t.TempDir(), "hello")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("testdata", "plugins", "hello")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		info, _ := e.Info()
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
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

// installHello copies the committed fixture into a temp XDG data dir.
func installHello(t *testing.T, dataDir string) {
	t.Helper()
	dst := filepath.Join(dataDir, "sofia", "plugins", "hello")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("testdata", "plugins", "hello")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		info, _ := e.Info()
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
}

// writeWatcher installs a managed plugin whose executable touches sentinel when
// run — a tripwire for the fork-bomb assertion.
func writeWatcher(t *testing.T, dataDir, sentinel string) {
	t.Helper()
	dst := filepath.Join(dataDir, "sofia", "plugins", "watcher")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 1\nprotocol: \"1.0.0\"\ndescription: watcher plugin\ncommands:\n  - path: go\n    short: run watcher\n"
	if err := os.WriteFile(filepath.Join(dst, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho ran > " + sentinel + "\n"
	if err := os.WriteFile(filepath.Join(dst, "watcher"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

type result struct {
	stdout, stderr string
	exit           int
}

func runSF(t *testing.T, bin, dataDir, logDir string, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = childEnv(map[string]string{
		"XDG_DATA_HOME": dataDir,
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

type logEntry struct {
	Tool     string `json:"tool"`
	Exit     int    `json:"exit"`
	OutBytes int64  `json:"out_bytes"`
}

func readLog(t *testing.T, logDir string) []logEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(logDir, "calls.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []logEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e logEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad log line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}
