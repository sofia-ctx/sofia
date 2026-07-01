// Package gripe records and lists agent complaints about sf — the one failure
// the call log can't capture on its own: sf exited 0 but gave the wrong thing,
// or the agent had to fall back to cat/rg/grep because sf couldn't do what was
// needed. `sf gripe '<one line>'` writes one cheap record (auto-tagged with
// project, session and time by calllog); bare `sf gripe` lists recent gripes so
// the sf author sees the coverage gaps to fix. Hard errors (exit != 0) are
// already in the log — read those with `sf history --failed --source agent`.
package gripe

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/toon"
)

// Options carries flag/arg state. A non-empty Message records a gripe; an empty
// Message lists recent ones.
type Options struct {
	Message string
	Format  string
	Limit   int // list view: max gripes to show (0 = unlimited)
}

// Run records a gripe when Message is set, otherwise lists recent gripes.
func Run(opts Options, w io.Writer) error {
	if strings.TrimSpace(opts.Message) == "" {
		return list(opts, w)
	}
	return record(opts, w)
}

// record self-logs the gripe via Start/Finish. calllog.skip suppresses only the
// central fallback (the bare list view), so an explicit Finish here always lands
// the entry — tool "gripe", args [msg], summary{note:msg} — with source,
// session id, project tag and timestamp filled in by calllog.
func record(opts Options, w io.Writer) error {
	msg := strings.TrimSpace(opts.Message)
	t := calllog.Start("gripe", []string{msg})
	t.SetSummary(map[string]any{"note": msg})
	cw := &calllog.Counter{W: w}
	fmt.Fprintln(cw, "gripe recorded")
	t.RecordOutput(cw)
	t.Finish(nil)
	return nil
}

func list(opts Options, w io.Writer) error {
	entries, err := calllog.Read()
	if err != nil {
		return err
	}
	var all []Gripe
	for _, e := range entries {
		if e.Tool != "gripe" {
			continue
		}
		all = append(all, toGripe(e))
	}
	return render(w, opts.Format, gripeView{
		Gripes: newestFirst(all, opts.Limit),
		Total:  len(all),
	})
}

// Gripe is one row of the list view.
type Gripe struct {
	When    string `json:"when"`    // YYYY-MM-DD HH:MM local (raw ts on parse fail)
	Project string `json:"project"` // tag; "?" when unknown
	Source  string `json:"source"`  // agent | manual | test
	Note    string `json:"note"`
}

func toGripe(e calllog.Entry) Gripe {
	g := Gripe{Project: e.Tag, Source: e.Source, Note: note(e), When: e.Timestamp}
	if g.Project == "" {
		g.Project = "?"
	}
	if g.Source == "" { // legacy entries pre-source-tagging are agent traffic
		g.Source = "agent"
	}
	if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
		g.When = t.Local().Format("2006-01-02 15:04")
	}
	return g
}

// note prefers the clean summary field, falling back to the recorded args so a
// gripe written by an older binary (no summary) still reads.
func note(e calllog.Entry) string {
	if e.Summary != nil {
		if v, ok := e.Summary["note"].(string); ok && v != "" {
			return v
		}
	}
	return strings.Join(e.Args, " ")
}

// newestFirst takes the last `limit` gripes (all if limit<=0) and reverses them
// so the newest is on top. Mirrors history's recentWindow.
func newestFirst(gripes []Gripe, limit int) []Gripe {
	window := gripes
	if limit > 0 && len(window) > limit {
		window = window[len(window)-limit:]
	}
	out := make([]Gripe, len(window))
	for i, g := range window {
		out[len(window)-1-i] = g
	}
	return out
}

type gripeView struct {
	Gripes []Gripe `json:"gripes"`
	Total  int     `json:"total"`
}

var gripeFields = []string{"when", "project", "source", "note"}

func render(w io.Writer, format string, v gripeView) error {
	switch format {
	case "", "toon":
		return renderTOON(w, v)
	case "md":
		return renderMarkdown(w, v)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}

func renderTOON(w io.Writer, v gripeView) error {
	fmt.Fprintf(w, "gripes[%d]{%s}:\n", len(v.Gripes), strings.Join(gripeFields, ","))
	for _, g := range v.Gripes {
		fmt.Fprintf(w, "%s%s,%s,%s,%s\n",
			toon.Indent,
			toon.Scalar(g.When),
			toon.Scalar(g.Project),
			toon.Scalar(g.Source),
			toon.Scalar(g.Note),
		)
	}
	if v.Total > len(v.Gripes) {
		fmt.Fprintf(w, "# +%d older not shown\n", v.Total-len(v.Gripes))
	}
	if v.Total == 0 {
		fmt.Fprintln(w, "# no gripes about sf")
	}
	return nil
}

func renderMarkdown(w io.Writer, v gripeView) error {
	fmt.Fprintf(w, "# Gripes about sf (%d shown / %d total)\n\n", len(v.Gripes), v.Total)
	if v.Total == 0 {
		fmt.Fprintln(w, "_no gripes_")
		return nil
	}
	for _, g := range v.Gripes {
		fmt.Fprintf(w, "- `%s` **%s** (%s) — %s\n", g.When, g.Project, g.Source, g.Note)
	}
	return nil
}
