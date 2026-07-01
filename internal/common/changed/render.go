package changed

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/sofia-ctx/sofia/internal/toon"
)

func renderTOON(w io.Writer, r *Result) {
	fmt.Fprintf(w, "changed[%d]{status,cat,add,del,path,symbols}: # %s\n", len(r.Changes), r.Spec)
	for _, c := range r.Changes {
		fmt.Fprintf(w, "%s%s,%s,%d,%d,%s,%s\n", toon.Indent,
			c.Status, c.Category, c.Adds, c.Dels,
			toon.Scalar(c.Path), toon.Scalar(truncate(strings.Join(c.Symbols, "; "), 90)))
	}

	files, adds, dels, byCat := totals(r)
	fmt.Fprintf(w, "totals: files=%d,+%d,-%d\n", files, adds, dels)
	if len(byCat) > 0 {
		fmt.Fprintf(w, "by_category[%d]{category,files}:\n", len(byCat))
		for _, kv := range byCat {
			fmt.Fprintf(w, "%s%s,%d\n", toon.Indent, kv.cat, kv.n)
		}
	}
}

func renderMarkdown(w io.Writer, r *Result) {
	fmt.Fprintf(w, "# Changed — %s (%d files)\n\n", r.Spec, len(r.Changes))
	fmt.Fprintln(w, "| Status | Cat | +/- | Path | Symbols |")
	fmt.Fprintln(w, "| --- | --- | ---: | --- | --- |")
	for _, c := range r.Changes {
		fmt.Fprintf(w, "| %s | %s | +%d/-%d | `%s` | %s |\n",
			c.Status, c.Category, c.Adds, c.Dels, c.Path, strings.Join(c.Symbols, "; "))
	}
	files, adds, dels, byCat := totals(r)
	fmt.Fprintf(w, "\n**totals:** %d files, +%d/-%d", files, adds, dels)
	if len(byCat) > 0 {
		parts := make([]string, len(byCat))
		for i, kv := range byCat {
			parts[i] = fmt.Sprintf("%s %d", kv.cat, kv.n)
		}
		fmt.Fprintf(w, " — %s", strings.Join(parts, ", "))
	}
	fmt.Fprintln(w)
}

func renderJSON(w io.Writer, r *Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

type catCount struct {
	cat string
	n   int
}

func totals(r *Result) (files, adds, dels int, byCat []catCount) {
	files = len(r.Changes)
	counts := map[string]int{}
	for _, c := range r.Changes {
		adds += c.Adds
		dels += c.Dels
		counts[c.Category]++
	}
	for cat, n := range counts {
		byCat = append(byCat, catCount{cat, n})
	}
	sort.Slice(byCat, func(i, j int) bool {
		if byCat[i].n != byCat[j].n {
			return byCat[i].n > byCat[j].n
		}
		return byCat[i].cat < byCat[j].cat
	})
	return files, adds, dels, byCat
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
