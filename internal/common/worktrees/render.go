package worktrees

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/sofia-ctx/sofia/internal/toon"
)

func renderTOON(w io.Writer, r *Result) {
	fmt.Fprintf(w, "worktrees[%d]{project,slug,branch,state,health,dirty,ahead,url,front,age}: # www=%s\n", len(r.Forks), r.Www)
	for _, f := range r.Forks {
		fmt.Fprintf(w, "%s%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n", toon.Indent,
			f.Project, dash(f.Slug), dash(f.Branch), state(f), dash(f.Health),
			yesno(f.Dirty), aheadStr(f.Ahead), toon.Scalar(dash(f.URL)),
			toon.Scalar(dash(f.FrontURL)), toon.Scalar(dash(f.Age)))
	}
}

func renderMarkdown(w io.Writer, r *Result) {
	fmt.Fprintf(w, "# Worktrees — %d forks (www=%s)\n\n", len(r.Forks), r.Www)
	fmt.Fprintln(w, "| Project | Slug | Branch | State | Health | Dirty | Ahead | URL | Front | Age |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- | ---: | --- | --- | --- |")
	for _, f := range r.Forks {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			f.Project, dash(f.Slug), dash(f.Branch), state(f), dash(f.Health),
			yesno(f.Dirty), aheadStr(f.Ahead), dash(f.URL), dash(f.FrontURL), dash(f.Age))
	}
}

func renderJSON(w io.Writer, r *Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

func state(f Fork) string {
	switch {
	case f.Running == nil:
		return "-"
	case *f.Running:
		return "up"
	default:
		return "down"
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "-"
}

func aheadStr(a *int) string {
	if a == nil {
		return "-"
	}
	return strconv.Itoa(*a)
}
