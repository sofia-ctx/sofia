// Package emit enforces the core invariant of every token-saving sofia
// tool: a tool that stands in for a standard command (cat, grep, …) must
// never cost MORE tokens than that command would. If the compact rendering
// is not actually smaller — or could not be produced at all — the tool
// returns the raw equivalent instead, so the agent always gets the cheaper
// of the two and never a hard error that forces it back to a manual `cat`.
package emit

import (
	"fmt"
	"io"
	"os"

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

// Footer writes one terse cost-accounting line to w — the one place `sf`
// prints its own token economics back into tool output, so the coaching
// signal ("this call cost N, here's what it saved") is visible to the model
// making the calls, not just to a human reading calls.jsonl later.
//
// tok is the running total from the same calllog.Counter the rest of the
// output went through, read AT THE MOMENT Footer is called — the footer's
// own text (a handful of tokens) is written after that snapshot and is
// deliberately not folded back in (that would mean rendering the line
// twice). Callers should treat the printed number as "cost so far," not a
// byte-exact total; the gap is a few tokens and irrelevant at this
// granularity.
//
// rawTok is the estimated cost of the raw equivalent (e.g. `cat`-ing the
// same file(s)), for callers that have one to compare against; pass 0 when
// there's nothing to compare (grep/changed have no single raw baseline).
// Savings are only reported when they're actually positive — a rawTok that
// doesn't beat tok (a passthrough response, or a compact summary that lost
// to raw and was replaced by it) falls back to a plain note instead of ever
// printing a negative "saved".
//
// SOFIA_FOOTER=off disables the footer everywhere; this is the only place
// that switch is read.
func Footer(w io.Writer, tok, rawTok int64) {
	if os.Getenv("SOFIA_FOOTER") == "off" {
		return
	}
	if rawTok > 0 {
		if saved := rawTok - tok; saved > 0 {
			fmt.Fprintf(w, "# sf ≈%d tok · raw ≈%d · saved ≈%d\n", tok, rawTok, saved)
			return
		}
		fmt.Fprintf(w, "# sf ≈%d tok · raw passthrough\n", tok)
		return
	}
	fmt.Fprintf(w, "# sf ≈%d tok\n", tok)
}
