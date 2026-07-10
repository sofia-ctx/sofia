package grep

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sofia-ctx/sofia/pkg/toon"
)

var hitFields = []string{"file", "line", "col", "enclosing", "text"}

func renderJSON(w io.Writer, r *Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

func renderTOON(w io.Writer, r *Result, maxPerPattern int) {
	for _, pr := range r.Patterns {
		shown := pr.Hits
		truncated := 0
		if maxPerPattern > 0 && len(pr.Hits) > maxPerPattern {
			shown = pr.Hits[:maxPerPattern]
			truncated = len(pr.Hits) - maxPerPattern
		}
		fmt.Fprintf(w, "%s{files=%d,hits=%d}:\n",
			toon.Scalar(pr.Pattern), pr.Files, len(pr.Hits))
		fmt.Fprintf(w, "%shits[%d]{%s}:\n",
			toon.Indent, len(shown), strings.Join(hitFields, ","))
		rowIndent := toon.Indent + toon.Indent
		for _, h := range shown {
			fmt.Fprintf(w, "%s%s,%d,%d,%s,%s\n",
				rowIndent,
				toon.Scalar(h.File),
				h.Line, h.Col,
				toon.Scalar(h.Enclosing),
				toon.Scalar(trim(h.Text, 200)),
			)
		}
		if truncated > 0 {
			fmt.Fprintf(w, "%s# +%d more truncated\n", rowIndent, truncated)
		}
	}
	if len(r.Empty) > 0 {
		fmt.Fprintf(w, "empty[%d]: %s\n", len(r.Empty), toon.JoinList(r.Empty))
	}
	if r.Skipped > 0 {
		fmt.Fprintf(w, "skipped: %d # binary or over-long lines\n", r.Skipped)
	}
}

func renderMarkdown(w io.Writer, r *Result, maxPerPattern int) {
	for i, pr := range r.Patterns {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "# %s    files=%d hits=%d\n\n", pr.Pattern, pr.Files, len(pr.Hits))
		shown := pr.Hits
		truncated := 0
		if maxPerPattern > 0 && len(pr.Hits) > maxPerPattern {
			shown = pr.Hits[:maxPerPattern]
			truncated = len(pr.Hits) - maxPerPattern
		}
		for _, h := range shown {
			fmt.Fprintf(w, "  %s:%d", h.File, h.Line)
			if h.Enclosing != "" {
				fmt.Fprintf(w, "  %s", h.Enclosing)
			}
			fmt.Fprintln(w)
			fmt.Fprintf(w, "    %s\n", trim(h.Text, 200))
		}
		if truncated > 0 {
			fmt.Fprintf(w, "  ... (+%d more)\n", truncated)
		}
	}
	if len(r.Empty) > 0 {
		fmt.Fprintf(w, "\nno matches: %s\n", strings.Join(r.Empty, ", "))
	}
	if r.Skipped > 0 {
		fmt.Fprintf(w, "\nskipped %d file(s): binary or over-long lines\n", r.Skipped)
	}
}

func trim(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
