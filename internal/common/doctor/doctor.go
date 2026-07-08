// Package doctor implements `sf doctor` — a one-call health check of the local
// sf install. Its anchor check is binary staleness: whether bin/sf is older
// than git HEAD (the "fixed in git but never rebuilt" trap, which silently
// makes the agent run outdated tools). It also checks PATH resolution and
// shell completions.
package doctor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia"
	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/gitexec"
	"github.com/sofia-ctx/sofia/internal/pack"
	"github.com/sofia-ctx/sofia/internal/version"
)

// repoRoot locates the dev checkout this binary was built from, by walking up
// from the running executable's path looking for the bin/ directory `make
// build` populates. $SOFIA_ROOT overrides it directly. Returns an error for
// any install that isn't a dev/bin checkout (e.g. `go install`), which is the
// normal case outside this repo's own working tree.
func repoRoot() (string, error) {
	if v := os.Getenv("SOFIA_ROOT"); v != "" {
		return v, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	for i := 0; i < 8; i++ {
		if filepath.Base(dir) == "bin" {
			return filepath.Dir(dir), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("cannot locate sofia repo root; set $SOFIA_ROOT")
}

// Check statuses.
const (
	statusOK   = "ok"
	statusWarn = "warn"
	statusFail = "fail"
)

// Options carries flag state.
type Options struct {
	Format string
}

// Check is one health probe.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
}

// Result is the full report.
type Result struct {
	Checks []Check `json:"checks"`
}

// Run collects the checks, renders them, logs the call, and returns a non-nil
// error (→ exit 1) when any check FAILs so doctor can gate scripts.
func Run(opts Options, w io.Writer) error {
	tracker := calllog.Start("doctor", []string{"--format=" + opts.Format})
	res, _ := Collect(opts)

	fails := 0
	for _, c := range res.Checks {
		if c.Status == statusFail {
			fails++
		}
	}
	tracker.SetSummary(map[string]any{"checks": len(res.Checks), "fail": fails})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch opts.Format {
	case "", "toon":
		renderTOON(cw, res)
	case "md":
		renderMarkdown(cw, res)
	case "json":
		renderErr = renderJSON(cw, res)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", opts.Format)
	}
	tracker.RecordOutput(cw)

	if renderErr == nil && fails > 0 {
		renderErr = fmt.Errorf("%d check(s) failed; see output above", fails)
	}
	tracker.Finish(renderErr)
	return renderErr
}

// Collect runs every check in order. It never errors (each check degrades to a
// warn/ok), so the report is always renderable.
func Collect(_ Options) (*Result, error) {
	return &Result{Checks: []Check{
		checkVersion(),
		checkStaleness(),
		checkPath(),
		checkCompletions(),
		checkHook(),
		checkSkill(),
		checkCalllog(),
		checkGripes(),
		checkMCP(),
		checkCodex(),
		checkPacks(),
	}}, nil
}

// checkHook verifies the global PreToolUse hook (`sf hook pre`) is wired in
// ~/.claude/settings.json — without it the Read channel runs unguarded.
func checkHook() Check {
	c := Check{Name: "hook"}
	home, err := os.UserHomeDir()
	if err != nil {
		c.Status = statusWarn
		c.Detail = "no $HOME — can't locate Claude Code settings"
		return c
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(b), "sf hook pre") {
		c.Status = statusWarn
		c.Detail = "sf's PreToolUse hook isn't configured in ~/.claude/settings.json (see README «sf hook»)"
		return c
	}
	c.Status = statusOK
	c.Detail = "PreToolUse → sf hook pre (global)"
	return c
}

// checkSkill compares the installed sf-context skill against the repo copy —
// same deploy-gap class as binary staleness: edited in git, forgot
// `make install`.
func checkSkill() Check {
	c := Check{Name: "skill"}
	home, err := os.UserHomeDir()
	if err != nil {
		c.Status = statusWarn
		c.Detail = "no $HOME — can't locate ~/.claude/skills"
		return c
	}
	installed, ierr := os.ReadFile(filepath.Join(home, ".claude", "skills", "sf-context", "SKILL.md"))
	if ierr != nil {
		c.Status = statusWarn
		c.Detail = "skill sf-context isn't installed — `make install`"
		return c
	}
	root, err := repoRoot()
	if err != nil {
		// No dev checkout to diff against (e.g. a Homebrew install) — fall
		// back to the copy baked into the binary itself.
		c.Status, c.Detail = compareSkill(installed, sofia.SkillMD)
		return c
	}
	repo, rerr := os.ReadFile(filepath.Join(root, "skills", "sf-context", "SKILL.md"))
	if rerr != nil {
		c.Status = statusWarn
		c.Detail = "skills/sf-context/SKILL.md is missing from the repo"
		return c
	}
	if !bytes.Equal(installed, repo) {
		c.Status = statusWarn
		c.Detail = "skill sf-context is stale relative to the repo — `make install`"
		return c
	}
	c.Status = statusOK
	c.Detail = "skill sf-context is installed and up to date"
	return c
}

// compareSkill is the pure comparison behind the embedded-fallback branch of
// checkSkill, split out so it's testable without faking repoRoot()'s
// filesystem walk.
func compareSkill(installed, embedded []byte) (status, detail string) {
	if bytes.Equal(installed, embedded) {
		return statusOK, "skill sf-context is installed and up to date"
	}
	return statusWarn, "skill sf-context is stale relative to the bundled copy — `sf init --force`"
}

// checkGripes surfaces unaddressed agent complaints about sf (see `sf gripe`).
// It counts agent-sourced gripes recorded since bin/sf was last built — i.e.
// feedback the current binary doesn't yet answer — so the loop self-closes: the
// author already runs doctor after `make install` and gets nudged to read them.
// Outside a dev/bin install (no bin/sf mtime) it counts all agent gripes.
func checkGripes() Check {
	c := Check{Name: "gripes", Status: statusOK}
	entries, err := calllog.Read()
	if err != nil {
		c.Status = statusWarn
		c.Detail = "log unavailable: " + err.Error()
		return c
	}

	var cutoff time.Time
	if root, err := repoRoot(); err == nil {
		if st, err := os.Stat(filepath.Join(root, "bin", "sf")); err == nil {
			cutoff = st.ModTime()
		}
	}

	n := 0
	for _, e := range entries {
		if e.Tool != "gripe" {
			continue
		}
		source := e.Source
		if source == "" {
			source = "agent"
		}
		if source != "agent" {
			continue
		}
		if !cutoff.IsZero() {
			t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
			if err != nil || t.Before(cutoff) {
				continue
			}
		}
		n++
	}

	if n == 0 {
		c.Detail = "no new agent gripes about sf"
		return c
	}
	c.Status = statusWarn
	c.Detail = fmt.Sprintf("%d agent gripe(s) about sf since the build — check `sf gripe`", n)
	return c
}

// checkVersion reports the running sf build's version string — informational,
// and a quick way to see whether -ldflags version injection actually ran
// (see scripts/build.sh). Pairs with checkStaleness below: that one says
// whether the binary is behind HEAD, this one says which version it is.
func checkVersion() Check {
	c := Check{Name: "version", Status: statusOK}
	if version.Version == "dev" {
		c.Detail = "dev (built without -ldflags version; see scripts/build.sh)"
		return c
	}
	c.Detail = version.Version
	return c
}

// checkStaleness compares the built bin/sf against git HEAD. This is the whole
// reason doctor exists: a fix can be merged yet bin/sf left unrebuilt, so the
// agent keeps running the old binary.
func checkStaleness() Check {
	c := Check{Name: "staleness"}
	root, err := repoRoot()
	if err != nil {
		c.Status = statusOK
		c.Detail = "not a dev/bin install — staleness check doesn't apply"
		return c
	}
	binPath := filepath.Join(root, "bin", "sf")
	st, err := os.Stat(binPath)
	if err != nil {
		c.Status = statusWarn
		c.Detail = fmt.Sprintf("bin/sf not found (%s); run `make build`", binPath)
		return c
	}
	headTime, herr := gitHeadTime(root)
	if herr != nil {
		c.Status = statusWarn
		c.Detail = fmt.Sprintf("git HEAD unavailable: %v", herr)
		return c
	}
	c.Status, c.Detail = classifyStaleness(st.ModTime(), headTime, gitDirtyGo(root))
	return c
}

// classifyStaleness is the pure decision: a build older than the latest commit
// is stale (fail); otherwise uncommitted *.go is a soft warning; else ok.
func classifyStaleness(binMtime, headTime time.Time, dirtyGo bool) (status, detail string) {
	const f = "2006-01-02 15:04"
	if headTime.After(binMtime) {
		return statusFail, fmt.Sprintf("bin/sf was built %s, but HEAD was committed %s — run `make install`",
			binMtime.Format(f), headTime.Format(f))
	}
	if dirtyGo {
		return statusWarn, "unbuilt *.go changes in the working tree — `make install` to test the fresh binary"
	}
	return statusOK, fmt.Sprintf("bin/sf is current (built %s)", binMtime.Format(f))
}

// checkPath verifies the `sf` resolved on $PATH is the running binary, so a
// stale copy earlier in PATH doesn't shadow a fresh build.
func checkPath() Check {
	c := Check{Name: "path"}
	exe, err := os.Executable()
	if err != nil {
		c.Status = statusWarn
		c.Detail = fmt.Sprintf("can't determine the binary's path: %v", err)
		return c
	}
	exeReal := resolve(exe)
	lp, err := exec.LookPath("sf")
	if err != nil {
		c.Status = statusWarn
		c.Detail = fmt.Sprintf("`sf` not found on $PATH; add %s to PATH", filepath.Dir(exe))
		return c
	}
	if resolve(lp) == exeReal {
		c.Status = statusOK
		c.Detail = "sf → " + exeReal
		return c
	}
	c.Status = statusWarn
	c.Detail = fmt.Sprintf("`sf` on PATH (%s) ≠ running binary (%s)", resolve(lp), exeReal)
	return c
}

// checkCompletions reports whether the shell-completion scripts `make install`
// writes are present in their standard locations.
func checkCompletions() Check {
	c := Check{Name: "completions"}
	home, err := os.UserHomeDir()
	if err != nil {
		c.Status = statusWarn
		c.Detail = "HOME is not set"
		return c
	}
	var missing []string
	if !fileExists(filepath.Join(home, ".config", "fish", "completions", "sf.fish")) {
		missing = append(missing, "fish")
	}
	if !fileExists(filepath.Join(home, ".local", "share", "bash-completion", "completions", "sf")) {
		missing = append(missing, "bash")
	}
	if len(missing) == 0 {
		c.Status = statusOK
		c.Detail = "fish+bash installed"
		return c
	}
	c.Status = statusWarn
	c.Detail = fmt.Sprintf("missing: %s; try: make install", strings.Join(missing, ","))
	return c
}

// checkCalllog reports the shared call-log path (informational).
func checkCalllog() Check {
	c := Check{Name: "calllog", Status: statusOK}
	p := calllog.Path()
	if fileExists(p) {
		c.Detail = p
	} else {
		c.Detail = p + " (not created yet)"
	}
	return c
}

// checkMCP reports whether the sofia MCP server is registered in the current
// directory's .mcp.json — the file `sf init` writes.
func checkMCP() Check {
	c := Check{Name: "mcp"}
	wd, err := os.Getwd()
	if err != nil {
		c.Status = statusWarn
		c.Detail = "can't determine the working directory"
		return c
	}
	b, err := os.ReadFile(filepath.Join(wd, ".mcp.json"))
	if err != nil {
		c.Status = statusWarn
		c.Detail = "no .mcp.json here — `sf init` registers the MCP server"
		return c
	}
	var doc struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if json.Unmarshal(b, &doc) != nil {
		// Malformed JSON — fall back to a raw substring check rather than
		// reporting nothing.
		if bytes.Contains(b, []byte("sofia")) {
			c.Status = statusOK
			c.Detail = "sofia MCP registered in ./.mcp.json"
			return c
		}
		c.Status = statusWarn
		c.Detail = "MCP configured here but sofia isn't registered — `sf init`"
		return c
	}
	if _, ok := doc.MCPServers["sofia"]; ok {
		c.Status = statusOK
		c.Detail = "sofia MCP registered in ./.mcp.json"
		return c
	}
	c.Status = statusWarn
	c.Detail = "MCP configured here but sofia isn't registered — `sf init`"
	return c
}

// codexDir mirrors initcmd.codexDir: $CODEX_HOME overrides, else ~/.codex.
// Reimplemented locally rather than imported — doctor has no other reason to
// depend on initcmd.
func codexDir() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".codex")
	}
	return ".codex"
}

// checkCodex reports whether Codex CLI is wired to sf: the PreToolUse hook
// and the MCP server, both appended to $CODEX_HOME/config.toml by `sf init`
// (see initcmd.codexHookStep/codexMCPStep). Codex not being installed at all
// is not a warning — it's simply not the agent in use here.
func checkCodex() Check {
	c := Check{Name: "codex"}
	dir := codexDir()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		c.Status = statusOK
		c.Detail = "Codex not detected"
		return c
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		c.Status = statusWarn
		c.Detail = "Codex detected but sf isn't wired — `sf init`"
		return c
	}
	hasHook := bytes.Contains(b, []byte("sf hook pre"))
	hasMCP := bytes.Contains(b, []byte("[mcp_servers.sofia]"))
	switch {
	case hasHook && hasMCP:
		c.Status = statusOK
		c.Detail = "Codex hook + MCP wired"
	case hasHook:
		c.Status = statusWarn
		c.Detail = "Codex: hook wired, MCP not — `sf init`"
	case hasMCP:
		c.Status = statusWarn
		c.Detail = "Codex: MCP wired, hook not — `sf init`"
	default:
		c.Status = statusWarn
		c.Detail = "Codex detected but sf isn't wired — `sf init`"
	}
	return c
}

// checkPacks surfaces drift in installed packs — files `sf pack install` put
// down that have since been hand-edited or deleted (see internal/pack).
// Machine-global like checkSkill's embedded fallback: it doesn't take a
// project, since a pack's receipt already records every project it touched.
func checkPacks() Check {
	c := Check{Name: "packs", Status: statusOK}
	statuses, err := pack.StatusAll()
	if err != nil {
		c.Status = statusWarn
		c.Detail = "pack status unavailable: " + err.Error()
		return c
	}
	if len(statuses) == 0 {
		c.Detail = "no packs installed"
		return c
	}
	var drifted []string
	for _, s := range statuses {
		if s.Modified == 0 && s.Missing == 0 {
			continue
		}
		var parts []string
		if s.Modified > 0 {
			parts = append(parts, fmt.Sprintf("%d modified", s.Modified))
		}
		if s.Missing > 0 {
			parts = append(parts, fmt.Sprintf("%d missing", s.Missing))
		}
		drifted = append(drifted, fmt.Sprintf("%s: %s", s.Name, strings.Join(parts, ", ")))
	}
	if len(drifted) == 0 {
		c.Detail = fmt.Sprintf("%d pack(s), no drift", len(statuses))
		return c
	}
	c.Status = statusWarn
	c.Detail = strings.Join(drifted, "; ")
	return c
}

// gitHeadTime returns the commit time of HEAD in root.
func gitHeadTime(root string) (time.Time, error) {
	out, err := gitexec.Run(root, "log", "-1", "--format=%ct")
	if err != nil {
		return time.Time{}, err
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse commit time %q: %w", out, err)
	}
	return time.Unix(sec, 0), nil
}

// gitDirtyGo reports whether the working tree has uncommitted *.go changes.
func gitDirtyGo(root string) bool {
	out, err := gitexec.Run(root, "status", "--porcelain")
	if err != nil {
		return false
	}
	return porcelainHasGo(out)
}

// porcelainHasGo scans `git status --porcelain` output for a touched *.go file.
func porcelainHasGo(out string) bool {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:]) // strip "XY "
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:] // rename: keep the new path
		}
		if strings.HasSuffix(path, ".go") {
			return true
		}
	}
	return false
}

// resolve follows symlinks, falling back to the input when that fails.
func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
