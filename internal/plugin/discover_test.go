package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManaged creates a managed plugin under $XDG_DATA_HOME/sofia/plugins/<name>/
// with the given manifest text and an executable script. execBody is the shell
// body after the shebang.
func writeManaged(t *testing.T, name, manifest, execBody string) {
	t.Helper()
	dir := filepath.Join(PluginsDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" + execBody
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// isolate points every plugin path at a fresh temp dir and empties $PATH so no
// real machine plugins leak into the test.
func isolate(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("PATH", "")
	return data
}

func TestLoad_ManagedEnabled(t *testing.T) {
	isolate(t)
	writeManaged(t, "hello", "schema: 1\nprotocol: \"1.0.0\"\nversion: \"0.1.0\"\ndescription: greeter\ncommands:\n  - path: greet\n    short: say hi\n", "echo hi\n")

	ds := Load()
	d, ok := Find(ds, "hello")
	if !ok {
		t.Fatal("hello not discovered")
	}
	if !d.Enabled || d.Reason != "" {
		t.Errorf("want enabled, got enabled=%v reason=%q", d.Enabled, d.Reason)
	}
	if d.Kind != Managed || !d.IsGroup() {
		t.Errorf("kind=%q isGroup=%v", d.Kind, d.IsGroup())
	}
	if !strings.HasSuffix(d.Exec, filepath.Join("hello", "hello")) {
		t.Errorf("exec resolved wrong: %q", d.Exec)
	}
}

func TestLoad_IncompatibleDisabledWithReason(t *testing.T) {
	isolate(t)
	// Protocol far in the future → too new for the host.
	writeManaged(t, "future", "schema: 1\nprotocol: \"99.0.0\"\n", "echo hi\n")

	ds := Load()
	d, ok := Find(ds, "future")
	if !ok {
		t.Fatal("future not discovered")
	}
	if d.Enabled {
		t.Fatal("incompatible plugin must be disabled")
	}
	if !strings.Contains(d.Reason, "newer") {
		t.Errorf("reason should explain the incompatibility, got %q", d.Reason)
	}
}

func TestLoad_InvalidManifestDisabledWithReason(t *testing.T) {
	isolate(t)
	writeManaged(t, "broken", "commands: [unterminated\n", "echo hi\n")

	ds := Load()
	d, ok := Find(ds, "broken")
	if !ok {
		t.Fatal("broken not discovered")
	}
	if d.Enabled || !strings.Contains(d.Reason, "invalid plugin.yaml") {
		t.Errorf("want disabled with invalid-manifest reason, got enabled=%v reason=%q", d.Enabled, d.Reason)
	}
}

func TestLoad_MissingExecutableDisabledWithReason(t *testing.T) {
	isolate(t)
	dir := filepath.Join(PluginsDir(), "noexe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte("schema: 1\nprotocol: \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ds := Load()
	d, ok := Find(ds, "noexe")
	if !ok {
		t.Fatal("noexe not discovered")
	}
	if d.Enabled || !strings.Contains(d.Reason, "no runnable executable") {
		t.Errorf("want disabled with missing-exe reason, got enabled=%v reason=%q", d.Enabled, d.Reason)
	}
}

func TestLoad_ConventionOnPath(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if err := os.WriteFile(filepath.Join(bin, "sf-conv"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A non-executable sf-* file must be ignored.
	if err := os.WriteFile(filepath.Join(bin, "sf-notexe"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ds := Load()
	d, ok := Find(ds, "conv")
	if !ok {
		t.Fatal("sf-conv not discovered as a convention plugin")
	}
	if d.Kind != Convention || !d.Enabled || d.IsGroup() {
		t.Errorf("convention plugin wrong: kind=%q enabled=%v isGroup=%v", d.Kind, d.Enabled, d.IsGroup())
	}
	if _, ok := Find(ds, "notexe"); ok {
		t.Error("non-executable sf-notexe should not be discovered")
	}
}

func TestLoad_ManagedWinsOverConvention(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if err := os.WriteFile(filepath.Join(bin, "sf-dup"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeManaged(t, "dup", "schema: 1\nprotocol: \"1.0.0\"\ndescription: managed dup\n", "echo hi\n")

	ds := Load()
	d, ok := Find(ds, "dup")
	if !ok {
		t.Fatal("dup not discovered")
	}
	if d.Kind != Managed {
		t.Errorf("managed should win over convention, got kind=%q", d.Kind)
	}
}

func TestDisableEnable(t *testing.T) {
	isolate(t)
	writeManaged(t, "hello", "schema: 1\nprotocol: \"1.0.0\"\ncommands:\n  - path: greet\n", "echo hi\n")

	if err := Disable("hello"); err != nil {
		t.Fatal(err)
	}
	d, _ := Find(Load(), "hello")
	if d.Enabled || !d.UserDisabled || !strings.Contains(d.Reason, "disabled by user") {
		t.Errorf("after Disable: enabled=%v userDisabled=%v reason=%q", d.Enabled, d.UserDisabled, d.Reason)
	}
	// A disabled group must not appear in the skip registry set.
	if names := GroupNames(Load()); len(names) != 0 {
		t.Errorf("disabled group should not be a registered group name: %v", names)
	}

	if err := Enable("hello"); err != nil {
		t.Fatal(err)
	}
	d, _ = Find(Load(), "hello")
	if !d.Enabled || d.UserDisabled {
		t.Errorf("after Enable: enabled=%v userDisabled=%v", d.Enabled, d.UserDisabled)
	}
	if names := GroupNames(Load()); len(names) != 1 || names[0] != "hello" {
		t.Errorf("enabled group should register its name, got %v", names)
	}
}

func TestCacheWrittenAndReused(t *testing.T) {
	isolate(t)
	writeManaged(t, "hello", "schema: 1\nprotocol: \"1.0.0\"\nversion: \"0.1.0\"\ncommands:\n  - path: greet\n", "echo hi\n")

	// First Load scans and writes the cache.
	d, _ := Find(Load(), "hello")
	if d.Manifest.Version != "0.1.0" {
		t.Fatalf("initial version = %q", d.Manifest.Version)
	}
	if _, err := os.Stat(cachePath()); err != nil {
		t.Fatalf("cache not written: %v", err)
	}

	// Rewrite the manifest *contents* only (bumping the version). Editing a file
	// in place doesn't change the plugins-directory mtime, so the cache stays
	// fresh and Load must serve the stale-but-cached version without rescanning.
	if err := os.WriteFile(filepath.Join(PluginsDir(), "hello", manifestFile),
		[]byte("schema: 1\nprotocol: \"1.0.0\"\nversion: \"9.9.9\"\ncommands:\n  - path: greet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, _ = Find(Load(), "hello")
	if d.Manifest.Version != "0.1.0" {
		t.Errorf("fresh cache should serve the cached version, got %q", d.Manifest.Version)
	}

	// An explicit Update forces a rescan and picks up the new contents.
	if _, err := Update(); err != nil {
		t.Fatal(err)
	}
	d, _ = Find(Load(), "hello")
	if d.Manifest.Version != "9.9.9" {
		t.Errorf("after Update, expected rescanned version 9.9.9, got %q", d.Manifest.Version)
	}
}

func TestUpdateForcesRescan(t *testing.T) {
	isolate(t)
	writeManaged(t, "hello", "schema: 1\nprotocol: \"1.0.0\"\ncommands:\n  - path: greet\n", "echo hi\n")
	_ = Load() // seed cache

	// Remove the plugin, then Update: a forced rescan must drop it.
	if err := os.RemoveAll(filepath.Join(PluginsDir(), "hello")); err != nil {
		t.Fatal(err)
	}
	ds, err := Update()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Find(ds, "hello"); ok {
		t.Error("Update should have rescanned and dropped the removed plugin")
	}
}

// Fork-bomb guard at the discovery layer: with N managed plugins present, Load
// (scan + cache + decorate) must not execute any of them. Each fixture writes a
// sentinel file when run; discovery reads only manifests, so no sentinel may
// appear. This is the Docker plugin-discovery lesson made a test.
func TestLoad_NeverExecutesPlugins(t *testing.T) {
	data := isolate(t)
	sentinel := filepath.Join(data, "ran")
	for _, name := range []string{"one", "two", "three"} {
		writeManaged(t, name,
			"schema: 1\nprotocol: \"1.0.0\"\ncommands:\n  - path: go\n    short: run\n",
			"echo ran >> "+sentinel+"\n")
	}

	ds := Load()
	if len(ds) != 3 {
		t.Fatalf("expected 3 plugins discovered, got %d", len(ds))
	}
	_ = GroupNames(ds) // building the skip set must not fork either
	if _, err := os.Stat(sentinel); err == nil {
		body, _ := os.ReadFile(sentinel)
		t.Fatalf("discovery executed a plugin (fork bomb): sentinel present:\n%s", body)
	}
}
