package initcmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sofia-ctx/sofia"
)

// Codex block markers, matching the first/last lines of codexHookBlock and
// codexMCPBlock — revert removes the whole marked span.
const (
	codexHookBegin = "# sf:hook:begin"
	codexHookEnd   = "# sf:hook:end"
	codexMCPBegin  = "# sf:mcp:begin"
	codexMCPEnd    = "# sf:mcp:end"
)

// executeRevert undoes every onboarding step init performs, in the same
// order. Each removal is surgical — the managed span/entry is taken out and
// everything else is left byte-identical — rather than a restore from the
// .sf-bak backups, which may predate later hand edits. Like execute, a step
// going wrong is reported as skipped, never an error; only a bad --project
// path fails the call. Steps aren't gated on agent detection: a wiring
// that isn't there reports ok, which makes revert idempotent.
func executeRevert(opts Options) (*Result, error) {
	project, err := resolveProject(opts.Project)
	if err != nil {
		return nil, err
	}

	res := &Result{
		ClaudeOnMachine: claudeOnMachine(),
		ClaudeInProject: claudeInProject(project),
		CodexOnMachine:  codexOnMachine(),
		Check:           opts.Check,
		Revert:          true,
	}
	res.Items = append(res.Items, revertAgentsMDStep(project, opts.Check))
	res.Items = append(res.Items, revertSkillStep(opts.Force, opts.Check))
	res.Items = append(res.Items, revertHookStep(opts.Check))
	res.Items = append(res.Items, revertMCPStep(project, opts.Check))
	res.Items = append(res.Items, revertCodexBlockStep("codex-hook", codexHookBegin, codexHookEnd, "PreToolUse hook", opts.Check))
	res.Items = append(res.Items, revertCodexBlockStep("codex-mcp", codexMCPBegin, codexMCPEnd, "sofia MCP server", opts.Check))
	res.Items = append(res.Items, revertCodexSkillStep(opts.Force, opts.Check))
	return res, nil
}

// revertAgentsMDStep removes the managed sf block from the project's
// AGENTS.md. If nothing but whitespace remains — the file was init's own
// creation — the file itself is removed.
func revertAgentsMDStep(project string, check bool) Item {
	path := filepath.Join(project, "AGENTS.md")
	existing, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Item{"agents-md", statusOK, "no AGENTS.md"}
	}
	if err != nil {
		return Item{"agents-md", statusSkipped, fmt.Sprintf("read failed: %v", err)}
	}

	next, found := stripMarkedSpan(string(existing), beginMarker, endMarker)
	if !found {
		return Item{"agents-md", statusOK, "no managed block"}
	}
	if strings.TrimSpace(next) == "" {
		if check {
			return Item{"agents-md", statusWould, "would remove AGENTS.md (holds only the managed block)"}
		}
		if rerr := os.Remove(path); rerr != nil {
			return Item{"agents-md", statusSkipped, fmt.Sprintf("remove failed: %v", rerr)}
		}
		return Item{"agents-md", statusRemoved, "removed AGENTS.md (held only the managed block)"}
	}
	if check {
		return Item{"agents-md", statusWould, "would remove managed block"}
	}
	if werr := os.WriteFile(path, []byte(next), 0o644); werr != nil {
		return Item{"agents-md", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{"agents-md", statusRemoved, "removed managed block"}
}

// stripMarkedSpan removes the span from beginMark through endMark inclusive,
// plus the single newline after the end marker and the blank line before the
// begin marker that appending inserted — so an append followed by a strip is
// byte-identical to the original. found is false when the markers aren't
// both present in order.
func stripMarkedSpan(content, beginMark, endMark string) (next string, found bool) {
	beginIdx := strings.Index(content, beginMark)
	endIdx := strings.Index(content, endMark)
	if beginIdx < 0 || endIdx < beginIdx {
		return content, false
	}
	head := content[:beginIdx]
	tail := content[endIdx+len(endMark):]
	tail = strings.TrimPrefix(tail, "\n")
	if strings.HasSuffix(head, "\n\n") {
		head = head[:len(head)-1]
	}
	return head + tail, true
}

// removeSkill is installSkill's inverse: the whole skill directory is
// removed when the installed copy matches the bundled one (or under
// --force), and left alone with a pointer at --force when it differs — a
// hand-edited copy shouldn't vanish silently.
func removeSkill(dest string, force, check bool, name string) Item {
	installed, err := os.ReadFile(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return Item{name, statusOK, "not installed"}
	}
	if err != nil {
		return Item{name, statusSkipped, fmt.Sprintf("read failed: %v", err)}
	}
	if !bytes.Equal(installed, sofia.SkillMD) && !force {
		return Item{name, statusSkipped, "differs from the bundled copy — --force to remove"}
	}
	if check {
		return Item{name, statusWould, "would remove sf-context skill"}
	}
	if rerr := os.RemoveAll(filepath.Dir(dest)); rerr != nil {
		return Item{name, statusSkipped, fmt.Sprintf("remove failed: %v", rerr)}
	}
	return Item{name, statusRemoved, "removed sf-context skill"}
}

func revertSkillStep(force, check bool) Item {
	dest := filepath.Join(claudeDir(), "skills", "sf-context", "SKILL.md")
	return removeSkill(dest, force, check, "skill")
}

func revertCodexSkillStep(force, check bool) Item {
	dest := filepath.Join(codexSkillsHome(), "sf-context", "SKILL.md")
	return removeSkill(dest, force, check, "codex-skill")
}

// revertHookStep removes the sf PreToolUse entry from
// $CLAUDE_DIR/settings.json, leaving every other hook and key intact. A
// PreToolUse array (or hooks object) left empty by the removal is dropped,
// since init created it on demand. The init-era .sf-bak backup is
// deliberately not consulted or touched — it may predate later hand edits,
// and it stays available for a manual restore.
func revertHookStep(check bool) Item {
	path := filepath.Join(claudeDir(), "settings.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Item{"hook", statusOK, "not wired"}
	}
	if err != nil {
		return Item{"hook", statusSkipped, fmt.Sprintf("read failed: %v", err)}
	}
	if !bytes.Contains(raw, []byte("sf hook pre")) {
		return Item{"hook", statusOK, "not wired"}
	}

	var doc map[string]any
	if uerr := json.Unmarshal(raw, &doc); uerr != nil || doc == nil {
		return Item{"hook", statusSkipped, "unrecognized settings.json shape — unwire manually, see README"}
	}
	hooksMap, ok := doc["hooks"].(map[string]any)
	if !ok {
		return Item{"hook", statusSkipped, "unrecognized settings.json shape — unwire manually, see README"}
	}
	preList, ok := hooksMap["PreToolUse"].([]any)
	if !ok {
		return Item{"hook", statusSkipped, "unrecognized settings.json shape — unwire manually, see README"}
	}

	kept := preList[:0:0]
	for _, entry := range preList {
		if !entryRunsSfHook(entry) {
			kept = append(kept, entry)
		}
	}
	if len(kept) == len(preList) {
		// The raw bytes mention the hook but not where init put it.
		return Item{"hook", statusSkipped, "unrecognized settings.json shape — unwire manually, see README"}
	}
	if check {
		return Item{"hook", statusWould, "would unwire PreToolUse hook"}
	}

	if len(kept) > 0 {
		hooksMap["PreToolUse"] = kept
	} else {
		delete(hooksMap, "PreToolUse")
	}
	if len(hooksMap) == 0 {
		delete(doc, "hooks")
	}

	out, merr := json.MarshalIndent(doc, "", "  ")
	if merr != nil {
		return Item{"hook", statusSkipped, fmt.Sprintf("marshal failed: %v", merr)}
	}
	if werr := os.WriteFile(path, out, 0o644); werr != nil {
		return Item{"hook", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{"hook", statusRemoved, "unwired PreToolUse hook"}
}

// entryRunsSfHook reports whether a hooks.PreToolUse entry dispatches to
// `sf hook pre` — the shape hookStep writes.
func entryRunsSfHook(v any) bool {
	entry, ok := v.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := entry["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hooks {
		if hm, ok := h.(map[string]any); ok && hm["command"] == "sf hook pre" {
			return true
		}
	}
	return false
}

// revertMCPStep removes the sofia server from <project>/.mcp.json. When
// sofia was the only key in the only top-level object — the exact file init
// creates from scratch — the file itself is removed.
func revertMCPStep(project string, check bool) Item {
	path := filepath.Join(project, ".mcp.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Item{"mcp", statusOK, "not registered"}
	}
	if err != nil {
		return Item{"mcp", statusSkipped, fmt.Sprintf("read failed: %v", err)}
	}

	var doc map[string]any
	if uerr := json.Unmarshal(raw, &doc); uerr != nil || doc == nil {
		return Item{"mcp", statusSkipped, "unrecognized .mcp.json shape — unwire manually, see README"}
	}
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		return Item{"mcp", statusOK, "not registered"}
	}
	if _, exists := servers["sofia"]; !exists {
		return Item{"mcp", statusOK, "not registered"}
	}

	delete(servers, "sofia")
	if len(servers) == 0 && len(doc) == 1 {
		if check {
			return Item{"mcp", statusWould, "would remove .mcp.json (holds only sofia)"}
		}
		if rerr := os.Remove(path); rerr != nil {
			return Item{"mcp", statusSkipped, fmt.Sprintf("remove failed: %v", rerr)}
		}
		return Item{"mcp", statusRemoved, "removed .mcp.json (held only sofia)"}
	}
	if check {
		return Item{"mcp", statusWould, "would unregister sofia"}
	}

	out, merr := json.MarshalIndent(doc, "", "  ")
	if merr != nil {
		return Item{"mcp", statusSkipped, fmt.Sprintf("marshal failed: %v", merr)}
	}
	if werr := os.WriteFile(path, out, 0o644); werr != nil {
		return Item{"mcp", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{"mcp", statusRemoved, "unregistered sofia"}
}

// revertCodexBlockStep removes a managed block from $CODEX_HOME/config.toml
// by its begin/end comment markers, leaving the rest of the file
// byte-identical. The file is never removed, even when emptied — it belongs
// to Codex, not to sf.
func revertCodexBlockStep(name, beginMark, endMark, what string, check bool) Item {
	path := filepath.Join(codexDir(), "config.toml")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Item{name, statusOK, "not wired"}
	}
	if err != nil {
		return Item{name, statusSkipped, fmt.Sprintf("read failed: %v", err)}
	}
	next, found := stripMarkedSpan(string(raw), beginMark, endMark)
	if !found {
		return Item{name, statusOK, "not wired"}
	}
	if check {
		return Item{name, statusWould, "would remove " + what}
	}
	if werr := os.WriteFile(path, []byte(next), 0o644); werr != nil {
		return Item{name, statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{name, statusRemoved, "removed " + what}
}
