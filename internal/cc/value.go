package cc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/cliflags"
	"github.com/sofia-ctx/sofia/internal/toon"
)

func newValueCommand() *cobra.Command {
	var (
		projectsDir string
		project     string
		days        int
		format      string
		quota       bool
	)
	cmd := &cobra.Command{
		Use:   "value",
		Short: "Weekly $ delta and token-type breakdown from your own session history",
		Long: `value turns the roadmap's "measurement as a feature" idea into a command:
your own recent Claude Code usage, converted to $ from the real per-session
token counts cc already parses (no new transcript parsing), broken down by
token type (input/output/cache_read/cache_creation) and compared against
the preceding window of the same length.

  sf cc value                   last 7 days vs the 7 days before that
  sf cc value --days 14         last 14 days vs the 14 before that
  sf cc value --project myapp   one project only

Pricing is a hardcoded snapshot (see docs/measurements/tools/cc-value.md) — sessions
on a model not in the table are counted in tokens but excluded from $ and
reported separately as unpriced, never guessed. Telemetry stays local:
this reads your own ~/.claude/projects transcripts and prints to stdout.

--quota is a different report for a different plan: on a Claude subscription
there's no $ to save, so it reads sf's OWN telemetry (calls.jsonl, source=agent
only — real tool traffic, not manual/test runs) and reports how many output
tokens sf actually handed back vs. what those same calls would have cost raw,
including the single busiest 5-hour span (the subscription's own quota
window). --project doesn't apply to --quota (calls.jsonl isn't project-scoped
the way transcripts are) — pass --days as usual.

  sf cc value --quota            last 7 days of sf's own call log
  sf cc value --quota --days 14  two weeks instead`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&projectsDir, "projects-dir", "", "Claude Code projects root (overrides $CC_PROJECTS_DIR)")
	cmd.Flags().StringVar(&project, "project", "", "filter by project (substring of dir name or cwd) — $ report only")
	cmd.Flags().IntVar(&days, "days", 7, "window size in days (current vs the preceding window of the same size)")
	cmd.Flags().BoolVar(&quota, "quota", false, "report sf's own token savings (subscription quota angle) instead of $")
	cliflags.AttachFormatFlags(cmd, &format)
	_ = cmd.RegisterFlagCompletionFunc("projects-dir", cliflags.DirOnly)

	cmd.RunE = func(c *cobra.Command, _ []string) error {
		if days <= 0 {
			return fmt.Errorf("--days must be positive, got %d", days)
		}
		if quota {
			if c.Flags().Changed("project") {
				return fmt.Errorf("--project applies to the $ report only")
			}
			return runQuota(days, format, os.Stdout)
		}
		dir, err := ProjectsDir(projectsDir)
		if err != nil {
			return err
		}
		now := time.Now()
		sessions, err := collectSessions(dir, project, now.AddDate(0, 0, -2*days), false)
		if err != nil {
			return err
		}
		return runValue(sessions, now, days, format, os.Stdout)
	}
	return cmd
}

// TokenTypeCost is one token type's aggregate volume and priced $ within a
// period.
type TokenTypeCost struct {
	Type    string  `json:"type"`
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"cost_usd"`
}

// ModelUsage aggregates one model's sessions/tokens/cost within the
// current window.
type ModelUsage struct {
	Model    string  `json:"model"`
	Sessions int     `json:"sessions"`
	Tokens   int64   `json:"tokens"`
	CostUSD  float64 `json:"cost_usd"`
	Priced   bool    `json:"priced"`
}

// Period is one time window's aggregate: $ cost and a token-type
// breakdown. UnpricedTokens is the token volume from sessions whose model
// isn't in modelPrices — real usage, just not converted to $.
type Period struct {
	Since          time.Time       `json:"since"`
	Until          time.Time       `json:"until"`
	Sessions       int             `json:"sessions"`
	CostUSD        float64         `json:"cost_usd"`
	ByType         []TokenTypeCost `json:"by_type"`
	UnpricedTokens int64           `json:"unpriced_tokens,omitempty"`
}

// Value is the full result of `sf cc value`: the current window vs the
// preceding window of equal length, the $ delta between them, and a
// per-model breakdown of the current window.
type Value struct {
	Current  Period       `json:"current"`
	Previous Period       `json:"previous"`
	DeltaUSD float64      `json:"delta_usd"`
	DeltaPct float64      `json:"delta_pct,omitempty"`
	ByModel  []ModelUsage `json:"by_model"`
}

// buildValue buckets sessions into "current" (the last `days`) and
// "previous" (the `days` before that) by session end time, prices each
// session's aggregate token counts at its model's rate, and returns the $
// delta plus a token-type breakdown. Pure (no I/O) so it's unit-testable.
func buildValue(sessions []*Session, now time.Time, days int) Value {
	curStart := now.AddDate(0, 0, -days)
	prevStart := curStart.AddDate(0, 0, -days)

	v := Value{
		Current:  Period{Since: curStart, Until: now},
		Previous: Period{Since: prevStart, Until: curStart},
	}

	type modelAgg struct {
		sessions int
		tokens   int64
		cost     float64
		priced   bool
	}
	byModel := map[string]*modelAgg{}
	curByType := map[string]*TokenTypeCost{}
	prevByType := map[string]*TokenTypeCost{}

	for _, s := range sessions {
		t := s.End
		if t.IsZero() {
			t = s.Start
		}
		if t.IsZero() || t.Before(prevStart) {
			continue // no reliable timestamp, or outside both windows
		}

		var period *Period
		var byType map[string]*TokenTypeCost
		current := false
		switch {
		case !t.Before(curStart):
			period, byType, current = &v.Current, curByType, true
		default: // prevStart <= t < curStart, guaranteed by the guard above
			period, byType = &v.Previous, prevByType
		}
		period.Sessions++

		price, priced := priceFor(s.Model)
		types := [4]struct {
			name   string
			tokens int64
			rate   float64
		}{
			{"input", s.InputTokens, price.Input},
			{"output", s.OutputTokens, price.Output},
			{"cache_read", s.CacheReadTokens, price.CacheRead},
			{"cache_creation", s.CacheCreateTokens, price.CacheWrite},
		}
		var sessionTokens int64
		var sessionCost float64
		for _, tt := range types {
			if tt.tokens == 0 {
				continue
			}
			sessionTokens += tt.tokens
			e := byType[tt.name]
			if e == nil {
				e = &TokenTypeCost{Type: tt.name}
				byType[tt.name] = e
			}
			e.Tokens += tt.tokens
			if priced {
				c := cost(tt.tokens, tt.rate)
				e.CostUSD += c
				sessionCost += c
			}
		}
		if priced {
			period.CostUSD += sessionCost
		} else {
			period.UnpricedTokens += sessionTokens
		}

		if current {
			m := s.Model
			if m == "" {
				m = "?"
			}
			a := byModel[m]
			if a == nil {
				a = &modelAgg{priced: priced}
				byModel[m] = a
			}
			a.sessions++
			a.tokens += sessionTokens
			a.cost += sessionCost
		}
	}

	v.Current.ByType = sortedTypes(curByType)
	v.Previous.ByType = sortedTypes(prevByType)
	v.DeltaUSD = v.Current.CostUSD - v.Previous.CostUSD
	if v.Previous.CostUSD > 0 {
		v.DeltaPct = v.DeltaUSD / v.Previous.CostUSD * 100
	}

	for m, a := range byModel {
		v.ByModel = append(v.ByModel, ModelUsage{Model: m, Sessions: a.sessions, Tokens: a.tokens, CostUSD: a.cost, Priced: a.priced})
	}
	sort.Slice(v.ByModel, func(i, j int) bool {
		if v.ByModel[i].CostUSD != v.ByModel[j].CostUSD {
			return v.ByModel[i].CostUSD > v.ByModel[j].CostUSD
		}
		return v.ByModel[i].Model < v.ByModel[j].Model
	})

	return v
}

// sortedTypes returns a token-type breakdown, largest token volume first
// (ties broken by name) so the dominant type — usually cache_read — leads.
func sortedTypes(m map[string]*TokenTypeCost) []TokenTypeCost {
	out := make([]TokenTypeCost, 0, len(m))
	for _, e := range m {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Type < out[j].Type
	})
	return out
}

func runValue(sessions []*Session, now time.Time, days int, format string, w io.Writer) error {
	tracker := calllog.Start("cc.value", []string{"--format=" + format})
	v := buildValue(sessions, now, days)
	tracker.SetSummary(map[string]any{
		"current_sessions": v.Current.Sessions,
		"delta_usd":        v.DeltaUSD,
	})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch format {
	case "", "toon":
		renderValueTOON(cw, v)
	case "md":
		renderValueMarkdown(cw, v)
	case "json":
		enc := json.NewEncoder(cw)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		renderErr = enc.Encode(v)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

func renderValueTOON(w io.Writer, v Value) {
	const day = "2006-01-02"
	fmt.Fprintf(w, "# sf cc value — %s..%s vs %s..%s\n",
		v.Current.Since.Format(day), v.Current.Until.Format(day),
		v.Previous.Since.Format(day), v.Previous.Until.Format(day))
	fmt.Fprintf(w, "current: sessions=%d cost_usd=%.2f", v.Current.Sessions, v.Current.CostUSD)
	if v.Current.UnpricedTokens > 0 {
		fmt.Fprintf(w, " unpriced_tokens=%d", v.Current.UnpricedTokens)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "previous: sessions=%d cost_usd=%.2f", v.Previous.Sessions, v.Previous.CostUSD)
	if v.Previous.UnpricedTokens > 0 {
		fmt.Fprintf(w, " unpriced_tokens=%d", v.Previous.UnpricedTokens)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "delta: usd=%+.2f pct=%+.1f%%\n", v.DeltaUSD, v.DeltaPct)
	fmt.Fprintf(w, "by_type[%d]{type,tokens,cost_usd}:\n", len(v.Current.ByType))
	for _, t := range v.Current.ByType {
		fmt.Fprintf(w, "%s%s,%d,%.4f\n", toon.Indent, t.Type, t.Tokens, t.CostUSD)
	}
	fmt.Fprintf(w, "by_model[%d]{model,sessions,tokens,cost_usd,priced}:\n", len(v.ByModel))
	for _, m := range v.ByModel {
		fmt.Fprintf(w, "%s%s,%d,%d,%.4f,%t\n", toon.Indent, toon.Scalar(m.Model), m.Sessions, m.Tokens, m.CostUSD, m.Priced)
	}
}

func renderValueMarkdown(w io.Writer, v Value) {
	const day = "2006-01-02"
	fmt.Fprintf(w, "# sf cc value\n\n")
	fmt.Fprintf(w, "Current: **%s .. %s** — %d session(s), **$%.2f**",
		v.Current.Since.Format(day), v.Current.Until.Format(day), v.Current.Sessions, v.Current.CostUSD)
	if v.Current.UnpricedTokens > 0 {
		fmt.Fprintf(w, " (+%d unpriced tokens)", v.Current.UnpricedTokens)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Previous: %s .. %s — %d session(s), $%.2f\n",
		v.Previous.Since.Format(day), v.Previous.Until.Format(day), v.Previous.Sessions, v.Previous.CostUSD)
	fmt.Fprintf(w, "Delta: **%+.2f USD** (%+.1f%%)\n\n", v.DeltaUSD, v.DeltaPct)

	fmt.Fprintln(w, "## By token type (current window)")
	fmt.Fprintln(w, "| Type | Tokens | $ |")
	fmt.Fprintln(w, "| --- | ---: | ---: |")
	for _, t := range v.Current.ByType {
		fmt.Fprintf(w, "| %s | %d | %.4f |\n", t.Type, t.Tokens, t.CostUSD)
	}

	fmt.Fprintln(w, "\n## By model (current window)")
	fmt.Fprintln(w, "| Model | Sessions | Tokens | $ | Priced |")
	fmt.Fprintln(w, "| --- | ---: | ---: | ---: | --- |")
	for _, m := range v.ByModel {
		fmt.Fprintf(w, "| %s | %d | %d | %.4f | %t |\n", m.Model, m.Sessions, m.Tokens, m.CostUSD, m.Priced)
	}
}
