// Package dedup implements a per-session dedup stub for token-heavy sofia
// tools: a repeated, byte-identical call within a short window of the same
// session returns a one-line stub ("already returned in call #N; --force to
// repeat") instead of paying for the full output again. This is the
// self-teaching-stdout counterpart to internal/common/hook's Read guard —
// hook stops the FIRST wasteful full read, dedup stops a SECOND identical
// tool call from re-paying for output the agent already has in context.
//
// Mirrors hook's State/gc/sanitize pattern (internal/common/hook): one
// append-only JSONL file per session, no locks. The difference is the key —
// hook keys on a bare file path, dedup keys on the whole call (tool +
// normalized arguments), since the concern here is a repeated QUERY, not a
// repeated read of one file.
//
// Currently wired into `sf code` only (see the H-B build plan); the package
// itself is generic, and a second caller (grep, changed) is a follow-up if
// telemetry shows the same repeat rate there.
package dedup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
)

// DefaultWindow is the dedup window used when SOFIA_DEDUP_WINDOW is unset or
// unparseable.
const DefaultWindow = 180 * time.Second

// Window reads SOFIA_DEDUP_WINDOW (whole seconds); 0 disables dedup
// entirely; unset or unparseable falls back to DefaultWindow. Same Sscanf
// idiom as code.rawBelow — 0 is meaningful here too (the off switch), so
// only negatives are rejected.
func Window() time.Duration {
	var n int64
	if _, err := fmt.Sscanf(os.Getenv("SOFIA_DEDUP_WINDOW"), "%d", &n); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return DefaultWindow
}

// Key returns a stable, order-insensitive identifier for "the same call":
// tool plus its normalized arguments, reusing calllog's existing Fingerprint
// so dedup groups calls exactly the way history already does. Callers build
// parts from whatever makes two invocations equivalent (e.g. code.keyParts).
func Key(tool string, parts ...string) string {
	return calllog.Fingerprint(append([]string{"tool=" + tool}, parts...))
}

// Hit describes a dedup match: the earlier call's ordinal within the
// session's log, how long ago it ran, and its (original, full-output) token
// cost — carried through to the stub's footer so the reported saving is
// real, not the stub line's own trivial size.
type Hit struct {
	N   int
	Age time.Duration
	Tok int64
}

// record is one JSONL line in a session's dedup file:
//
//	{"fp":"a1b2c3d4e5f60718","n":3,"ts":1751791234,"tok":412}
//
// fp is Key's output, n the 1-based ordinal of the call within the session's
// file, ts a Unix-second timestamp, tok the token cost of the ORIGINAL full
// output. A stub record repeats the original's n/tok with a fresh ts (see
// Guard.CommitStub) — the window slides, and a repeat of a repeat still
// points at the real call.
type record struct {
	FP  string `json:"fp"`
	N   int    `json:"n"`
	TS  int64  `json:"ts"`
	Tok int64  `json:"tok"`
}

// Guard is the per-call handle Begin returns. Call Hit once; if it's non-nil,
// emit the stub and call CommitStub; otherwise produce the full output and
// call CommitFull on every successful path (never on a failed one — a failed
// call must not poison a retry with a dedup stub).
type Guard struct {
	force     bool
	disabled  bool
	key       string
	path      string
	lineCount int
	hit       *Hit
}

// beginDisabled reports whether dedup must not engage for this call: no
// session id to key on, the window switched off, or — this is the
// load-bearing one — a `go test` binary running without SOFIA_DEDUP_WINDOW
// set explicitly. A `go test` run inside a Claude Code session inherits
// CLAUDE_CODE_SESSION_ID from the outer process, so without this guard dedup
// would activate mid-suite and TestFooterDeterministic (internal/common/code,
// which calls Run twice and demands byte-identical output) would see its
// second call stubbed. Tests that want to exercise dedup set
// SOFIA_DEDUP_WINDOW explicitly, which lifts this guard.
func beginDisabled() bool {
	if calllog.SessionID() == "" {
		return true
	}
	if Window() == 0 {
		return true
	}
	if calllog.UnderTest() && os.Getenv("SOFIA_DEDUP_WINDOW") == "" {
		return true
	}
	return false
}

// Begin opens a dedup check for one call: tool names the caller (e.g.
// "code"), force bypasses the check (the call still records itself, so a
// later non-force call sees it as the new "original"), and keyParts are the
// caller-specific arguments that define "the same call" (see Key).
func Begin(tool string, force bool, keyParts ...string) *Guard {
	g := &Guard{force: force}
	if beginDisabled() {
		g.disabled = true
		return g
	}
	g.key = Key(tool, keyParts...)
	g.path = statePath(calllog.SessionID())
	g.lineCount, g.hit = scanState(g.path, g.key, Window())
	return g
}

// Hit reports the dedup match found at Begin, or nil when there wasn't
// one — including when force was set: the scan still ran (so CommitFull
// knows the right ordinal), but the caller must not stub a forced call.
func (g *Guard) Hit() *Hit {
	if g.disabled || g.force {
		return nil
	}
	return g.hit
}

// CommitFull records that this call produced fresh, full output: tok is its
// token cost, filed under the next ordinal in the session's log. Call this
// only on a successful full-output path.
func (g *Guard) CommitFull(tok int64) {
	if g.disabled {
		return
	}
	g.append(record{FP: g.key, N: g.lineCount + 1, TS: time.Now().Unix(), Tok: tok})
}

// CommitStub records that a stub was emitted for a dedup hit, sliding the
// window forward: the new entry repeats the ORIGINAL call's ordinal and
// token cost with a fresh timestamp, so a stub-of-a-stub still points at the
// real call.
func (g *Guard) CommitStub() {
	if g.disabled || g.hit == nil {
		return
	}
	g.append(record{FP: g.key, N: g.hit.N, TS: time.Now().Unix(), Tok: g.hit.Tok})
}

// append writes one record to this session's log, garbage-collecting stale
// session files first (see gc). Fail-open: any IO error here is swallowed —
// dedup bookkeeping must never break the call it's piggybacking on.
func (g *Guard) append(r record) {
	dir := filepath.Dir(g.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	gc(dir)
	f, err := os.OpenFile(g.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}

// scanState reads a session's dedup file and reports the total number of
// valid records seen (so CommitFull can pick the next ordinal) plus the most
// recent record matching key, if it's still within window. Malformed lines
// are skipped, same as calllog.Read; a missing file is not an error (no
// history yet) — fail-open throughout, any problem here just means "no hit".
func scanState(path, key string, window time.Duration) (lineCount int, hit *Hit) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = f.Close() }()
	windowSec := int64(window / time.Second)
	now := time.Now().Unix()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var r record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		lineCount++
		if r.FP != key {
			continue
		}
		age := now - r.TS
		if age >= 0 && age < windowSec {
			hit = &Hit{N: r.N, Age: time.Duration(age) * time.Second, Tok: r.Tok}
		} else {
			hit = nil // out of window (or clock skew) — a later match can still revive it
		}
	}
	return lineCount, hit
}

// stubJSON is the --format json stub payload — one line, no envelope, since
// the caller's normal JSON output is itself a single encoded value.
type stubJSON struct {
	Dedup     bool   `json:"dedup"`
	DupOfCall int    `json:"dup_of_call"`
	Age       string `json:"age"`
	Hint      string `json:"hint"`
}

// WriteStub writes the one-line dedup stub for hit in the given output
// format (json gets the structured form, everything else — toon, md, plain
// slice output — gets the text line). Callers still print the normal cost
// footer after this (see emit.Footer): SOFIA_FOOTER=off silences that footer
// but not the stub line itself, which is the payload, not decoration.
func WriteStub(w io.Writer, format string, h *Hit) {
	age := formatAge(h.Age)
	if format == "json" {
		b, _ := json.Marshal(stubJSON{
			Dedup:     true,
			DupOfCall: h.N,
			Age:       age,
			Hint: fmt.Sprintf("already returned in call #%d (~%s ago); rerun with --force (CLI) or force:true (MCP) to repeat",
				h.N, age),
		})
		fmt.Fprintf(w, "%s\n", b)
		return
	}
	fmt.Fprintf(w, "# sf: already returned in call #%d (~%s ago); rerun with --force to repeat\n", h.N, age)
}

// formatAge renders a dedup hit's age the way the stub text does: seconds
// under a minute, else minutes rounded to the nearest (ties round up).
func formatAge(d time.Duration) string {
	if d < 60*time.Second {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return fmt.Sprintf("%dm", int((d+30*time.Second)/time.Minute))
}

// dedupDir is the directory dedup state files live under, a sibling of the
// shared call log ($state/dedup/ next to $state/calls.jsonl) so both honour
// the same SOFIA_LOG_DIR override.
func dedupDir() string {
	return filepath.Join(filepath.Dir(calllog.Path()), "dedup")
}

// statePath is the per-session dedup file: one JSONL file per session id,
// same sanitize+layout as hook.State.
func statePath(sid string) string {
	return filepath.Join(dedupDir(), sanitize(sid)+".jsonl")
}

// gc removes session files older than 48h — verbatim the same policy as
// hook.State.gc, run opportunistically on every commit rather than on a
// schedule.
func gc(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-48 * time.Hour)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// sanitize is copied verbatim from hook.sanitize (unexported there on
// purpose; no shared internal home for a five-line rune filter).
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}
