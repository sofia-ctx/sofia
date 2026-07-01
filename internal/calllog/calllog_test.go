package calllog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathPriority(t *testing.T) {
	t.Run("SOFIA_LOG_DIR wins", func(t *testing.T) {
		t.Setenv("SOFIA_LOG_DIR", "/explicit")
		t.Setenv("XDG_STATE_HOME", "/xdg")
		if got := Path(); got != "/explicit/calls.jsonl" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("XDG_STATE_HOME used when SOFIA_LOG_DIR empty", func(t *testing.T) {
		t.Setenv("SOFIA_LOG_DIR", "")
		t.Setenv("XDG_STATE_HOME", "/xdg")
		if got := Path(); got != "/xdg/sofia/calls.jsonl" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("falls back to ~/.local/state/sofia", func(t *testing.T) {
		t.Setenv("SOFIA_LOG_DIR", "")
		t.Setenv("XDG_STATE_HOME", "")
		got := Path()
		if !strings.HasSuffix(got, "/.local/state/sofia/calls.jsonl") {
			t.Errorf("got %q, expected ~/.local/state/sofia/calls.jsonl tail", got)
		}
	})
}

func TestFingerprintStableAcrossOrder(t *testing.T) {
	a := Fingerprint([]string{"foo", "bar", "baz"})
	b := Fingerprint([]string{"baz", "foo", "bar"})
	if a != b {
		t.Errorf("fingerprint not order-independent: %q vs %q", a, b)
	}
}

func TestFingerprintDifferentArgs(t *testing.T) {
	if Fingerprint([]string{"foo"}) == Fingerprint([]string{"bar"}) {
		t.Error("different args produced same fingerprint")
	}
}

func TestFingerprintLength(t *testing.T) {
	fp := Fingerprint([]string{"x"})
	if len(fp) != 16 {
		t.Errorf("fingerprint length = %d, want 16", len(fp))
	}
}

func TestCounter(t *testing.T) {
	var buf bytes.Buffer
	c := &Counter{W: &buf}
	n, err := c.Write([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 || c.Count != 5 {
		t.Errorf("Write returned n=%d, Count=%d", n, c.Count)
	}
	_, _ = c.Write([]byte(" world"))
	if c.Count != 11 {
		t.Errorf("Count = %d, want 11", c.Count)
	}
	if buf.String() != "hello world" {
		t.Errorf("inner writer got %q", buf.String())
	}
	// 11 ASCII bytes → ~3 tokens via the heuristic (11/4 = 2.75 → 3).
	if c.Tokens < 2 || c.Tokens > 4 {
		t.Errorf("Tokens = %d, want ~3 (2-4 acceptable)", c.Tokens)
	}
}

func TestRecordOutput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", dir)

	tr := Start("test.tool", nil)
	tr.RecordOutput(&Counter{Count: 123, Tokens: 45})
	tr.Finish(nil)

	data, _ := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
	var e Entry
	_ = json.Unmarshal(bytes.TrimSpace(data), &e)
	if e.OutputBytes != 123 || e.OutputTokens != 45 {
		t.Errorf("recorded bytes=%d tokens=%d; want 123, 45", e.OutputBytes, e.OutputTokens)
	}
}

func TestTrackerLifecycle(t *testing.T) {
	// Redirect log to a temp dir via SOFIA_LOG_DIR.
	dir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", dir)

	tr := Start("test.tool", []string{"--flag", "arg"})
	tr.SetSummary(map[string]any{"count": 3})
	tr.SetOutputBytes(42)
	tr.Finish(nil)

	data, err := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	var e Entry
	if err := json.Unmarshal(bytes.TrimSpace(data), &e); err != nil {
		t.Fatal(err)
	}
	if e.Tool != "test.tool" {
		t.Errorf("tool=%q", e.Tool)
	}
	if e.OutputBytes != 42 {
		t.Errorf("OutputBytes=%d", e.OutputBytes)
	}
	if e.Fingerprint == "" {
		t.Error("Fingerprint not populated")
	}
	if e.ExitCode != 0 || e.Error != "" {
		t.Errorf("expected success, got exit=%d err=%q", e.ExitCode, e.Error)
	}
}

func TestTrackerError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", dir)

	tr := Start("test.tool", nil)
	tr.Finish(testErr("boom"))

	data, _ := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
	var e Entry
	_ = json.Unmarshal(bytes.TrimSpace(data), &e)
	if e.ExitCode != 1 || e.Error != "boom" {
		t.Errorf("expected exit=1 err=boom, got exit=%d err=%q", e.ExitCode, e.Error)
	}
}

func TestDetectSource(t *testing.T) {
	// Note: this runs in a *.test binary, so underTest() is true and short-
	// circuits to "test" before the CLAUDECODE/terminal branches. Only the
	// SOFIA_SOURCE override (checked first) and the test-binary classification
	// are deterministic here; the CLAUDECODE=agent path is exercised in
	// production and asserted via the env-var contract in TestSessionID-style
	// helpers.
	t.Run("SOFIA_SOURCE override wins", func(t *testing.T) {
		t.Setenv("SOFIA_SOURCE", "ci")
		t.Setenv("CLAUDECODE", "1")
		if got := detectSource(); got != "ci" {
			t.Errorf("detectSource() = %q, want ci", got)
		}
	})
	t.Run("test binary → test when no override", func(t *testing.T) {
		t.Setenv("SOFIA_SOURCE", "")
		if got := detectSource(); got != "test" {
			t.Errorf("detectSource() = %q, want test", got)
		}
	})
}

func TestSessionID(t *testing.T) {
	t.Run("CLAUDE_CODE_SESSION_ID wins", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "abc123")
		t.Setenv("SOFIA_SESSION_ID", "fallback")
		if got := sessionID(); got != "abc123" {
			t.Errorf("sessionID() = %q, want abc123", got)
		}
	})
	t.Run("SOFIA_SESSION_ID fallback", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "")
		t.Setenv("SOFIA_SESSION_ID", "fallback")
		if got := sessionID(); got != "fallback" {
			t.Errorf("sessionID() = %q, want fallback", got)
		}
	})
	t.Run("empty when neither set", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "")
		t.Setenv("SOFIA_SESSION_ID", "")
		if got := sessionID(); got != "" {
			t.Errorf("sessionID() = %q, want empty", got)
		}
	})
}

func TestProjectTag(t *testing.T) {
	t.Run("SOFIA_TAG wins over cwd", func(t *testing.T) {
		t.Setenv("SOFIA_TAG", "myapp")
		if got := projectTag(); got != "myapp" {
			t.Errorf("projectTag() = %q, want myapp", got)
		}
	})
	t.Run("derives repo-root basename from .git", func(t *testing.T) {
		t.Setenv("SOFIA_TAG", "")
		root := t.TempDir()
		sub := filepath.Join(root, "internal", "deep")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		cwd, _ := os.Getwd()
		defer func() { _ = os.Chdir(cwd) }()
		if err := os.Chdir(sub); err != nil {
			t.Fatal(err)
		}
		// macOS tmp dirs are symlinked (/var → /private/var); compare basenames.
		if got := deriveProjectFromCwd(); got != filepath.Base(root) {
			t.Errorf("deriveProjectFromCwd() = %q, want %q", got, filepath.Base(root))
		}
	})
}

func TestStartStampsSessionAndTag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-xyz")
	t.Setenv("SOFIA_TAG", "myproj")

	Start("test.tool", nil).Finish(nil)

	data, _ := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
	var e Entry
	if err := json.Unmarshal(bytes.TrimSpace(data), &e); err != nil {
		t.Fatal(err)
	}
	if e.SessionID != "sess-xyz" {
		t.Errorf("SessionID = %q, want sess-xyz", e.SessionID)
	}
	if e.Tag != "myproj" {
		t.Errorf("Tag = %q, want myproj", e.Tag)
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }
