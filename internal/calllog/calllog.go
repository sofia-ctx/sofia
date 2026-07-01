// Package calllog records every invocation of a sofia-repo tool to a
// shared JSON-lines log so calls can be analysed and optimised later.
//
// Log location follows the XDG Base Directory spec: by default
// `$XDG_STATE_HOME/sofia/calls.jsonl` (→ `~/.local/state/sofia/calls.jsonl`).
// The location can be overridden with the SOFIA_LOG_DIR env var, which is
// what `sf history` advertises so developers can point the log at the
// repo's own working tree if they prefer.
package calllog

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sofia-ctx/sofia/internal/tokens"
)

type Entry struct {
	Timestamp    string         `json:"ts"`
	Tool         string         `json:"tool"`
	Source       string         `json:"source,omitempty"` // agent | manual | test (who invoked it)
	SessionID    string         `json:"sid,omitempty"`    // Claude Code session id (joins with `sf cc`)
	Tag          string         `json:"tag,omitempty"`    // project the call belongs to
	Args         []string       `json:"args"`
	Fingerprint  string         `json:"fp,omitempty"` // sorted+joined args for grouping equivalent invocations
	DurationMs   int64          `json:"dur_ms"`
	ExitCode     int            `json:"exit"`
	Error        string         `json:"err,omitempty"`
	OutputBytes  int64          `json:"out_bytes,omitempty"`  // size of stdout payload, in bytes
	OutputTokens int64          `json:"out_tokens,omitempty"` // approximate LLM tokens (see internal/tokens)
	Summary      map[string]any `json:"summary,omitempty"`
}

// Path returns the file used for the shared JSONL log. Resolution order:
//
//  1. $SOFIA_LOG_DIR/calls.jsonl  — explicit override (CI, devs that want
//     in-tree logs).
//  2. $XDG_STATE_HOME/sofia/calls.jsonl — XDG-conformant default.
//  3. ~/.local/state/sofia/calls.jsonl — XDG fallback when the env var is
//     unset (the spec's defined default).
//  4. ./calls.jsonl — final fallback if we can't even discover HOME.
//
// All sofia tools (master `sf` binary and project-specific binaries) hit
// the same path so history aggregates the full picture.
func Path() string {
	if dir := os.Getenv("SOFIA_LOG_DIR"); dir != "" {
		return filepath.Join(dir, "calls.jsonl")
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "sofia", "calls.jsonl")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "sofia", "calls.jsonl")
	}
	return "calls.jsonl"
}

// underTest reports whether the process is a `go test` binary (its name
// ends in ".test"). Used to keep test runs from polluting the real call log.
func underTest() bool {
	return len(os.Args) > 0 && strings.HasSuffix(os.Args[0], ".test")
}

// detectSource classifies who triggered the invocation, so `sf history` can
// separate real agent traffic from a developer poking the CLI by hand or a
// test run. Resolution order: SOFIA_SOURCE override → a test binary → Claude
// Code (authoritative CLAUDECODE=1 in the Bash-tool env) → an interactive
// terminal on stdout → "manual" → "agent". The CLAUDECODE check is more
// reliable than term.IsTerminal: it catches agent calls even when stdout
// happens to look like a terminal, and is set by Claude regardless of how the
// session was launched.
func detectSource() string {
	if s := os.Getenv("SOFIA_SOURCE"); s != "" {
		return s
	}
	if underTest() {
		return "test"
	}
	if os.Getenv("CLAUDECODE") == "1" {
		return "agent"
	}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return "manual"
	}
	return "agent"
}

// firstNonEmpty returns the first non-empty string, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// sessionID identifies the session a call belongs to so history can be sliced
// per session and joined with the Claude transcript that `sf cc` reads.
// CLAUDE_CODE_SESSION_ID is injected natively by Claude Code into every
// Bash-tool environment and equals the transcript filename
// (~/.claude/projects/<slug>/<id>.jsonl); it tracks the live session across
// /clear and --resume because it's read per call. SOFIA_SESSION_ID is an
// escape hatch for non-Claude automation. Empty for hand-run terminal calls.
func sessionID() string {
	return firstNonEmpty(os.Getenv("CLAUDE_CODE_SESSION_ID"), os.Getenv("SOFIA_SESSION_ID"))
}

// projectTag names the project a call belongs to. SOFIA_TAG (stamped by the
// `sf claude` launcher with the authoritative, disambiguated project name)
// wins; otherwise it's derived from the working directory so manual calls and
// sessions started outside the launcher still attribute correctly.
func projectTag() string {
	return firstNonEmpty(os.Getenv("SOFIA_TAG"), deriveProjectFromCwd())
}

// deriveProjectFromCwd returns the basename of the nearest ancestor holding a
// .git entry (the repo root), else the basename of the cwd. It walks the tree
// rather than forking `git`, keeping the hot path allocation-light and free of
// an external-binary dependency. A bare repo or missing cwd yields "".
func deriveProjectFromCwd() string {
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		return ""
	}
	for dir := cwd; ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return filepath.Base(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Base(cwd)
}

func appendEntry(e Entry) error {
	// Don't let `go test` write to the real shared log. Tests that genuinely
	// exercise logging set SOFIA_LOG_DIR to a temp dir (see calllog_test.go),
	// so honour an explicit override even under test.
	if underTest() && os.Getenv("SOFIA_LOG_DIR") == "" {
		return nil
	}
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return enc.Encode(e)
}

// Read loads every entry from the shared call log, in append order (oldest
// first). A missing log is not an error — it returns nil. Malformed lines are
// skipped so a single bad write can't break a reader. Used by tools that need
// the raw log (e.g. `sf gripe`'s list view, `sf doctor`'s pending-feedback
// count); `sf history` keeps its own filtering reader on top of the same file.
func Read() ([]Entry, error) {
	f, err := os.Open(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var out []Entry
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Tracker measures an invocation. Pair Start(...) with .Finish(err, summary).
type Tracker struct {
	start time.Time
	done  bool
	entry Entry
}

func Start(tool string, args []string) *Tracker {
	cp := append([]string(nil), args...)
	t := &Tracker{
		start: time.Now(),
		entry: Entry{
			Tool:        tool,
			Source:      detectSource(),
			SessionID:   sessionID(),
			Tag:         projectTag(),
			Args:        cp,
			Fingerprint: Fingerprint(cp),
		},
	}
	pending = t
	return t
}

// SetSummary mutates the pending summary map; safe to call multiple times.
func (t *Tracker) SetSummary(s map[string]any) {
	t.entry.Summary = s
}

// SetOutputBytes records the size of the user-facing payload (typically
// stdout). History can use this to spot heavy outputs that should be
// trimmed or capped.
func (t *Tracker) SetOutputBytes(n int64) {
	t.entry.OutputBytes = n
}

// SetOutputTokens records the approximate LLM token count for the
// user-facing payload (see package `internal/tokens`).
func (t *Tracker) SetOutputTokens(n int64) {
	t.entry.OutputTokens = n
}

// RecordOutput copies both byte and token accumulators from a Counter
// in one call — the typical Run() epilogue.
func (t *Tracker) RecordOutput(c *Counter) {
	t.entry.OutputBytes = c.Count
	t.entry.OutputTokens = c.Tokens
}

// Counter wraps w to track both the byte count and an approximate LLM
// token count of everything written through it. Token cost is estimated
// per chunk via tokens.Estimate; chunk-boundary inaccuracy in the rare
// case of a Write splitting a multi-byte rune is bounded by a single
// token and irrelevant for analytics.
type Counter struct {
	W      io.Writer
	Count  int64
	Tokens int64
}

func (c *Counter) Write(p []byte) (int, error) {
	n, err := c.W.Write(p)
	if n > 0 {
		c.Count += int64(n)
		c.Tokens += tokens.Estimate(string(p[:n]))
	}
	return n, err
}

// Fingerprint returns a stable, sort-independent identifier for a set of
// argument strings. Equivalent invocations (e.g. "A B" and "B A") share
// a fingerprint, so history can collapse them when surfacing repeated
// queries. The form is a 16-hex-char SHA-256 prefix — short enough to
// scan, long enough to avoid collisions across normal usage.
func Fingerprint(args []string) string {
	cp := append([]string(nil), args...)
	sort.Strings(cp)
	sum := sha256.Sum256([]byte(strings.Join(cp, "\x1f")))
	return hex.EncodeToString(sum[:8])
}

// Finish writes the log entry. Idempotent — a second call is a no-op, so a
// tool's own Finish and the central Finalize can't double-log. Errors
// writing the log are swallowed; we don't want logging to break the user's
// tool invocation.
func (t *Tracker) Finish(err error) {
	if t.done {
		return
	}
	t.done = true
	t.entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	t.entry.DurationMs = time.Since(t.start).Milliseconds()
	if err != nil {
		t.entry.Error = err.Error()
		t.entry.ExitCode = 1
	}
	_ = appendEntry(t.entry)
}

// Track wraps fn with logging in one call. The caller can update summary
// fields via the returned tracker before fn returns.
func Track(tool string, args []string, fn func(t *Tracker) error) error {
	t := Start(tool, args)
	err := fn(t)
	t.Finish(err)
	return err
}

// pending holds the tracker for the in-flight invocation. A sofia binary
// runs exactly one command per process, so a package-level pointer is
// enough for Finalize to tell whether a tool already logged itself.
var pending *Tracker

// skip reports whether a tool name must never reach the log via the central
// fallback: shell-completion plumbing (not token work), the history viewer
// (it reads the very log it would pollute), and bare command groups that
// only print help. Everything that actually does token work — code, grep,
// changed, composer.*, packagist.*, github.*, cc.* — is logged.
//
// `gripe` is special: its record-mode self-logs via Start/Finish (which ignore
// skip), so the gripe still lands; what skip suppresses is the *bare* `sf gripe`
// list view, which is a reader like `history` and must not write a junk entry.
func skip(tool string) bool {
	if tool == "" || tool == "sf" {
		return true
	}
	head := tool
	if i := strings.IndexByte(tool, '.'); i >= 0 {
		head = tool[:i]
	}
	switch head {
	case "history", "completion", "__complete", "help", "hook":
		return true
	}
	switch tool { // bare groups (print help) + readers that must not self-log
	case "cc", "gripe":
		return true
	}
	return false
}

// toolName turns a cobra command path ("sf composer show") into the dotted
// log name ("composer.show"). The leading binary segment is dropped. Returns
// "" when the path is just the binary (a standalone binary whose root *is*
// the tool, or `sf` with no subcommand) — callers pass a fallback for that.
func toolName(path string) string {
	f := strings.Fields(path)
	if len(f) <= 1 {
		return ""
	}
	return strings.Join(f[1:], ".")
}

// Finalize guarantees exactly one log entry per invocation. Tools log
// themselves via Start/Finish; this fills the gaps those miss — chiefly
// Cobra arg-validation errors that abort before a tool's RunE ever runs.
// Idempotent and safe: a no-op when the tool already finished.
func Finalize(fallbackTool string, err error) {
	if pending != nil {
		if !pending.done && !skip(pending.entry.Tool) {
			pending.Finish(err)
		}
		return
	}
	if skip(fallbackTool) {
		return
	}
	Start(fallbackTool, nil).Finish(err)
}

// Run executes root and logs the invocation centrally, then returns the
// command error to the caller (which prints/exits). fallbackTool names the
// tool when the command path can't — standalone binaries whose root *is*
// the tool. A tool that actually ran will have set its own canonical name,
// which takes precedence.
func Run(root *cobra.Command, fallbackTool string) error {
	executed, err := root.ExecuteC()
	name := fallbackTool
	if executed != nil {
		if t := toolName(executed.CommandPath()); t != "" {
			name = t
		}
	}
	Finalize(name, err)
	return err
}
