package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readPayload(path string, offset, limit int) Input {
	ti, _ := json.Marshal(map[string]any{"file_path": path, "offset": offset, "limit": limit})
	return Input{SessionID: "sid-1", ToolName: "Read", ToolInput: ti}
}

func bashPayload(command, cwd string) Input {
	ti, _ := json.Marshal(map[string]string{"command": command})
	return Input{SessionID: "sid-1", CWD: cwd, ToolName: "Bash", ToolInput: ti}
}

func TestDecide(t *testing.T) {
	dir := t.TempDir()
	bigGo := writeFile(t, dir, "big.go", 9000)
	smallGo := writeFile(t, dir, "small.go", 500)
	bigMD := writeFile(t, dir, "big.md", 9000)
	bigPHP := writeFile(t, dir, "big.php", 9000)

	cases := []struct {
		name   string
		in     Input
		mode   string
		action string
	}{
		{"big go full read → deny", readPayload(bigGo, 0, 0), "nudge", ActionDeny},
		{"targeted read passes", readPayload(bigGo, 100, 50), "nudge", ""},
		{"small file passes", readPayload(smallGo, 0, 0), "nudge", ""},
		{"non-code ext passes", readPayload(bigMD, 0, 0), "nudge", ""},
		{"missing file passes", readPayload(filepath.Join(dir, "nope.go"), 0, 0), "nudge", ""},
		{"mode off passes", readPayload(bigGo, 0, 0), "off", ""},
		{"mode suggest allows with note", readPayload(bigGo, 0, 0), "suggest", ActionSuggest},
		{"mode strict denies", readPayload(bigGo, 0, 0), "strict", ActionDeny},
		{"bare cat big php → deny", bashPayload("cat "+bigPHP, dir), "nudge", ActionDeny},
		{"piped cat passes", bashPayload("cat "+bigPHP+" | head -50", dir), "nudge", ""},
		{"relative cat resolves via cwd", bashPayload("cat big.php", dir), "nudge", ActionDeny},
		{"non-cat bash passes", bashPayload("git status", dir), "nudge", ""},
		{"other tool passes", Input{ToolName: "Edit", ToolInput: json.RawMessage(`{}`)}, "nudge", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := NewState(t.TempDir())
			d := Decide(tc.in, st, tc.mode, defaultMinBytes)
			if d.Action != tc.action {
				t.Fatalf("action = %q, want %q (reason %q)", d.Action, tc.action, d.Reason)
			}
			if tc.action != "" && !strings.Contains(d.Reason, "sf code") {
				t.Fatalf("reason must advertise sf code, got %q", d.Reason)
			}
		})
	}
}

func TestNudgeOncePerSession(t *testing.T) {
	dir := t.TempDir()
	bigGo := writeFile(t, dir, "big.go", 9000)
	st := NewState(t.TempDir())

	first := Decide(readPayload(bigGo, 0, 0), st, "nudge", defaultMinBytes)
	if first.Action != ActionDeny {
		t.Fatalf("first read: action = %q, want deny", first.Action)
	}
	second := Decide(readPayload(bigGo, 0, 0), st, "nudge", defaultMinBytes)
	if second.Action != "" {
		t.Fatalf("second read must pass, got %q", second.Action)
	}
	// A different session is nudged independently.
	other := readPayload(bigGo, 0, 0)
	other.SessionID = "sid-2"
	if d := Decide(other, st, "nudge", defaultMinBytes); d.Action != ActionDeny {
		t.Fatalf("other session: action = %q, want deny", d.Action)
	}
	// strict ignores the seen-state.
	if d := Decide(readPayload(bigGo, 0, 0), st, "strict", defaultMinBytes); d.Action != ActionDeny {
		t.Fatalf("strict repeat: action = %q, want deny", d.Action)
	}
}

func TestParseCat(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"cat /tmp/a.go", "/tmp/a.go"},
		{"cat 'a.go'", "/cwd/a.go"},
		{"/bin/cat /tmp/a.go", "/tmp/a.go"},
		{"cat -n /tmp/a.go", ""},
		{"cat a.go b.go", ""},
		{"cat a.go | head", ""},
		{"cat a.go > out", ""},
		{"cat *.go", ""},
		{"echo cat", ""},
		{"category /tmp/a.go", ""},
		{"cd /x && cat a.go", ""},
	}
	for _, tc := range cases {
		if got := parseCat(tc.cmd, "/cwd"); got != tc.want {
			t.Errorf("parseCat(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}
