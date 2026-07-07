package initcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// codexIsolate points CODEX_HOME at a fresh, pre-created temp dir — the
// "Codex on this machine" signal, mirroring isolate's CLAUDE_DIR handling.
// Call after isolate(t). Returns the CODEX_HOME path.
func codexIsolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	return dir
}

// itemsByName indexes a report's items by name for lookups in assertions.
func itemsByName(items []Item) map[string]Item {
	m := make(map[string]Item, len(items))
	for _, it := range items {
		m[it.Name] = it
	}
	return m
}

func TestAgentsBlockCreate(t *testing.T) {
	isolate(t)
	project := t.TempDir()

	item := agentsMDStep(project, false)
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

	item := agentsMDStep(project, false)
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

	item := agentsMDStep(project, false)
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

	agentsMDStep(project, false)
	first, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}

	item := agentsMDStep(project, false)
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

	item := skillStep(false, false)
	if item.Status != statusWritten {
		t.Fatalf("fresh install: item = %+v", item)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, sofia.SkillMD) {
		t.Fatalf("installed skill != bundled copy (err=%v)", err)
	}

	item = skillStep(false, false)
	if item.Status != statusOK {
		t.Errorf("identical: item = %+v", item)
	}

	if err := os.WriteFile(dest, []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	item = skillStep(false, false)
	if item.Status != statusSkipped {
		t.Errorf("hand-edited without --force: item = %+v", item)
	}
	if got, _ := os.ReadFile(dest); string(got) != "hand-edited\n" {
		t.Error("hand-edited skill was overwritten without --force")
	}

	item = skillStep(true, false)
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

	item := hookStep(false)
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

	item := hookStep(false)
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

// TestHookNullSettingsSkipped guards against a nil-map panic: valid JSON
// ("null") that isn't an object must be treated as an unrecognized shape,
// not decoded into a nil map that a later assignment then panics on.
func TestHookNullSettingsSkipped(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}

	item := hookStep(false)
	if item.Status != statusSkipped {
		t.Errorf("item = %+v, want skipped", item)
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

	item := mcpStep(project, false)
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

	item := mcpStep(project, false)
	if item.Status != statusOK {
		t.Errorf("item = %+v", item)
	}
}

// TestMcpNullFileSkipped mirrors TestHookNullSettingsSkipped for .mcp.json.
func TestMcpNullFileSkipped(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	path := filepath.Join(project, ".mcp.json")
	if err := os.WriteFile(path, []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}

	item := mcpStep(project, false)
	if item.Status != statusSkipped {
		t.Errorf("item = %+v, want skipped", item)
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

// TestCodexGate mirrors TestDetectionGates for Codex: no CODEX_HOME dir
// means the three codex-* steps report skipped, and Claude's own items
// (which isolate(t) leaves detected) are unaffected.
func TestCodexGate(t *testing.T) {
	isolate(t)
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "nonexistent"))
	project := t.TempDir()

	res, err := execute(Options{Project: project})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.CodexOnMachine {
		t.Error("CodexOnMachine should be false when CODEX_HOME doesn't exist")
	}

	byName := itemsByName(res.Items)
	for _, name := range []string{"codex-hook", "codex-mcp", "codex-skill"} {
		if got := byName[name]; got.Status != statusSkipped || got.Detail != "Codex not detected" {
			t.Errorf("%s = %+v, want skipped/\"Codex not detected\"", name, got)
		}
	}
	for _, name := range []string{"skill", "hook", "mcp"} {
		if byName[name].Status == statusSkipped {
			t.Errorf("%s = %+v, should not be gated by Codex's absence", name, byName[name])
		}
	}
}

// TestCodexConfigAppendPreservesContent covers the append path against a
// realistic fixture: comments and an existing table survive verbatim, both
// managed blocks land at EOF, and the pre-run bytes are backed up once.
func TestCodexConfigAppendPreservesContent(t *testing.T) {
	isolate(t)
	codexHome := codexIsolate(t)
	path := filepath.Join(codexHome, "config.toml")
	original := "# my codex config\nmodel = \"gpt-5\"\n\n[profiles.x]\nmodel = \"gpt-5-mini\"\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()

	res, err := execute(Options{Project: project})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), original) {
		t.Errorf("original config.toml not preserved as a verbatim prefix:\n%s", got)
	}
	if !strings.Contains(string(got), "sf hook pre") {
		t.Error("hook block not appended")
	}
	if !strings.Contains(string(got), "[mcp_servers.sofia]") {
		t.Error("mcp block not appended")
	}

	backup, err := os.ReadFile(path + ".sf-bak")
	if err != nil {
		t.Fatalf("no backup written: %v", err)
	}
	if string(backup) != original {
		t.Errorf("backup = %q, want original bytes %q", backup, original)
	}

	byName := itemsByName(res.Items)
	if byName["codex-hook"].Status != statusWritten {
		t.Errorf("codex-hook = %+v", byName["codex-hook"])
	}
	if byName["codex-mcp"].Status != statusWritten {
		t.Errorf("codex-mcp = %+v", byName["codex-mcp"])
	}
}

// TestCodexAlreadyWired checks the no-op path: both markers already present
// means both steps report ok and the file is left untouched byte-for-byte
// (in particular, no backup is written for a file that wasn't modified).
func TestCodexAlreadyWired(t *testing.T) {
	isolate(t)
	codexHome := codexIsolate(t)
	path := filepath.Join(codexHome, "config.toml")
	original := "[[hooks.PreToolUse]]\nmatcher = \"^Bash$\"\n\n[[hooks.PreToolUse.hooks]]\ncommand = \"sf hook pre\"\n\n[mcp_servers.sofia]\ncommand = \"sf\"\nargs = [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()

	res, err := execute(Options{Project: project})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	byName := itemsByName(res.Items)
	if byName["codex-hook"].Status != statusOK {
		t.Errorf("codex-hook = %+v", byName["codex-hook"])
	}
	if byName["codex-mcp"].Status != statusOK {
		t.Errorf("codex-mcp = %+v", byName["codex-mcp"])
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("config.toml bytes changed:\n%s", got)
	}
	if _, err := os.Stat(path + ".sf-bak"); err == nil {
		t.Error("a backup was written even though nothing changed")
	}
}

// TestCodexConfigCreated covers the missing-file path: config.toml doesn't
// exist yet, so it's created containing both blocks and no backup is made
// (there was nothing to back up).
func TestCodexConfigCreated(t *testing.T) {
	isolate(t)
	codexHome := codexIsolate(t)
	project := t.TempDir()

	res, err := execute(Options{Project: project})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	path := filepath.Join(codexHome, "config.toml")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config.toml not created: %v", err)
	}
	if !bytes.Contains(got, []byte("sf hook pre")) || !bytes.Contains(got, []byte("[mcp_servers.sofia]")) {
		t.Errorf("config.toml missing an expected block:\n%s", got)
	}
	if _, err := os.Stat(path + ".sf-bak"); err == nil {
		t.Error("a backup was written for a freshly created file")
	}

	byName := itemsByName(res.Items)
	if got := byName["codex-hook"]; got.Status != statusWritten || got.Detail != "created config.toml" {
		t.Errorf("codex-hook = %+v", got)
	}
}

// TestCodexSkillInstall checks the skill lands at the Codex user-level
// skills path and behaves the same missing→written / identical→ok as the
// Claude skill step.
func TestCodexSkillInstall(t *testing.T) {
	isolate(t)
	codexIsolate(t)
	project := t.TempDir()

	res, err := execute(Options{Project: project})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	byName := itemsByName(res.Items)
	if byName["codex-skill"].Status != statusWritten {
		t.Errorf("codex-skill = %+v", byName["codex-skill"])
	}

	dest := filepath.Join(os.Getenv("HOME"), ".agents", "skills", "sf-context", "SKILL.md")
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, sofia.SkillMD) {
		t.Fatalf("installed codex skill != bundled copy (err=%v)", err)
	}

	res2, err := execute(Options{Project: project})
	if err != nil {
		t.Fatalf("execute (second run): %v", err)
	}
	if got := itemsByName(res2.Items)["codex-skill"]; got.Status != statusOK {
		t.Errorf("second run codex-skill = %+v, want ok", got)
	}
}

// TestCorporateSkipsCodex checks --corporate reports only the agents-md
// item, even with Codex detected on the machine — no config.toml write.
func TestCorporateSkipsCodex(t *testing.T) {
	isolate(t)
	codexHome := codexIsolate(t)
	project := t.TempDir()

	res, err := execute(Options{Project: project, Corporate: true})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.CodexOnMachine {
		t.Fatal("test setup: Codex should be detected")
	}
	if len(res.Items) != 1 || res.Items[0].Name != "agents-md" {
		t.Errorf("items = %+v, want a single agents-md item", res.Items)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "config.toml")); err == nil {
		t.Error("config.toml was written despite --corporate")
	}
}

// TestCodexConfigTOMLShape asserts the appended blocks keep config.toml
// structurally valid TOML, without pulling in a TOML parser dependency: the
// repo carries no TOML library (go.mod only has yaml.v3, used for other
// config formats), and adding one just for this one test isn't worth a new
// dependency. Instead this checks every appended line is one of the shapes
// valid top-level TOML permits: blank, a comment, a "[table]" or
// "[[array-of-tables]]" header, or a "key = value" pair.
func TestCodexConfigTOMLShape(t *testing.T) {
	isolate(t)
	codexHome := codexIsolate(t)
	project := t.TempDir()

	if _, err := execute(Options{Project: project}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(got), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
		case strings.Contains(line, "="):
		default:
			t.Errorf("line %q is not a valid top-level TOML construct", line)
		}
	}
	if !strings.Contains(string(got), `matcher = "^Bash$"`) {
		t.Error("hook matcher missing")
	}
	if !strings.Contains(string(got), `args = ["mcp"]`) {
		t.Error("mcp args missing")
	}
}

// checkPaths lists every file a real (non-corporate) init run can write, for
// TestCheckWritesNothing/TestCheckReportsAlreadyOk to snapshot.
func checkPaths(claudeDir, codexHome, project string) []string {
	return []string{
		filepath.Join(project, "AGENTS.md"),
		filepath.Join(claudeDir, "skills", "sf-context", "SKILL.md"),
		filepath.Join(claudeDir, "settings.json"),
		filepath.Join(project, ".mcp.json"),
		filepath.Join(codexHome, "config.toml"),
		filepath.Join(os.Getenv("HOME"), ".agents", "skills", "sf-context", "SKILL.md"),
	}
}

// TestCheckWritesNothing runs --check against a fresh project with both
// Claude and Codex detected: every actionable step must report "would", and
// not one byte may land on disk.
func TestCheckWritesNothing(t *testing.T) {
	claudeDir := isolate(t)
	codexHome := codexIsolate(t)
	project := t.TempDir()

	res, err := execute(Options{Project: project, Check: true})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	byName := itemsByName(res.Items)
	for _, name := range []string{"agents-md", "skill", "hook", "mcp", "codex-hook", "codex-mcp", "codex-skill"} {
		if got := byName[name]; got.Status != statusWould {
			t.Errorf("%s = %+v, want status %q", name, got, statusWould)
		}
	}

	for _, p := range checkPaths(claudeDir, codexHome, project) {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was written under --check", p)
		}
	}
	entries, err := os.ReadDir(claudeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("CLAUDE_DIR is not empty after --check: %v", entries)
	}
}

// TestCheckReportsAlreadyOk runs a real init first, then --check against the
// same project/machine state: every item should read "ok", and nothing that
// was already written gets touched again (checked by byte content and mtime,
// not just presence).
func TestCheckReportsAlreadyOk(t *testing.T) {
	claudeDir := isolate(t)
	codexHome := codexIsolate(t)
	project := t.TempDir()

	if _, err := execute(Options{Project: project}); err != nil {
		t.Fatalf("execute (real run): %v", err)
	}

	type snapshot struct {
		bytes []byte
		mtime time.Time
	}
	paths := checkPaths(claudeDir, codexHome, project)
	before := make(map[string]snapshot, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("setup: %s missing after the real run: %v", p, err)
		}
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		before[p] = snapshot{b, st.ModTime()}
	}

	res, err := execute(Options{Project: project, Check: true})
	if err != nil {
		t.Fatalf("execute (--check): %v", err)
	}
	for _, it := range res.Items {
		if it.Status != statusOK {
			t.Errorf("%s = %+v, want status %q", it.Name, it, statusOK)
		}
	}

	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s vanished after --check: %v", p, err)
		}
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, before[p].bytes) {
			t.Errorf("%s bytes changed under --check", p)
		}
		if !st.ModTime().Equal(before[p].mtime) {
			t.Errorf("%s mtime changed under --check", p)
		}
	}
}

// TestCheckReportsWouldReplace covers the "would" detail text for the one
// step whose real-run detail varies by branch (append vs replace).
func TestCheckReportsWouldReplace(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	path := filepath.Join(project, "AGENTS.md")
	before := "# Before\n\n<!-- sf:begin -->\nSTALE CONTENT\n<!-- sf:end -->\n\n# After\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	item := agentsMDStep(project, true)
	if item.Status != statusWould || item.Detail != "would replace managed block" {
		t.Errorf("item = %+v", item)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != before {
		t.Errorf("AGENTS.md was modified under --check:\n%s", got)
	}
}
