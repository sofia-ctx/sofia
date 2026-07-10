package doctor

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/sofia-ctx/sofia/pkg/toon"
)

func renderTOON(w io.Writer, r *Result) {
	fmt.Fprintf(w, "checks[%d]{check,status,detail}:\n", len(r.Checks))
	for _, c := range r.Checks {
		fmt.Fprintf(w, "%s%s,%s,%s\n", toon.Indent, c.Name, c.Status, toon.Scalar(c.Detail))
	}
}

func renderMarkdown(w io.Writer, r *Result) {
	fmt.Fprintln(w, "| Check | Status | Detail |")
	fmt.Fprintln(w, "| --- | --- | --- |")
	for _, c := range r.Checks {
		fmt.Fprintf(w, "| %s | %s | %s |\n", c.Name, c.Status, c.Detail)
	}
}

func renderJSON(w io.Writer, r *Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}
