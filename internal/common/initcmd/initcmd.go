// Package initcmd implements `sf init` — one-shot per-project onboarding
// that wires sf up for a project's coding agents: a managed block in
// AGENTS.md, the sf-context skill, the PreToolUse hook, and MCP server
// registration — for both Claude Code and Codex CLI, each gated on its own
// detection. Steps beyond the AGENTS.md block are gated on whether Claude
// Code and/or Codex are detected on the machine (and/or, for Claude, in the
// project), and --corporate skips all of them for locked-down environments
// where only instruction files are writable.
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
	"github.com/sofia-ctx/sofia/pkg/toon"
)

// Managed-block markers in AGENTS.md. The block is safe to replace wholesale
// on every run — it says so in its own first line.
const (
	beginMarker = "<!-- sf:begin -->"
	endMarker   = "<!-- sf:end -->"
)

//go:embed agents_block.md
var agentsBlock string

// codexHookBlock/codexMCPBlock are appended verbatim to $CODEX_HOME/config.toml
// — never parsed or rewritten, see codexAppendStep.
const (
	codexHookBlock = "\n# sf:hook:begin — managed by `sf init`\n[[hooks.PreToolUse]]\nmatcher = \"^Bash$\"\n\n[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = \"sf hook pre\"\ntimeout = 10\n# sf:hook:end\n"
	codexMCPBlock  = "\n# sf:mcp:begin — managed by `sf init`\n[mcp_servers.sofia]\ncommand = \"sf\"\nargs = [\"mcp\"]\n# sf:mcp:end\n"
)

// Status values for an Item. statusWould only appears under --check: a step
// that would write in a real run, without having written anything.
const (
	statusWritten = "written"
	statusOK      = "ok"
	statusSkipped = "skipped"
	statusWould   = "would"
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
	CodexOnMachine  bool   `json:"codex_on_machine"`
	Check           bool   `json:"check,omitempty"`
	Items           []Item `json:"items"`
}

// Options carries flag state.
type Options struct {
	Project   string
	Corporate bool
	Force     bool
	Check     bool
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
	if opts.Check {
		args = append(args, "--check")
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
		CodexOnMachine:  codexOnMachine(),
		Check:           opts.Check,
	}
	res.Items = append(res.Items, agentsMDStep(project, opts.Check))
	if opts.Corporate {
		return res, nil
	}

	if res.ClaudeOnMachine {
		res.Items = append(res.Items, skillStep(opts.Force, opts.Check))
		res.Items = append(res.Items, hookStep(opts.Check))
	} else {
		res.Items = append(res.Items, gatedItem("skill", claudeNotDetected))
		res.Items = append(res.Items, gatedItem("hook", claudeNotDetected))
	}
	if res.ClaudeOnMachine || res.ClaudeInProject {
		res.Items = append(res.Items, mcpStep(project, opts.Check))
	} else {
		res.Items = append(res.Items, gatedItem("mcp", claudeNotDetected))
	}

	if res.CodexOnMachine {
		guard := newCodexConfigGuard(filepath.Join(codexDir(), "config.toml"))
		res.Items = append(res.Items, codexHookStep(guard, opts.Check))
		res.Items = append(res.Items, codexMCPStep(guard, opts.Check))
		res.Items = append(res.Items, codexSkillStep(opts.Force, opts.Check))
	} else {
		res.Items = append(res.Items, gatedItem("codex-hook", codexNotDetected))
		res.Items = append(res.Items, gatedItem("codex-mcp", codexNotDetected))
		res.Items = append(res.Items, gatedItem("codex-skill", codexNotDetected))
	}
	return res, nil
}

// Detail text for a step skipped because its agent isn't detected.
const (
	claudeNotDetected = "Claude Code not detected"
	codexNotDetected  = "Codex not detected"
)

func gatedItem(name, detail string) Item {
	return Item{Name: name, Status: statusSkipped, Detail: detail}
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

// codexDir mirrors claudeDir: $CODEX_HOME overrides, else ~/.codex.
func codexDir() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".codex")
	}
	return ".codex"
}

// codexOnMachine reports whether Codex CLI looks installed on this machine:
// its config directory exists. There's no project-level equivalent of
// claudeInProject — Codex's project config only applies in trusted
// projects, which init has no way to tell from the filesystem alone.
func codexOnMachine() bool {
	st, err := os.Stat(codexDir())
	return err == nil && st.IsDir()
}

// agentsMDStep writes/updates the managed sf block in the project's
// AGENTS.md. Missing file → created with exactly the block; a file without
// markers → the block is appended; a file with markers → the span between
// (and including) them is replaced in place, everything else untouched.
// Under check, the decision is made the same way but the write never
// happens — the Item just says what would have.
func agentsMDStep(project string, check bool) Item {
	path := filepath.Join(project, "AGENTS.md")
	existing, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if check {
			return Item{"agents-md", statusWould, "would create AGENTS.md"}
		}
		if werr := os.WriteFile(path, []byte(agentsBlock), 0o644); werr != nil {
			return Item{"agents-md", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
		}
		return Item{"agents-md", statusWritten, "created AGENTS.md"}
	}
	if err != nil {
		return Item{"agents-md", statusSkipped, fmt.Sprintf("read failed: %v", err)}
	}

	content := string(existing)
	next, replaced := mergeAgentsMD(content)
	if next == content {
		return Item{"agents-md", statusOK, "already up to date"}
	}
	action, doneDetail := "append managed block", "appended managed block"
	if replaced {
		action, doneDetail = "replace managed block", "replaced managed block"
	}
	if check {
		return Item{"agents-md", statusWould, "would " + action}
	}
	if werr := os.WriteFile(path, []byte(next), 0o644); werr != nil {
		return Item{"agents-md", statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{"agents-md", statusWritten, doneDetail}
}

// mergeAgentsMD computes the next AGENTS.md content given its current
// content: replace the marked span in place when present (only the span
// itself changes — the block's own trailing newline is trimmed since the
// bytes right after the old end marker already supply it), else append a
// blank line plus the block. Pure and separately testable from the
// idempotence it's meant to guarantee.
func mergeAgentsMD(content string) (next string, replaced bool) {
	beginIdx := strings.Index(content, beginMarker)
	endIdx := strings.Index(content, endMarker)
	if beginIdx >= 0 && endIdx > beginIdx {
		core := strings.TrimRight(agentsBlock, "\n")
		return content[:beginIdx] + core + content[endIdx+len(endMarker):], true
	}
	return content + "\n" + agentsBlock, false
}

// installSkill installs sofia.SkillMD to dest: missing → written, identical
// → ok, differs → skipped unless force, in which case the stale/hand-edited
// copy is overwritten. Shared by the Claude and Codex skill steps, which
// only differ in dest and Item name. Under check, whichever branch would
// write instead reports what it would have done.
func installSkill(dest string, force, check bool, name string) Item {
	installed, err := os.ReadFile(dest)
	if errors.Is(err, fs.ErrNotExist) {
		if check {
			return Item{name, statusWould, "would install sf-context skill"}
		}
		if werr := writeFileAll(dest, sofia.SkillMD, 0o644); werr != nil {
			return Item{name, statusSkipped, fmt.Sprintf("write failed: %v", werr)}
		}
		return Item{name, statusWritten, "installed sf-context skill"}
	}
	if err != nil {
		return Item{name, statusSkipped, fmt.Sprintf("read failed: %v", err)}
	}
	if bytes.Equal(installed, sofia.SkillMD) {
		return Item{name, statusOK, "up to date"}
	}
	if !force {
		// A real run wouldn't write here either, so this stays "skipped"
		// under --check too — it's what --force is for, not --check.
		return Item{name, statusSkipped, "differs from the bundled copy — --force to overwrite"}
	}
	if check {
		return Item{name, statusWould, "would overwrite hand-edited/stale copy (--force)"}
	}
	if werr := os.WriteFile(dest, sofia.SkillMD, 0o644); werr != nil {
		return Item{name, statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{name, statusWritten, "overwrote hand-edited/stale copy (--force)"}
}

// skillStep installs the sf-context skill into $CLAUDE_DIR/skills, comparing
// against the copy embedded in the binary (sofia.SkillMD) — the same asset
// doctor's checkSkill falls back to when there's no repo checkout to diff.
func skillStep(force, check bool) Item {
	dest := filepath.Join(claudeDir(), "skills", "sf-context", "SKILL.md")
	return installSkill(dest, force, check, "skill")
}

// codexSkillStep installs the same skill where Codex looks for user-level
// skills: $HOME/.agents/skills, the agentskills.io layout Codex's skill
// support reads from (https://developers.openai.com/codex/skills/) — not
// under $CODEX_HOME, which is config only.
func codexSkillStep(force, check bool) Item {
	dest := filepath.Join(codexSkillsHome(), "sf-context", "SKILL.md")
	return installSkill(dest, force, check, "codex-skill")
}

// codexSkillsHome is $HOME/.agents/skills; falls back to a relative path if
// $HOME can't be resolved, matching claudeDir's fallback style.
func codexSkillsHome() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".agents", "skills")
	}
	return filepath.Join(".agents", "skills")
}

// hookStep wires the PreToolUse hook into $CLAUDE_DIR/settings.json. If the
// raw bytes already mention "sf hook pre" — the same signal doctor's
// checkHook uses — it's left alone. Otherwise the file is decoded, the
// hooks.PreToolUse array is created or extended (never touching an
// unrecognized shape), and a .sf-bak copy of the pre-existing file is made
// before the first write. Under check, the decode/shape checks still run (so
// an unrecognized shape is still reported as skipped, same as a real run)
// but nothing — not even the .sf-bak backup — is written.
func hookStep(check bool) Item {
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

	if check {
		return Item{"hook", statusWould, "would wire PreToolUse hook"}
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
// any servers already declared there. Under check, both branches that would
// otherwise write instead report the same "would register sofia" outcome.
func mcpStep(project string, check bool) Item {
	path := filepath.Join(project, ".mcp.json")
	sofiaServer := map[string]any{"command": "sf", "args": []any{"mcp"}}

	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if check {
			return Item{"mcp", statusWould, "would register sofia"}
		}
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
	if check {
		return Item{"mcp", statusWould, "would register sofia"}
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

// codexConfigGuard threads backup state across codex-hook and codex-mcp,
// which both may append to the same $CODEX_HOME/config.toml: whichever of
// the two modifies the file first backs up its pre-run bytes to
// config.toml.sf-bak; the second must not re-back-up the now half-modified
// file over that backup.
type codexConfigGuard struct {
	existed  bool
	original []byte
	backedUp bool
}

// newCodexConfigGuard captures path's bytes once, before either step runs.
func newCodexConfigGuard(path string) *codexConfigGuard {
	raw, err := os.ReadFile(path)
	if err != nil {
		return &codexConfigGuard{}
	}
	return &codexConfigGuard{existed: true, original: raw}
}

// ensureBackup writes config.toml.sf-bak from the run's original bytes, once.
func (g *codexConfigGuard) ensureBackup(path string) error {
	if g.backedUp {
		return nil
	}
	g.backedUp = true
	return os.WriteFile(path+".sf-bak", g.original, 0o644)
}

// codexAppendStep appends a managed TOML block to $CODEX_HOME/config.toml
// unless marker is already present. Appending a top-level table at EOF is
// always valid TOML and never requires parsing or rewriting the user's
// existing tables/comments — same reasoning as hookStep, TOML instead of
// JSON. guard makes sure codex-hook and codex-mcp, which share this file,
// only back it up once per `sf init` run — under check, guard is never
// touched at all, since the check-gate returns before the backup/write.
func codexAppendStep(name, marker, alreadyDetail, wouldDetail, block string, guard *codexConfigGuard, check bool) Item {
	path := filepath.Join(codexDir(), "config.toml")
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		raw = nil
	case err != nil:
		return Item{name, statusSkipped, fmt.Sprintf("read failed: %v", err)}
	case bytes.Contains(raw, []byte(marker)):
		return Item{name, statusOK, alreadyDetail}
	}

	if check {
		return Item{name, statusWould, wouldDetail}
	}

	detail := "created config.toml"
	if guard.existed {
		if berr := guard.ensureBackup(path); berr != nil {
			return Item{name, statusSkipped, fmt.Sprintf("backup failed: %v", berr)}
		}
		detail = "backup: config.toml.sf-bak"
	}

	next := append(append([]byte{}, raw...), []byte(block)...)
	if werr := writeFileAll(path, next, 0o644); werr != nil {
		return Item{name, statusSkipped, fmt.Sprintf("write failed: %v", werr)}
	}
	return Item{name, statusWritten, detail}
}

// codexHookStep wires the PreToolUse hook into $CODEX_HOME/config.toml —
// same `sf hook pre` binary Claude Code calls; Codex's PreToolUse hooks use
// an identical stdin/response contract (see docs/codex.md).
func codexHookStep(guard *codexConfigGuard, check bool) Item {
	return codexAppendStep("codex-hook", "sf hook pre", "already wired", "would wire PreToolUse hook", codexHookBlock, guard, check)
}

// codexMCPStep registers the sofia MCP server in $CODEX_HOME/config.toml.
func codexMCPStep(guard *codexConfigGuard, check bool) Item {
	return codexAppendStep("codex-mcp", "[mcp_servers.sofia]", "already registered", "would register sofia MCP server", codexMCPBlock, guard, check)
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
	if res.Check {
		fmt.Fprintln(w, "# dry run — nothing written")
	}
	fmt.Fprintf(w, "# claude on machine: %s\n", yesNo(res.ClaudeOnMachine))
	fmt.Fprintf(w, "# claude in project: %s\n", yesNo(res.ClaudeInProject))
	fmt.Fprintf(w, "# codex on machine: %s\n", yesNo(res.CodexOnMachine))
	fmt.Fprintf(w, "items[%d]{item,status,detail}:\n", len(res.Items))
	for _, it := range res.Items {
		fmt.Fprintf(w, "%s%s,%s,%s\n", toon.Indent, it.Name, it.Status, toon.Scalar(it.Detail))
	}
}

func renderMarkdown(w io.Writer, res *Result) {
	if res.Check {
		fmt.Fprintln(w, "# dry run — nothing written")
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "**claude on machine:** %s  \n**claude in project:** %s  \n**codex on machine:** %s\n\n",
		yesNo(res.ClaudeOnMachine), yesNo(res.ClaudeInProject), yesNo(res.CodexOnMachine))
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
