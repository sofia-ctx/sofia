package gripe

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sofia-ctx/sofia/internal/calllog"
)

func TestRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", dir)

	var buf bytes.Buffer
	if err := Run(Options{Message: "  sf code .kt does not structure it  "}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "recorded") {
		t.Errorf("no confirmation line: %q", buf.String())
	}

	entries, err := calllog.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Tool != "gripe" {
		t.Errorf("tool = %q, want gripe", e.Tool)
	}
	if got := note(e); got != "sf code .kt does not structure it" {
		t.Errorf("note = %q (trim/summary wrong)", got)
	}
}

func TestListNewestFirstAndFilter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", dir)
	writeLog(t, dir,
		calllog.Entry{Tool: "code", Timestamp: "2026-06-01T10:00:00Z"}, // non-gripe → ignored
		entryGripe("2026-06-02T10:00:00Z", "myapp", "agent", "first"),
		entryGripe("2026-06-03T10:00:00Z", "sofia", "agent", "second"),
	)

	var buf bytes.Buffer
	if err := Run(Options{Limit: 20}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "gripes[2]") {
		t.Errorf("want gripes[2] header, got:\n%s", out)
	}
	if strings.Contains(out, "code") {
		t.Errorf("non-gripe leaked into list:\n%s", out)
	}
	iSecond, iFirst := strings.Index(out, "second"), strings.Index(out, "first")
	if iSecond < 0 || iFirst < 0 || iSecond > iFirst {
		t.Errorf("expected newest-first (second before first); got:\n%s", out)
	}
}

func TestListLimitTruncates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", dir)
	writeLog(t, dir,
		entryGripe("2026-06-01T10:00:00Z", "myapp", "agent", "g1"),
		entryGripe("2026-06-02T10:00:00Z", "myapp", "agent", "g2"),
		entryGripe("2026-06-03T10:00:00Z", "myapp", "agent", "g3"),
	)

	var buf bytes.Buffer
	if err := Run(Options{Limit: 2}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "g3") || !strings.Contains(out, "g2") {
		t.Errorf("want newest two; got:\n%s", out)
	}
	if strings.Contains(out, "g1") {
		t.Errorf("oldest should be truncated:\n%s", out)
	}
	if !strings.Contains(out, "# +1 older not shown") {
		t.Errorf("want truncation note; got:\n%s", out)
	}
}

func TestNoteFallbackToArgs(t *testing.T) {
	e := calllog.Entry{Tool: "gripe", Args: []string{"raw", "msg"}}
	if got := note(e); got != "raw msg" {
		t.Errorf("note fallback = %q, want %q", got, "raw msg")
	}
}

func TestRenderFormats(t *testing.T) {
	gv := gripeView{Gripes: []Gripe{{When: "2026-06-03 10:00", Project: "myapp", Source: "agent", Note: "boom"}}, Total: 1}
	tests := []struct{ format, want string }{
		{"", "gripes[1]"},
		{"toon", "gripes[1]"},
		{"md", "# Gripes about sf"},
		{"json", `"note": "boom"`},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		if err := render(&buf, tt.format, gv); err != nil {
			t.Fatalf("%s: %v", tt.format, err)
		}
		if !strings.Contains(buf.String(), tt.want) {
			t.Errorf("%s: want %q in:\n%s", tt.format, tt.want, buf.String())
		}
	}
	var buf bytes.Buffer
	if err := render(&buf, "bogus", gv); err == nil {
		t.Error("bogus format should error")
	}
}

func entryGripe(ts, tag, source, n string) calllog.Entry {
	return calllog.Entry{
		Tool:      "gripe",
		Timestamp: ts,
		Tag:       tag,
		Source:    source,
		Args:      []string{n},
		Summary:   map[string]any{"note": n},
	}
}

func writeLog(t *testing.T, dir string, entries ...calllog.Entry) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
}
