package initcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sofia-ctx/sofia"
)

// initThenRevert runs a full init and a full revert over the same project.
func initThenRevert(t *testing.T, project string) map[string]Item {
	t.Helper()
	if _, err := execute(Options{Project: project}); err != nil {
		t.Fatal(err)
	}
	res, err := executeRevert(Options{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	return itemsByName(res.Items)
}

func TestRevertRemovesFreshInitFootprint(t *testing.T) {
	dir := isolate(t)
	project := t.TempDir()

	items := initThenRevert(t, project)
	for name, want := range map[string]string{
		"agents-md": statusRemoved,
		"skill":     statusRemoved,
		"hook":      statusRemoved,
		"mcp":       statusRemoved,
	} {
		if items[name].Status != want {
			t.Errorf("%s = %+v, want status %s", name, items[name], want)
		}
	}

	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("AGENTS.md created by init survived revert")
	}
	if _, err := os.Stat(filepath.Join(project, ".mcp.json")); !os.IsNotExist(err) {
		t.Error(".mcp.json created by init survived revert")
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "sf-context")); !os.IsNotExist(err) {
		t.Error("skill dir survived revert")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sf hook pre") {
		t.Errorf("hook survived revert: %s", raw)
	}
}

func TestRevertIsIdempotent(t *testing.T) {
	isolate(t)
	project := t.TempDir()

	initThenRevert(t, project)
	res, err := executeRevert(Options{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range res.Items {
		if it.Status != statusOK {
			t.Errorf("second revert: %+v, want status ok", it)
		}
	}
}

func TestRevertAgentsMDKeepsOwnContent(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	path := filepath.Join(project, "AGENTS.md")
	original := "# My project\n\nHand-written instructions.\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	initThenRevert(t, project)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("AGENTS.md after init+revert = %q, want %q", got, original)
	}
}

func TestRevertHookPreservesOtherHooks(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "settings.json")
	original := `{"unrelatedKey":"keepme","hooks":{"PreToolUse":[{"matcher":"Foo","hooks":[{"type":"command","command":"other-hook"}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	items := initThenRevert(t, t.TempDir())
	if items["hook"].Status != statusRemoved {
		t.Fatalf("hook = %+v", items["hook"])
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
	pre, ok := doc["hooks"].(map[string]any)["PreToolUse"].([]any)
	if !ok || len(pre) != 1 {
		t.Fatalf("PreToolUse = %v, want the one original entry", pre)
	}
	if !strings.Contains(string(got), "other-hook") {
		t.Error("original PreToolUse entry lost")
	}
}

func TestRevertHookDropsEmptiedKeys(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"model":"opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	initThenRevert(t, t.TempDir())

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if _, exists := doc["hooks"]; exists {
		t.Errorf("emptied hooks key kept: %s", got)
	}
	if doc["model"] != "opus" {
		t.Errorf("unrelated setting lost: %s", got)
	}
}

func TestRevertHookUnrecognizedShape(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "settings.json")
	// Mentions the hook command, but not in the shape init writes.
	if err := os.WriteFile(path, []byte(`{"hooks":{"PreToolUse":"sf hook pre"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	item := revertHookStep(false)
	if item.Status != statusSkipped {
		t.Fatalf("item = %+v, want skipped", item)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "sf hook pre") {
		t.Error("unrecognized settings.json was modified")
	}
}

func TestRevertMCPPreservesOtherServers(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	path := filepath.Join(project, ".mcp.json")
	original := `{"mcpServers":{"other":{"command":"other-mcp"}}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	items := initThenRevert(t, project)
	if items["mcp"].Status != statusRemoved {
		t.Fatalf("mcp = %+v", items["mcp"])
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
	if _, exists := servers["sofia"]; exists {
		t.Error("sofia entry survived revert")
	}
	if _, exists := servers["other"]; !exists {
		t.Error("other server lost")
	}
}

func TestRevertSkillDiffersNeedsForce(t *testing.T) {
	dir := isolate(t)
	dest := filepath.Join(dir, "skills", "sf-context", "SKILL.md")
	if err := writeFileAll(dest, []byte("hand-edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	item := revertSkillStep(false, false)
	if item.Status != statusSkipped {
		t.Fatalf("without force: %+v, want skipped", item)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal("hand-edited skill removed without --force")
	}

	item = revertSkillStep(true, false)
	if item.Status != statusRemoved {
		t.Fatalf("with force: %+v, want removed", item)
	}
	if _, err := os.Stat(filepath.Dir(dest)); !os.IsNotExist(err) {
		t.Error("skill dir survived forced revert")
	}
}

func TestRevertCodexKeepsUserConfig(t *testing.T) {
	isolate(t)
	codexHome := codexIsolate(t)
	path := filepath.Join(codexHome, "config.toml")
	original := "model = \"o4\"\n\n[projects.demo]\ntrusted = true\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	items := initThenRevert(t, t.TempDir())
	for _, name := range []string{"codex-hook", "codex-mcp", "codex-skill"} {
		if items[name].Status != statusRemoved {
			t.Errorf("%s = %+v, want removed", name, items[name])
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("config.toml after init+revert = %q, want %q", got, original)
	}
	skill := filepath.Join(os.Getenv("HOME"), ".agents", "skills", "sf-context")
	if _, err := os.Stat(skill); !os.IsNotExist(err) {
		t.Error("codex skill dir survived revert")
	}
}

func TestRevertCheckWritesNothing(t *testing.T) {
	dir := isolate(t)
	project := t.TempDir()
	if _, err := execute(Options{Project: project}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	res, err := executeRevert(Options{Project: project, Check: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agents-md", "skill", "hook", "mcp"} {
		if it := itemsByName(res.Items)[name]; it.Status != statusWould {
			t.Errorf("%s = %+v, want would", name, it)
		}
	}

	after, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("--check modified settings.json")
	}
	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); err != nil {
		t.Error("--check removed AGENTS.md")
	}
	installed, err := os.ReadFile(filepath.Join(dir, "skills", "sf-context", "SKILL.md"))
	if err != nil || string(installed) != string(sofia.SkillMD) {
		t.Error("--check touched the installed skill")
	}
}

func TestStripMarkedSpan(t *testing.T) {
	tests := []struct {
		name    string
		content string
		next    string
		found   bool
	}{
		{"appended", "own\n\n<!-- sf:begin -->\nblock\n<!-- sf:end -->\n", "own\n", true},
		{"only block", "<!-- sf:begin -->\nblock\n<!-- sf:end -->\n", "", true},
		{"mid file", "a\n<!-- sf:begin -->x<!-- sf:end -->\nb\n", "a\nb\n", true},
		{"no markers", "just text\n", "just text\n", false},
		{"end before begin", "<!-- sf:end --><!-- sf:begin -->", "<!-- sf:end --><!-- sf:begin -->", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, found := stripMarkedSpan(tt.content, beginMarker, endMarker)
			if next != tt.next || found != tt.found {
				t.Errorf("stripMarkedSpan(%q) = (%q, %v), want (%q, %v)", tt.content, next, found, tt.next, tt.found)
			}
		})
	}
}
