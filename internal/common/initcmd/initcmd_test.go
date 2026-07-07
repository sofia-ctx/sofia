package initcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sofia-ctx/sofia"
)

// isolate points HOME/CLAUDE_DIR/XDG_DATA_HOME/SOFIA_LOG_DIR at fresh temp
// dirs so a test never touches the real machine, and pre-creates CLAUDE_DIR
// (a directory that exists is the "Claude Code on this machine" signal).
// Returns the CLAUDE_DIR path.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_DIR", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	return dir
}

func TestAgentsBlockCreate(t *testing.T) {
	isolate(t)
	project := t.TempDir()

	item := agentsMDStep(project)
	if item.Status != statusWritten || item.Detail != "created AGENTS.md" {
		t.Errorf("item = %+v", item)
	}
	got, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != agentsBlock {
		t.Errorf("AGENTS.md = %q, want exactly the block", got)
	}
}

func TestAgentsBlockAppend(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	existing := "# My Project\n\nSome instructions.\n"
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	item := agentsMDStep(project)
	if item.Status != statusWritten || item.Detail != "appended managed block" {
		t.Errorf("item = %+v", item)
	}
	got, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), existing) {
		t.Errorf("existing content not preserved above the block:\n%s", got)
	}
	if want := existing + "\n" + agentsBlock; string(got) != want {
		t.Errorf("AGENTS.md = %q, want %q", got, want)
	}
}

func TestAgentsBlockReplace(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	before := "# Before\n\n"
	after := "\n\n# After\n"
	stale := "<!-- sf:begin -->\nSTALE CONTENT\n<!-- sf:end -->"
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte(before+stale+after), 0o644); err != nil {
		t.Fatal(err)
	}

	item := agentsMDStep(project)
	if item.Status != statusWritten || item.Detail != "replaced managed block" {
		t.Errorf("item = %+v", item)
	}
	got, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), before) || !strings.HasSuffix(string(got), after) {
		t.Errorf("text outside markers was touched:\n%s", got)
	}
	if strings.Contains(string(got), "STALE CONTENT") {
		t.Errorf("stale content survived the replace:\n%s", got)
	}
	want := before + strings.TrimRight(agentsBlock, "\n") + after
	if string(got) != want {
		t.Errorf("AGENTS.md = %q, want %q", got, want)
	}
}

func TestAgentsBlockIdempotent(t *testing.T) {
	isolate(t)
	project := t.TempDir()

	agentsMDStep(project)
	first, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}

	item := agentsMDStep(project)
	if item.Status != statusOK {
		t.Errorf("second run status = %q, want %q", item.Status, statusOK)
	}
	second, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("second run changed the file:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestCorporateOnlyTouchesProject(t *testing.T) {
	claudeDir := isolate(t)
	project := t.TempDir()

	var buf bytes.Buffer
	if err := Run(Options{Project: project, Corporate: true, Format: "toon"}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md not written: %v", err)
	}
	entries, err := os.ReadDir(claudeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("CLAUDE_DIR is not empty after --corporate: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(project, ".mcp.json")); err == nil {
		t.Error(".mcp.json was written despite --corporate")
	}
}

func TestSkillInstallFreshStaleForce(t *testing.T) {
	dir := isolate(t)
	dest := filepath.Join(dir, "skills", "sf-context", "SKILL.md")

	item := skillStep(false)
	if item.Status != statusWritten {
		t.Fatalf("fresh install: item = %+v", item)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, sofia.SkillMD) {
		t.Fatalf("installed skill != bundled copy (err=%v)", err)
	}

	item = skillStep(false)
	if item.Status != statusOK {
		t.Errorf("identical: item = %+v", item)
	}

	if err := os.WriteFile(dest, []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	item = skillStep(false)
	if item.Status != statusSkipped {
		t.Errorf("hand-edited without --force: item = %+v", item)
	}
	if got, _ := os.ReadFile(dest); string(got) != "hand-edited\n" {
		t.Error("hand-edited skill was overwritten without --force")
	}

	item = skillStep(true)
	if item.Status != statusWritten {
		t.Errorf("hand-edited with --force: item = %+v", item)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, sofia.SkillMD) {
		t.Error("--force did not restore the bundled copy")
	}
}

func TestHookMergePreservesSettings(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "settings.json")
	original := `{"unrelatedKey":"keepme","hooks":{"PreToolUse":[{"matcher":"Foo","hooks":[{"type":"command","command":"other-hook"}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	item := hookStep()
	if item.Status != statusWritten {
		t.Fatalf("item = %+v", item)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	if doc["unrelatedKey"] != "keepme" {
		t.Errorf("unrelated key lost: %v", doc)
	}
	pre := doc["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse len = %d, want 2: %v", len(pre), pre)
	}
	if !bytes.Contains(got, []byte("other-hook")) {
		t.Error("original PreToolUse entry lost")
	}
	if !bytes.Contains(got, []byte("sf hook pre")) {
		t.Error("sf entry not appended")
	}

	backup, err := os.ReadFile(path + ".sf-bak")
	if err != nil {
		t.Fatalf("no backup written: %v", err)
	}
	if string(backup) != original {
		t.Errorf("backup = %q, want original bytes %q", backup, original)
	}
}

func TestHookAlreadyWired(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "settings.json")
	original := `{"hooks":{"PreToolUse":[{"matcher":"Read|Bash","hooks":[{"type":"command","command":"sf hook pre","timeout":10}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	item := hookStep()
	if item.Status != statusOK {
		t.Errorf("item = %+v", item)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("settings.json bytes changed:\n%s", got)
	}
	if _, err := os.Stat(path + ".sf-bak"); err == nil {
		t.Error("a backup was written even though nothing changed")
	}
}

func TestMcpMergePreservesServers(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	path := filepath.Join(project, ".mcp.json")
	original := `{"mcpServers":{"other":{"command":"foo"}}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	item := mcpStep(project)
	if item.Status != statusWritten {
		t.Fatalf("item = %+v", item)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Errorf("existing server lost: %v", servers)
	}
	if _, ok := servers["sofia"]; !ok {
		t.Errorf("sofia server not registered: %v", servers)
	}
}

func TestMcpAlreadyRegistered(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	path := filepath.Join(project, ".mcp.json")
	original := `{"mcpServers":{"sofia":{"command":"sf","args":["mcp"]}}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	item := mcpStep(project)
	if item.Status != statusOK {
		t.Errorf("item = %+v", item)
	}
}

func TestDetectionGates(t *testing.T) {
	isolate(t)
	t.Setenv("CLAUDE_DIR", filepath.Join(t.TempDir(), "nonexistent"))
	project := t.TempDir()

	res, err := execute(Options{Project: project})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.ClaudeOnMachine {
		t.Error("ClaudeOnMachine should be false when CLAUDE_DIR doesn't exist")
	}
	if res.ClaudeInProject {
		t.Error("ClaudeInProject should be false for a bare project dir")
	}

	byName := map[string]Item{}
	for _, it := range res.Items {
		byName[it.Name] = it
	}
	if byName["agents-md"].Status != statusWritten {
		t.Errorf("agents-md = %+v, want written", byName["agents-md"])
	}
	for _, name := range []string{"skill", "hook", "mcp"} {
		if byName[name].Status != statusSkipped {
			t.Errorf("%s = %+v, want skipped", name, byName[name])
		}
	}
}

func TestRenderFormats(t *testing.T) {
	res := &Result{
		ClaudeOnMachine: true,
		ClaudeInProject: false,
		Items:           []Item{{Name: "agents-md", Status: statusWritten, Detail: "created AGENTS.md"}},
	}
	tests := []struct{ format, want string }{
		{"", "items[1]"},
		{"toon", "items[1]"},
		{"md", "| Item | Status | Detail |"},
		{"json", `"claude_on_machine": true`},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		if err := render(&buf, tt.format, res); err != nil {
			t.Fatalf("%s: %v", tt.format, err)
		}
		if !strings.Contains(buf.String(), tt.want) {
			t.Errorf("%s: want %q in:\n%s", tt.format, tt.want, buf.String())
		}
	}
	var buf bytes.Buffer
	if err := render(&buf, "bogus", res); err == nil {
		t.Error("bogus format should error")
	}
}
