package refs

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sofia-ctx/sofia/internal/toon"
)

var refFields = []string{"kind", "file", "line", "enclosing", "text"}

// header is the one-line summary every format leads with: true totals (not
// the possibly-capped list below it), plus a truncation note when the cap
// dropped entries.
func header(r *Result) string {
	h := fmt.Sprintf("# refs: %s — %d def(s), %d use(s)", r.Symbol, r.Defs, r.Uses)
	if r.Truncated > 0 {
		h += fmt.Sprintf(" (+%d more, --max to widen)", r.Truncated)
	}
	return h
}

func renderTOON(w io.Writer, r *Result) {
	fmt.Fprintln(w, header(r))
	fmt.Fprintf(w, "refs[%d]{%s}:\n", len(r.Refs), strings.Join(refFields, ","))
	for _, ref := range r.Refs {
		fmt.Fprintf(w, "%s%s,%s,%d,%s,%s\n",
			toon.Indent,
			toon.Scalar(ref.Kind), toon.Scalar(ref.File), ref.Line,
			toon.Scalar(ref.Enclosing), toon.Scalar(trim(ref.Text, 200)))
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "# skipped %d binary/unreadable files\n", len(r.Skipped))
	}
}

func renderMarkdown(w io.Writer, r *Result) {
	fmt.Fprintln(w, header(r))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| kind | file:line | enclosing | text |")
	fmt.Fprintln(w, "|---|---|---|---|")
	for _, ref := range r.Refs {
		fmt.Fprintf(w, "| %s | %s:%d | %s | %s |\n", ref.Kind, ref.File, ref.Line, ref.Enclosing, trim(ref.Text, 200))
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "\nskipped %d file(s): binary or unreadable\n", len(r.Skipped))
	}
}

func renderJSON(w io.Writer, r *Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

func trim(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
