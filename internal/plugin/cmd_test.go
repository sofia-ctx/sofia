package plugin

import (
	"bytes"
	"encoding/json"
	"os"
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
