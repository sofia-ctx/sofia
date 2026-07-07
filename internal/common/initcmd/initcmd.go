// Package initcmd implements `sf init` — one-shot per-project onboarding
// that wires sf up for a project's coding agents: a managed block in
// AGENTS.md, the sf-context skill, the PreToolUse hook, and MCP server
// registration. Steps beyond the AGENTS.md block are gated on whether
// Claude Code is detected on the machine and/or in the project, and
// --corporate skips all of them for locked-down environments where only
// instruction files are writable.
package initcmd

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sofia-ctx/sofia"
	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/toon"
)

// Managed-block markers in AGENTS.md. The block is safe to replace wholesale
// on every run — it says so in its own first line.
const (
	beginMarker = "<!-- sf:begin -->"
	endMarker   = "<!-- sf:end -->"
)

//go:embed agents_block.md
var agentsBlock string

// Status values for an Item.
const (
	statusWritten = "written"
	statusOK      = "ok"
	statusSkipped = "skipped"
)

// Item is one onboarding step's outcome — same shape as doctor.Check, same
// voice: a short Status plus a sentence of Detail.
type Item struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Result is the full report: what was detected, and what each step did.
type Result struct {
	ClaudeOnMachine bool   `json:"claude_on_machine"`
	ClaudeInProject bool   `json:"claude_in_project"`
	Items           []Item `json:"items"`
}

// Options carries flag state.
type Options struct {
	Project   string
	Corporate bool
	Force     bool
	Format    string
}

// Run executes the onboarding, renders the report, and logs the call.
func Run(opts Options, w io.Writer) error {
	tracker := calllog.Start("init", logArgs(opts))
	res, err := execute(opts)
	if err != nil {
		tracker.Finish(err)
		return err
	}

	written, skipped := 0, 0
	for _, it := range res.Items {
		switch it.Status {
		case statusWritten:
			written++
		case statusSkipped:
			skipped++
		}
	}
	tracker.SetSummary(map[string]any{"written": written, "skipped": skipped})

	cw := &calllog.Counter{W: w}
	renderErr := render(cw, opts.Format, res)
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

func logArgs(opts Options) []string {
	args := []string{"--format=" + opts.Format}
	if opts.Project != "" {
		args = append(args, "--project="+opts.Project)
	}
	if opts.Corporate {
		args = append(args, "--corporate")
	}
	if opts.Force {
		args = append(args, "--force")
	}
	return args
}

// execute runs every onboarding step in order and collects their outcomes.
// It never errors on a step going wrong — a failed write is reported as a
// skipped item — only a bad --project path fails the whole call.
func execute(opts Options) (*Result, error) {
	project, err := resolveProject(opts.Project)
	if err != nil {
		return nil, err
	}

	res := &Result{
		ClaudeOnMachine: claudeOnMachine(),
		ClaudeInProject: claudeInProject(project),
	}
	res.Items = append(res.Items, agentsMDStep(project))
	if opts.Corporate {
		return res, nil
	}

	if res.ClaudeOnMachine {
		res.Items = append(res.Items, skillStep(opts.Force))
		res.Items = append(res.Items, hookStep())
	} else {
		res.Items = append(res.Items, gatedItem("skill"))
		res.Items = append(res.Items, gatedItem("hook"))
	}
	if res.ClaudeOnMachine || res.ClaudeInProject {
		res.Items = append(res.Items, mcpStep(project))
	} else {
		res.Items = append(res.Items, gatedItem("mcp"))
	}
	return res, nil
}

func gatedItem(name string) Item {
	return Item{Name: name, Status: statusSkipped, Detail: "Claude Code not detected"}
}

// resolveProject mirrors pack.resolveProject: opts.Project if set, else cwd,
// always resolved to an absolute path.
func resolveProject(project string) (string, error) {
	if project == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		project = wd
	}
	return filepath.Abs(project)
}

// claudeDir mirrors pack.claudeDir: $CLAUDE_DIR overrides, else ~/.claude.
func claudeDir() string {
	if d := os.Getenv("CLAUDE_DIR"); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude")
	}
	return ".claude"
}

// claudeOnMachine reports whether Claude Code looks installed on this
// machine: its config directory exists.
func claudeOnMachine() bool {
	st, err := os.Stat(claudeDir())
	return err == nil && st.IsDir()
}

// claudeInProject reports whether the project itself already has a Claude
// Code footprint: a .claude directory, or a CLAUDE.md.
func claudeInProject(project string) bool {
	if st, err := os.Stat(filepath.Join(project, ".claude")); err == nil && st.IsDir() {
		return true
	}
	_, err := os.Stat(filepath.Join(project, "CLAUDE.md"))
	return err == nil
}

// agentsMDStep writes/updates the managed sf block in the project's
// AGENTS.md. Missing file → created with exactly the block; a file without
// markers → the block is appended; a file with markers → the span between
// (and including) them is replaced in place, everything else untouched.
func agentsMDStep(project string) Item {
	path := filepath.Join(project, "AGENTS.md")
	existing, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if werr := os.WriteFile(path, []byte(agentsBlock), 0o644); werr != nil {
			return Item{"agents-md", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
		}
		return Item{"agents-md", statusWritten, "created AGENTS.md"}
	}
	if err != nil {
		return Item{"agents-md", statusSkipped, fmt.Sprintf("read failed: %v", err)}
	}

	content := string(existing)
	next, detail := mergeAgentsMD(content)
	if next == content {
		return Item{"agents-md", statusOK, "already up to date"}
	}
	if werr := os.WriteFile(path, []byte(next), 0o644); werr != nil {
		return Item{"agents-md", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{"agents-md", statusWritten, detail}
}

// mergeAgentsMD computes the next AGENTS.md content given its current
// content: replace the marked span in place when present (only the span
// itself changes — the block's own trailing newline is trimmed since the
// bytes right after the old end marker already supply it), else append a
// blank line plus the block. Pure and separately testable from the
// idempotence it's meant to guarantee.
func mergeAgentsMD(content string) (next, detail string) {
	beginIdx := strings.Index(content, beginMarker)
	endIdx := strings.Index(content, endMarker)
	if beginIdx >= 0 && endIdx > beginIdx {
		core := strings.TrimRight(agentsBlock, "\n")
		return content[:beginIdx] + core + content[endIdx+len(endMarker):], "replaced managed block"
	}
	return content + "\n" + agentsBlock, "appended managed block"
}

// skillStep installs the sf-context skill into $CLAUDE_DIR/skills, comparing
// against the copy embedded in the binary (sofia.SkillMD) — the same asset
// doctor's checkSkill falls back to when there's no repo checkout to diff.
func skillStep(force bool) Item {
	dest := filepath.Join(claudeDir(), "skills", "sf-context", "SKILL.md")
	installed, err := os.ReadFile(dest)
	if errors.Is(err, fs.ErrNotExist) {
		if werr := writeFileAll(dest, sofia.SkillMD, 0o644); werr != nil {
			return Item{"skill", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
		}
		return Item{"skill", statusWritten, "installed sf-context skill"}
	}
	if err != nil {
		return Item{"skill", statusSkipped, fmt.Sprintf("read failed: %v", err)}
	}
	if bytes.Equal(installed, sofia.SkillMD) {
		return Item{"skill", statusOK, "up to date"}
	}
	if !force {
		return Item{"skill", statusSkipped, "differs from the bundled copy — --force to overwrite"}
	}
	if werr := os.WriteFile(dest, sofia.SkillMD, 0o644); werr != nil {
		return Item{"skill", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{"skill", statusWritten, "overwrote hand-edited/stale copy (--force)"}
}

// hookStep wires the PreToolUse hook into $CLAUDE_DIR/settings.json. If the
// raw bytes already mention "sf hook pre" — the same signal doctor's
// checkHook uses — it's left alone. Otherwise the file is decoded, the
// hooks.PreToolUse array is created or extended (never touching an
// unrecognized shape), and a .sf-bak copy of the pre-existing file is made
// before the first write.
func hookStep() Item {
	dir := claudeDir()
	path := filepath.Join(dir, "settings.json")
	raw, err := os.ReadFile(path)
	existed := err == nil
	switch {
	case errors.Is(err, fs.ErrNotExist):
		raw = []byte("{}")
	case err != nil:
		return Item{"hook", statusSkipped, fmt.Sprintf("read failed: %v", err)}
	case bytes.Contains(raw, []byte("sf hook pre")):
		return Item{"hook", statusOK, "already wired"}
	}

	var doc map[string]any
	if uerr := json.Unmarshal(raw, &doc); uerr != nil {
		return Item{"hook", statusSkipped, "unrecognized settings.json shape — wire manually, see README"}
	}
	if doc == nil {
		// Valid JSON ("null") that isn't an object — same "won't clobber it"
		// signal as any other unrecognized shape, not a nil-map write later.
		return Item{"hook", statusSkipped, "unrecognized settings.json shape — wire manually, see README"}
	}

	hooksMap, ok := asObject(doc["hooks"])
	if !ok {
		return Item{"hook", statusSkipped, "unrecognized settings.json shape — wire manually, see README"}
	}
	preList, ok := asArray(hooksMap["PreToolUse"])
	if !ok {
		return Item{"hook", statusSkipped, "unrecognized settings.json shape — wire manually, see README"}
	}
	preList = append(preList, map[string]any{
		"matcher": "Read|Bash",
		"hooks": []any{
			map[string]any{"type": "command", "command": "sf hook pre", "timeout": 10},
		},
	})
	hooksMap["PreToolUse"] = preList
	doc["hooks"] = hooksMap

	detail := "created settings.json"
	if existed {
		if berr := os.WriteFile(path+".sf-bak", raw, 0o644); berr != nil {
			return Item{"hook", statusSkipped, fmt.Sprintf("backup failed: %v", berr)}
		}
		detail = "backup: settings.json.sf-bak"
	}

	out, merr := json.MarshalIndent(doc, "", "  ")
	if merr != nil {
		return Item{"hook", statusSkipped, fmt.Sprintf("marshal failed: %v", merr)}
	}
	if werr := writeFileAll(path, out, 0o644); werr != nil {
		return Item{"hook", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{"hook", statusWritten, detail}
}

// mcpStep registers the sofia MCP server in <project>/.mcp.json, preserving
// any servers already declared there.
func mcpStep(project string) Item {
	path := filepath.Join(project, ".mcp.json")
	sofiaServer := map[string]any{"command": "sf", "args": []any{"mcp"}}

	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		doc := map[string]any{"mcpServers": map[string]any{"sofia": sofiaServer}}
		out, _ := json.MarshalIndent(doc, "", "  ")
		if werr := os.WriteFile(path, out, 0o644); werr != nil {
			return Item{"mcp", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
		}
		return Item{"mcp", statusWritten, "registered sofia"}
	}
	if err != nil {
		return Item{"mcp", statusSkipped, fmt.Sprintf("read failed: %v", err)}
	}

	var doc map[string]any
	if uerr := json.Unmarshal(raw, &doc); uerr != nil {
		return Item{"mcp", statusSkipped, "unrecognized .mcp.json shape — wire manually, see README"}
	}
	if doc == nil {
		// Valid JSON ("null") that isn't an object — same "won't clobber it"
		// signal as any other unrecognized shape, not a nil-map write later.
		return Item{"mcp", statusSkipped, "unrecognized .mcp.json shape — wire manually, see README"}
	}
	servers, ok := asObject(doc["mcpServers"])
	if !ok {
		return Item{"mcp", statusSkipped, "unrecognized .mcp.json shape — wire manually, see README"}
	}
	if _, exists := servers["sofia"]; exists {
		return Item{"mcp", statusOK, "already registered"}
	}
	servers["sofia"] = sofiaServer
	doc["mcpServers"] = servers

	out, merr := json.MarshalIndent(doc, "", "  ")
	if merr != nil {
		return Item{"mcp", statusSkipped, fmt.Sprintf("marshal failed: %v", merr)}
	}
	if werr := os.WriteFile(path, out, 0o644); werr != nil {
		return Item{"mcp", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{"mcp", statusWritten, "registered sofia"}
}

// asObject type-asserts v as a JSON object, treating a missing key (nil) as
// an empty-but-valid object so a fresh hooks/mcpServers key can be created;
// any other type is the "unrecognized shape" signal callers must not clobber.
func asObject(v any) (map[string]any, bool) {
	if v == nil {
		return map[string]any{}, true
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// asArray type-asserts v as a JSON array, treating a missing key (nil) as an
// empty-but-valid array; see asObject.
func asArray(v any) ([]any, bool) {
	if v == nil {
		return []any{}, true
	}
	a, ok := v.([]any)
	return a, ok
}

// writeFileAll writes data to destAbs, creating parent directories first.
func writeFileAll(destAbs string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destAbs, data, perm)
}

func render(w io.Writer, format string, res *Result) error {
	switch format {
	case "", "toon":
		renderTOON(w, res)
		return nil
	case "md":
		renderMarkdown(w, res)
		return nil
	case "json":
		return renderJSON(w, res)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}

func renderTOON(w io.Writer, res *Result) {
	fmt.Fprintf(w, "# claude on machine: %s\n", yesNo(res.ClaudeOnMachine))
	fmt.Fprintf(w, "# claude in project: %s\n", yesNo(res.ClaudeInProject))
	fmt.Fprintf(w, "items[%d]{item,status,detail}:\n", len(res.Items))
	for _, it := range res.Items {
		fmt.Fprintf(w, "%s%s,%s,%s\n", toon.Indent, it.Name, it.Status, toon.Scalar(it.Detail))
	}
}

func renderMarkdown(w io.Writer, res *Result) {
	fmt.Fprintf(w, "**claude on machine:** %s  \n**claude in project:** %s\n\n", yesNo(res.ClaudeOnMachine), yesNo(res.ClaudeInProject))
	fmt.Fprintln(w, "| Item | Status | Detail |")
	fmt.Fprintln(w, "| --- | --- | --- |")
	for _, it := range res.Items {
		fmt.Fprintf(w, "| %s | %s | %s |\n", it.Name, it.Status, it.Detail)
	}
}

func renderJSON(w io.Writer, res *Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(res)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
