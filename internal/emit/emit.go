// Package emit enforces the core invariant of every token-saving sofia
// tool: a tool that stands in for a standard command (cat, grep, …) must
// never cost MORE tokens than that command would. If the compact rendering
// is not actually smaller — or could not be produced at all — the tool
// returns the raw equivalent instead, so the agent always gets the cheaper
// of the two and never a hard error that forces it back to a manual `cat`.
package emit

import (
	"io"

	"github.com/sofia-ctx/sofia/internal/tokens"
)

// Result reports which branch SmallerOf chose, for call-log telemetry
// (`sf history` can then show how often compression actually pays off).
type Result struct {
	UsedRaw     bool
	CompactToks int64
	RawToks     int64
}

// SmallerOf writes whichever of compact/raw costs fewer estimated tokens to
// w and reports the decision. Ties go to compact (the saver wins when equal).
// A nil raw means "no raw equivalent available" → compact is written as-is.
func SmallerOf(w io.Writer, compact, raw []byte) (Result, error) {
	ct := tokens.Estimate(string(compact))
	if raw == nil {
		_, err := w.Write(compact)
		return Result{UsedRaw: false, CompactToks: ct}, err
	}
	rt := tokens.Estimate(string(raw))
	if rt < ct {
		_, err := w.Write(raw)
		return Result{UsedRaw: true, CompactToks: ct, RawToks: rt}, err
	}
	_, err := w.Write(compact)
	return Result{UsedRaw: false, CompactToks: ct, RawToks: rt}, err
}
