package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installFixture copies internal/plugin/testdata/plugins/<name> into the managed
// plugins dir (under the test's temp $XDG_DATA_HOME), preserving the exec bit.
func installFixture(t *testing.T, name string) {
	t.Helper()
	src := filepath.Join("testdata", "plugins", name)
	dst := filepath.Join(PluginsDir(), name)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, mode); err != nil {
			t.Fatal(err)
		}
	}
}

// logLines reads and decodes every calls.jsonl entry written under dir.
func logLines(t *testing.T, dir string) []calllogEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []calllogEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e calllogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad calls.jsonl line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

// calllogEntry mirrors the fields of calllog.Entry the tests assert on.
type calllogEntry struct {
	Tool       string `json:"tool"`
	Args       []string
	ExitCode   int    `json:"exit"`
	Error      string `json:"err"`
	OutputByte int64  `json:"out_bytes"`
}

func TestInvoke_GreetStdoutEnvAndOneLogLine(t *testing.T) {
	isolate(t)
	logDir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", logDir)
	t.Setenv("SOFIA_FORMAT", "md")
	t.Setenv("SOFIA_TAG", "myproj")
	installFixture(t, "hello")

	d, ok := Find(Load(), "hello")
	if !ok {
		t.Fatal("fixture hello not discovered")
	}

	var out bytes.Buffer
	err := Invoke(context.Background(), InvokeRequest{
		Descriptor: d,
		Command:    &Command{Path: "greet"},
		Args:       []string{"there"},
		Stdout:     &out,
		Stderr:     &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Invoke greet: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "greeting: Hello there") {
		t.Errorf("stdout missing greeting (settings default + argv):\n%s", got)
	}
	if !strings.Contains(got, "format=md") || !strings.Contains(got, "tag=myproj") {
		t.Errorf("plugin did not receive the SOFIA_* env:\n%s", got)
	}
	if !strings.Contains(got, "plugin=1") {
		t.Errorf("plugin did not receive SOFIA_PLUGIN=1:\n%s", got)
	}

	lines := logLines(t, logDir)
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 call-log line, got %d: %+v", len(lines), lines)
	}
	e := lines[0]
	if e.Tool != "hello.greet" {
		t.Errorf("tool name = %q, want hello.greet", e.Tool)
	}
	if e.ExitCode != 0 || e.Error != "" {
		t.Errorf("want clean exit, got exit=%d err=%q", e.ExitCode, e.Error)
	}
	if e.OutputByte <= 0 {
		t.Errorf("output bytes not metered: %d", e.OutputByte)
	}
}

// The crash / non-zero-exit case: still exactly one log line, carrying the real
// exit code and an error, even though the plugin printed nothing to stdout.
func TestInvoke_CrashStillOneLogLineWithRealExit(t *testing.T) {
	isolate(t)
	logDir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", logDir)
	installFixture(t, "hello")

	d, _ := Find(Load(), "hello")
	err := Invoke(context.Background(), InvokeRequest{
		Descriptor: d,
		Command:    &Command{Path: "boom"},
		Stdout:     &bytes.Buffer{},
		Stderr:     &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "exited with code 3") {
		t.Fatalf("want an exit-3 error, got %v", err)
	}

	lines := logLines(t, logDir)
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 call-log line on crash, got %d: %+v", len(lines), lines)
	}
	e := lines[0]
	if e.Tool != "hello.boom" {
		t.Errorf("tool = %q, want hello.boom", e.Tool)
	}
	if e.ExitCode != 3 {
		t.Errorf("exit = %d, want the real code 3", e.ExitCode)
	}
	if e.Error == "" {
		t.Error("error text not recorded for the crash")
	}
}

func TestCommandArgvAndToolName(t *testing.T) {
	// Multi-segment path splits on spaces and prepends before user args.
	argv := commandArgv(&Command{Path: "cache clear"}, []string{"--all"})
	if strings.Join(argv, " ") != "cache clear --all" {
		t.Errorf("argv = %v", argv)
	}
	if got := toolName(Descriptor{Name: "x"}, &Command{Path: "cache/clear"}); got != "x.cache.clear" {
		t.Errorf("toolName = %q", got)
	}
	if got := toolName(Descriptor{Name: "x"}, nil); got != "x" {
		t.Errorf("passthrough toolName = %q", got)
	}
}
