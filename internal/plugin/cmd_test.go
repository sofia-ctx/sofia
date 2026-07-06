package plugin

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderList_FormatsAndStatus(t *testing.T) {
	isolate(t)
	writeManaged(t, "hello", "schema: 1\nprotocol: \"1.0.0\"\nversion: \"0.1.0\"\ncommands:\n  - path: greet\n", "echo hi\n")
	writeManaged(t, "future", "schema: 1\nprotocol: \"99.0.0\"\nversion: \"9.9\"\n", "echo hi\n")
	ds := Load()

	var toonBuf bytes.Buffer
	if err := RenderList(&toonBuf, "toon", ds); err != nil {
		t.Fatal(err)
	}
	toonOut := toonBuf.String()
	if !strings.Contains(toonOut, "plugins[2]{name,kind,status,version,reason}:") {
		t.Errorf("toon header missing:\n%s", toonOut)
	}
	if !strings.Contains(toonOut, "hello,managed,enabled,0.1.0") {
		t.Errorf("enabled row missing:\n%s", toonOut)
	}
	if !strings.Contains(toonOut, "future,managed,disabled") || !strings.Contains(toonOut, "newer") {
		t.Errorf("disabled row must carry a reason:\n%s", toonOut)
	}

	var jsonBuf bytes.Buffer
	if err := RenderList(&jsonBuf, "json", ds); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Plugins []listRow `json:"plugins"`
	}
	if err := json.Unmarshal(jsonBuf.Bytes(), &parsed); err != nil {
		t.Fatalf("json list not parseable: %v\n%s", err, jsonBuf.String())
	}
	if len(parsed.Plugins) != 2 {
		t.Errorf("json list wrong length: %+v", parsed.Plugins)
	}
}

func TestRenderInfo_Detail(t *testing.T) {
	isolate(t)
	writeManaged(t, "hello",
		"schema: 1\nprotocol: \"1.0.0\"\nversion: \"0.1.0\"\nmin_sf: \"1.0.0\"\ndescription: greeter\ncapabilities: [stdin-json]\ncommands:\n  - path: greet\n    short: say hi\nsettings:\n  - key: HELLO_GREETING\n    default: Hello\n",
		"echo hi\n")
	d, _ := Find(Load(), "hello")

	var buf bytes.Buffer
	if err := RenderInfo(&buf, "toon", d); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"name: hello", "protocol: 1.0.0", "min_sf: 1.0.0",
		"commands[1]{path,short}:", "greet,", "settings[1]", "HELLO_GREETING", "capabilities: stdin-json"} {
		if !strings.Contains(out, want) {
			t.Errorf("info toon missing %q:\n%s", want, out)
		}
	}

	// A disabled plugin's info must state the reason.
	writeManaged(t, "future", "schema: 1\nprotocol: \"99.0.0\"\n", "echo hi\n")
	fd, _ := Find(Load(), "future")
	var fb bytes.Buffer
	if err := RenderInfo(&fb, "toon", fd); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fb.String(), "reason:") || !strings.Contains(fb.String(), "newer") {
		t.Errorf("disabled info must include the reason:\n%s", fb.String())
	}
}

func TestInstallUninstall(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("PATH", "")

	// A source plugin dir outside the managed tree.
	src := filepath.Join(t.TempDir(), "hello")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, manifestFile), []byte("schema: 1\nprotocol: \"1.0.0\"\ncommands:\n  - path: greet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "hello"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	name, err := Install(src)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if name != "hello" {
		t.Errorf("installed name = %q", name)
	}
	d, ok := Find(Load(), "hello")
	if !ok || !d.Enabled {
		t.Fatalf("installed plugin not enabled: %+v", d)
	}
	// The executable bit must survive the copy.
	if !isExecutable(d.Exec) {
		t.Errorf("copied executable lost its exec bit: %s", d.Exec)
	}

	if err := Uninstall("hello"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, ok := Find(Load(), "hello"); ok {
		t.Error("plugin still present after Uninstall")
	}
}

func TestInstall_RejectsDirWithoutManifest(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	src := t.TempDir()
	if _, err := Install(src); err == nil {
		t.Fatal("expected an error installing a dir without plugin.yaml")
	}
}

// mustGit runs a git subcommand in dir, failing the test on error. Building
// fixture repos this way (rather than mocking) is what actually exercises
// InstallFromGit's clone path.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitPluginRepo commits the hello fixture (plugin.yaml + executable) into a
// fresh git repo and returns its path. The repo dir is named "hello" to match
// the fixture's executable — managedExec defaults to the plugin dir's own
// name, and InstallFromGit names the plugin after the repo (gitclone.RepoName).
func gitPluginRepo(t *testing.T) string {
	t.Helper()
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
	mustGit(t, dir, "init", "--quiet")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "--quiet", "-m", "init")
	return dir
}

func TestInstallFromGit(t *testing.T) {
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
	if name != filepath.Base(repo) {
		t.Errorf("installed name = %q, want %q", name, filepath.Base(repo))
	}

	dir := filepath.Join(PluginsDir(), name)
	if _, err := os.Stat(filepath.Join(dir, "plugin.yaml")); err != nil {
		t.Errorf("plugin.yaml missing from installed tree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Error(".git leaked into the managed plugin dir")
	}
	if d, ok := Find(Load(), name); !ok || !d.Enabled {
		t.Errorf("git-installed plugin not enabled: %+v", d)
	}

	raw, err := os.ReadFile(filepath.Join(dir, originFile))
	if err != nil {
		t.Fatalf("%s missing: %v", originFile, err)
	}
	var o origin
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatalf("%s not valid JSON: %v", originFile, err)
	}
	if o.URL != "file://"+repo || o.Commit == "" {
		t.Errorf("origin = %+v, want url=%q and a non-empty commit", o, "file://"+repo)
	}
	if o.Ref != "" {
		t.Errorf("empty ref should be omitted, got %q", o.Ref)
	}
}

func TestInstallCmd_RefRequiresURL(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("PATH", "")

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, manifestFile), []byte("schema: 1\nprotocol: \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := installCmd()
	c.SetArgs([]string{src, "--ref", "main"})
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	if err := c.Execute(); err == nil {
		t.Fatal("expected --ref with a local directory to error")
	}
}
