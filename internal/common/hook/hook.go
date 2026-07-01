// Package hook implements Claude Code hook endpoints (`sf hook pre`).
//
// PreToolUse guards the Read channel — the biggest unintercepted token
// producer (≈1M result-tokens/week vs ≈270k lifetime for `sf code`): a full
// Read/cat of a big source file lands in the context window once and is then
// cache-read on every following turn. The hook nudges the agent toward the
// structural path (`sf code <file>` → `sf code <file> <Symbol>`) instead.
//
// Modes (SOFIA_HOOK_MODE):
//
//	off     — do nothing;
//	suggest — allow the call, attach an advisory note (additionalContext);
//	nudge   — deny the FIRST full read of a given file per session with an
//	          actionable reason; an identical repeated call passes (so an
//	          agent that truly needs the body — e.g. before Edit — recovers
//	          in one turn). Default.
//	strict  — always deny full reads of big source files.
//
// Fail-open by design: any parse/IO problem inside the hook results in a
// silent allow — the hook must never break a session.
package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Input is the subset of the PreToolUse stdin payload the hook cares about.
type Input struct {
	SessionID string          `json:"session_id"`
	CWD       string          `json:"cwd"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

type readInput struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

type bashInput struct {
	Command string `json:"command"`
}

// Actions a decision can take. Empty action means "allow silently".
const (
	ActionSuggest = "suggest"
	ActionDeny    = "deny"
)

// Decision is the outcome of Decide for one tool call.
type Decision struct {
	Action string // "", ActionSuggest or ActionDeny
	Path   string // resolved target file
	Bytes  int64  // its size
	Reason string // text fed to the model (deny reason or advisory note)
}

// Defaults; both have env overrides (see Mode/MinBytes).
const (
	defaultMode     = "nudge"
	defaultMinBytes = 4096 // ≈1k tokens by the ASCII/4 heuristic
)

// Mode reads SOFIA_HOOK_MODE, falling back to nudge.
func Mode() string {
	switch m := os.Getenv("SOFIA_HOOK_MODE"); m {
	case "off", "suggest", "nudge", "strict":
		return m
	}
	return defaultMode
}

// MinBytes reads SOFIA_HOOK_MIN_BYTES, falling back to 4096.
func MinBytes() int64 {
	var n int64
	if _, err := fmt.Sscanf(os.Getenv("SOFIA_HOOK_MIN_BYTES"), "%d", &n); err == nil && n > 0 {
		return n
	}
	return defaultMinBytes
}

// structuralExt mirrors what `sf code` dispatches on
// (internal/common/code/code.go) — nudging makes sense only where the
// structural reader actually works.
func structuralExt(path string) bool {
	for _, ext := range []string{".go", ".php", ".ts", ".tsx", ".vue"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// Decide inspects one PreToolUse payload and picks an action. st may be nil
// (no dedup state — every big read nudges).
func Decide(in Input, st *State, mode string, minBytes int64) Decision {
	if mode == "off" {
		return Decision{}
	}
	path := targetPath(in)
	if path == "" || !structuralExt(path) {
		return Decision{}
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || fi.Size() < minBytes {
		return Decision{}
	}
	if mode == "nudge" && st != nil && st.Seen(in.SessionID, path) {
		return Decision{}
	}
	d := Decision{Path: path, Bytes: fi.Size()}
	kb := float64(fi.Size()) / 1024
	tokens := fi.Size() / 4
	switch mode {
	case "suggest":
		d.Action = ActionSuggest
		d.Reason = fmt.Sprintf(
			"sf-context: %s — %.1fK (≈%d tok. read in full); cheaper next time: `sf code %s` (structure) or `sf code %s <Symbol>` (one body).",
			path, kb, tokens, path, path)
	case "strict":
		d.Action = ActionDeny
		d.Reason = fmt.Sprintf(
			"sf-context: %s — %.1fK (≈%d tok. read in full). Use `sf code %s` (structure, no bodies), `sf code %s <Symbol>` (one symbol's body), or Read with offset/limit. (SOFIA_HOOK_MODE=strict: full Read disabled.)",
			path, kb, tokens, path, path)
	default: // nudge
		d.Action = ActionDeny
		d.Reason = fmt.Sprintf(
			"sf-context: %s — %.1fK (≈%d tok. read in full). First try: `sf code %s` (structure, no bodies) or `sf code %s <Symbol>` (one symbol's body). If you genuinely need the whole file (e.g. before an Edit) — repeat this same call: it goes through the second time.",
			path, kb, tokens, path, path)
		if st != nil {
			_ = st.Mark(in.SessionID, path)
		}
	}
	return d
}

// targetPath extracts the file a tool call is about to read in full.
// Returns "" when the call is fine as-is (targeted read, piped cat, not a
// file read at all).
func targetPath(in Input) string {
	switch in.ToolName {
	case "Read":
		var r readInput
		if json.Unmarshal(in.ToolInput, &r) != nil {
			return ""
		}
		if r.Offset > 0 || r.Limit > 0 { // targeted read — already economical
			return ""
		}
		return absPath(r.FilePath, in.CWD)
	case "Bash":
		var b bashInput
		if json.Unmarshal(in.ToolInput, &b) != nil {
			return ""
		}
		return parseCat(b.Command, in.CWD)
	}
	return ""
}

// parseCat recognises a bare full-file dump: `cat <one-file>` with no shell
// composition. Anything piped, redirected, multi-file or flagged is left
// alone — `cat f | head -50` is already self-limited.
func parseCat(cmd, cwd string) string {
	if strings.ContainsAny(cmd, "|&;<>$`(){}\n*?") {
		return ""
	}
	f := strings.Fields(cmd)
	if len(f) != 2 || filepath.Base(f[0]) != "cat" {
		return ""
	}
	p := strings.Trim(f[1], `'"`)
	if p == "" || strings.HasPrefix(p, "-") {
		return ""
	}
	return absPath(p, cwd)
}

func absPath(p, cwd string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(cwd, p)
}

// State remembers which files were already nudged in a session, so the
// second identical call passes. One small file per session id under
// <state>/hook/; entries older than two days are pruned opportunistically.
type State struct{ dir string }

// NewState returns a State rooted at dir (created lazily on Mark).
func NewState(dir string) *State { return &State{dir: dir} }

func (s *State) file(sid string) string {
	if sid == "" {
		sid = "nosid"
	}
	return filepath.Join(s.dir, sanitize(sid)+".seen")
}

// Seen reports whether path was already nudged for this session.
func (s *State) Seen(sid, path string) bool {
	b, err := os.ReadFile(s.file(sid))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if line == path {
			return true
		}
	}
	return false
}

// Mark records path as nudged for this session and prunes stale sessions.
func (s *State) Mark(sid, path string) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	s.gc()
	f, err := os.OpenFile(s.file(sid), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(path + "\n")
	return err
}

func (s *State) gc() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-48 * time.Hour)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}
