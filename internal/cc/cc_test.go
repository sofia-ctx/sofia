package cc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTranscript builds a synthetic .jsonl transcript from a list of
// entries (each a map) under projectsDir/<dirName>/<stem>.jsonl.
func writeTranscript(t *testing.T, projectsDir, dirName, stem string, entries []map[string]any) string {
	t.Helper()
	dir := filepath.Join(projectsDir, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	path := filepath.Join(dir, stem+".jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func sampleEntries() []map[string]any {
	bigResult := strings.Repeat("x", 7000) // ~1750 est tokens → "fat"
	return []map[string]any{
		// human prompt + session metadata
		{
			"type": "user", "cwd": "/tmp/foo/myproj", "gitBranch": "main",
			"version": "1.2.3", "sessionId": "11111111-2222-3333-4444-555555555555",
			"timestamp": "2026-05-23T10:00:00.000Z",
			"message":   map[string]any{"role": "user", "content": "do the thing"},
		},
		// assistant turn: usage + four tool_use blocks
		{
			"type": "assistant", "timestamp": "2026-05-23T10:01:00.000Z", "durationMs": 1500,
			"message": map[string]any{
				"role":  "assistant",
				"model": "claude-test",
				"usage": map[string]any{
					"input_tokens": 5, "output_tokens": 100,
					"cache_read_input_tokens": 200, "cache_creation_input_tokens": 50,
				},
				"content": []any{
					map[string]any{"type": "tool_use", "id": "u1", "name": "Bash", "input": map[string]any{"command": "grep -r foo ."}},
					map[string]any{"type": "tool_use", "id": "u2", "name": "Read", "input": map[string]any{"file_path": "/tmp/foo/myproj/a.go"}},
					map[string]any{"type": "tool_use", "id": "u3", "name": "Edit", "input": map[string]any{"file_path": "/tmp/foo/myproj/a.go"}},
					map[string]any{"type": "tool_use", "id": "u4", "name": "Write", "input": map[string]any{"file_path": "/tmp/foo/myproj/b.go"}},
				},
			},
		},
		// tool results (u2 is fat)
		{
			"type": "user", "timestamp": "2026-05-23T10:02:00.000Z",
			"message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "u1", "content": "line1\nline2"},
				map[string]any{"type": "tool_result", "tool_use_id": "u2", "content": bigResult},
			}},
		},
		// injected system reminder — must NOT count as a human prompt
		{
			"type": "user", "isMeta": true, "timestamp": "2026-05-23T10:03:00.000Z",
			"message": map[string]any{"role": "user", "content": "<system-reminder>ignore me</system-reminder>"},
		},
		// continuation summary — must NOT count as a human prompt
		{
			"type": "user", "timestamp": "2026-05-23T10:04:00.000Z",
			"message": map[string]any{"role": "user", "content": "This session is being continued from a previous conversation"},
		},
		// ai title + PR link
		{"type": "ai-title", "aiTitle": "My Title"},
		{"type": "pr-link", "prNumber": 7, "prUrl": "https://example/pr/7", "timestamp": "2026-05-23T10:05:00.000Z"},
	}
}

func TestParse(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "-tmp-foo-myproj", "11111111-2222-3333-4444-555555555555", sampleEntries())

	s, err := Parse(path, true)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if s.ID != "11111111" {
		t.Errorf("ID = %q, want 11111111", s.ID)
	}
	if s.Project != "myproj" {
		t.Errorf("Project = %q, want myproj", s.Project)
	}
	if s.Branch != "main" || s.Version != "1.2.3" || s.Model != "claude-test" {
		t.Errorf("meta = branch %q version %q model %q", s.Branch, s.Version, s.Model)
	}
	if s.Title != "My Title" {
		t.Errorf("Title = %q, want My Title", s.Title)
	}

	// usage totals
	if s.OutputTokens != 100 || s.InputTokens != 5 || s.CacheReadTokens != 200 || s.CacheCreateTokens != 50 {
		t.Errorf("usage = out %d in %d cr %d cc %d", s.OutputTokens, s.InputTokens, s.CacheReadTokens, s.CacheCreateTokens)
	}
	if s.DurationMs != 1500 {
		t.Errorf("DurationMs = %d, want 1500", s.DurationMs)
	}

	// tool histogram
	for tool, want := range map[string]int{"Bash": 1, "Read": 1, "Edit": 1, "Write": 1} {
		if s.ToolCalls[tool] != want {
			t.Errorf("ToolCalls[%s] = %d, want %d", tool, s.ToolCalls[tool], want)
		}
	}

	// prompts: only the genuine human turn
	if len(s.UserPrompts) != 1 || s.UserPrompts[0] != "do the thing" {
		t.Errorf("UserPrompts = %v, want [\"do the thing\"]", s.UserPrompts)
	}

	// bash captured + categorised
	if len(s.Bash) != 1 || Categorize(s.Bash[0]) != CatSearch {
		t.Errorf("Bash = %v (cat %v)", s.Bash, s.BashCategories())
	}

	// fat results: exactly the big u2 Read
	if len(s.FatResults) != 1 {
		t.Fatalf("FatResults = %d, want 1 (%v)", len(s.FatResults), s.FatResults)
	}
	if s.FatResults[0].Tool != "Read" || !strings.HasSuffix(s.FatResults[0].Brief, "a.go") {
		t.Errorf("FatResults[0] = %+v, want Read .../a.go", s.FatResults[0])
	}

	// file touches
	files := map[string]FileTouch{}
	for _, f := range s.Files {
		files[f.Path] = f
	}
	if a := files["/tmp/foo/myproj/a.go"]; a.Reads != 1 || a.Edits != 1 {
		t.Errorf("a.go touch = %+v, want reads 1 edits 1", a)
	}
	if b := files["/tmp/foo/myproj/b.go"]; b.Writes != 1 {
		t.Errorf("b.go touch = %+v, want writes 1", b)
	}

	// PRs
	if len(s.PRs) != 1 || s.PRs[0].Number != 7 {
		t.Errorf("PRs = %v, want [#7]", s.PRs)
	}

	// span
	if got := s.Span(); got != 5*time.Minute {
		t.Errorf("Span = %v, want 5m", got)
	}
}

func TestParseLiteSkipsDetail(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "-tmp-foo-myproj", "aaaaaaaa-0000", sampleEntries())
	s, err := Parse(path, false)
	if err != nil {
		t.Fatal(err)
	}
	// aggregates still populated...
	if s.OutputTokens != 100 || s.ToolCalls["Bash"] != 1 {
		t.Errorf("lite parse lost aggregates: out %d bash %d", s.OutputTokens, s.ToolCalls["Bash"])
	}
	// ...but heavy detail is skipped
	if len(s.Bash) != 0 || len(s.Files) != 0 || len(s.FatResults) != 0 {
		t.Errorf("lite parse should skip detail: bash %d files %d fat %d", len(s.Bash), len(s.Files), len(s.FatResults))
	}
}

func TestResolveSelector(t *testing.T) {
	dir := t.TempDir()
	older := writeTranscript(t, dir, "-tmp-foo-myproj", "11111111-aaaa", sampleEntries())
	newer := writeTranscript(t, dir, "-tmp-bar-other", "22222222-bbbb", sampleEntries())

	// Make `older` genuinely older so `last` is deterministic.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(older, old, old); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		sel  string
		want string
	}{
		{"", newer},
		{"last", newer},
		{"11111111", older}, // uuid prefix
		{"22222222", newer}, // uuid prefix
		{"myproj", older},   // project dir substring
		{"other", newer},    // project dir substring
		{newer, newer},      // explicit path
	}
	for _, c := range cases {
		got, err := ResolveSelector(dir, c.sel)
		if err != nil {
			t.Errorf("ResolveSelector(%q): %v", c.sel, err)
			continue
		}
		if got != c.want {
			t.Errorf("ResolveSelector(%q) = %s, want %s", c.sel, got, c.want)
		}
	}

	if _, err := ResolveSelector(dir, "nope-no-match"); err == nil {
		t.Error("expected error for unmatched selector")
	}
}

func TestProjectFromDir(t *testing.T) {
	cases := map[string]string{
		"-home-user-www-myapp":          "myapp",
		"-home-user-www-myorg-otherapp": "otherapp",
		"-tmp-foo-myproj":               "myproj",
		"plain":                         "plain",
	}
	for in, want := range cases {
		if got := projectFromDir(in); got != want {
			t.Errorf("projectFromDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsHumanText(t *testing.T) {
	yes := []string{"do the thing", "fix bug X", "https://example.com please review"}
	no := []string{"", "   ", "<system-reminder>x</system-reminder>", "Caveat: ...", "This session is being continued from ..."}
	for _, s := range yes {
		if !isHumanText(s) {
			t.Errorf("isHumanText(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isHumanText(s) {
			t.Errorf("isHumanText(%q) = true, want false", s)
		}
	}
}
