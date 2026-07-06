// quota.go implements `sf cc value --quota`: a "subscription quota
// stretcher" report. Unlike the rest of this file (which reads Claude Code
// session transcripts and converts to $), the quota report reads sf's OWN
// telemetry — calllog.Read(), the same calls.jsonl `sf history` reads — and
// asks a different question: on a Claude subscription plan there's no $ to
// save (it's a flat fee), the thing that actually runs out is the rolling
// 5-hour usage window, so what matters is how many output tokens sf handed
// back instead of the agent paying full price for a raw read/grep/diff.
package cc

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
)

// QuotaWindow is the busiest-window span the report scans for: 5 hours is
// the Claude subscription's own rolling quota window, so "which 5h span
// burned the most tokens sf just saved you" is the direct answer to
// "where did --quota actually stretch my usage."
const QuotaWindow = 5 * time.Hour

// point is one baseline-bearing entry's contribution to the busiest-window
// scan: when it ran, and how much it saved.
type point struct {
	ts    time.Time
	saved int64
}

// Quota is the aggregate `sf cc value --quota` renders. Pure data — built by
// buildQuota, independent of how it's read or rendered.
type Quota struct {
	Days           int       `json:"days"`
	Since          time.Time `json:"since"`
	Until          time.Time `json:"until"`
	Calls          int       `json:"calls"`
	WithBaseline   int       `json:"with_baseline"` // calls whose summary carries tok_raw or tok_rep
	DedupStubs     int       `json:"dedup_stubs"`
	EmittedTokens  int64     `json:"emitted_tokens"`  // Σ out_tokens across Calls
	BaselineTokens int64     `json:"baseline_tokens"` // Σ tok_raw/tok_rep across WithBaseline
	SavedTokens    int64     `json:"saved_tokens"`    // Σ max(baseline-emitted, 0)
	SavedPct       float64   `json:"saved_pct"`       // SavedTokens / BaselineTokens * 100
	BusiestStart   time.Time `json:"busiest_start,omitempty"`
	BusiestSaved   int64     `json:"busiest_saved,omitempty"` // saved tokens inside that QuotaWindow
}

// buildQuota filters entries to real agent traffic (Source == "agent") in
// the last `days` days as of now, then aggregates. No I/O, no clock reads —
// tests pass `now` explicitly instead of faking time.
//
// A savings baseline exists only when a summary carries tok_raw (a full `sf
// code` call — see internal/common/code.Run) or tok_rep (a dedup stub — the
// original call's cost). Entries without either — grep/changed (no single
// raw equivalent) or log lines written before this field existed — still
// count toward Calls/EmittedTokens but contribute zero to the savings math;
// never guessed.
func buildQuota(entries []calllog.Entry, now time.Time, days int) Quota {
	since := now.AddDate(0, 0, -days)
	q := Quota{Days: days, Since: since, Until: now}

	var points []point
	for _, e := range entries {
		if e.Source != "agent" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil || ts.Before(since) || ts.After(now) {
			continue
		}
		q.Calls++
		q.EmittedTokens += e.OutputTokens
		if isDedupStub(e.Summary) {
			q.DedupStubs++
		}

		baseline, ok := savingsBaseline(e.Summary)
		if !ok {
			continue
		}
		q.WithBaseline++
		q.BaselineTokens += baseline
		saved := baseline - e.OutputTokens
		if saved < 0 {
			saved = 0
		}
		q.SavedTokens += saved
		points = append(points, point{ts: ts, saved: saved})
	}

	if q.BaselineTokens > 0 {
		q.SavedPct = float64(q.SavedTokens) / float64(q.BaselineTokens) * 100
	}

	start, saved := busiestWindow(points)
	q.BusiestStart, q.BusiestSaved = start, saved
	return q
}

// savingsBaseline reads a summary's raw-equivalent token cost: tok_raw for a
// full call, tok_rep for a dedup stub (mutually exclusive — see
// internal/common/code.Run). Summaries decode from JSON, so numbers arrive
// as float64; ok is false when neither key is present (no baseline).
func savingsBaseline(summary map[string]any) (int64, bool) {
	if v, ok := numField(summary, "tok_raw"); ok {
		return v, true
	}
	if v, ok := numField(summary, "tok_rep"); ok {
		return v, true
	}
	return 0, false
}

func numField(m map[string]any, key string) (int64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int64(f), true
}

func isDedupStub(summary map[string]any) bool {
	dedup, _ := summary["dedup"].(bool)
	return dedup
}

// busiestWindow finds the QuotaWindow-wide span (start anchored on a real
// entry, not a rounded clock tick) with the highest total saved tokens, via
// the standard sorted two-pointer sliding-window scan. pts need not be
// sorted going in. Returns the zero time and 0 when pts is empty or nothing
// was saved.
func busiestWindow(pts []point) (time.Time, int64) {
	if len(pts) == 0 {
		return time.Time{}, 0
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].ts.Before(pts[j].ts) })

	var bestStart time.Time
	var bestSaved, windowSaved int64
	left := 0
	for right := 0; right < len(pts); right++ {
		windowSaved += pts[right].saved
		for pts[right].ts.Sub(pts[left].ts) > QuotaWindow {
			windowSaved -= pts[left].saved
			left++
		}
		if windowSaved > bestSaved {
			bestSaved = windowSaved
			bestStart = pts[left].ts
		}
	}
	return bestStart, bestSaved
}

// runQuota reads sf's own call log, aggregates the last `days` days of agent
// traffic, and renders the quota-stretcher report.
func runQuota(days int, format string, w io.Writer) error {
	tracker := calllog.Start("cc.value", []string{"--format=" + format, "--quota"})
	entries, err := calllog.Read()
	if err != nil {
		tracker.Finish(err)
		return err
	}
	q := buildQuota(entries, time.Now(), days)
	tracker.SetSummary(map[string]any{
		"quota":         true,
		"calls":         q.Calls,
		"with_baseline": q.WithBaseline,
		"saved_tokens":  q.SavedTokens,
	})

	cw := &calllog.Counter{W: w}
	var renderErr error
	switch format {
	case "", "toon":
		renderQuotaTOON(cw, q)
	case "md":
		renderQuotaMarkdown(cw, q)
	case "json":
		enc := json.NewEncoder(cw)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		renderErr = enc.Encode(q)
	default:
		renderErr = fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// formatK renders a token count compactly for a one-screen report: below
// 1000 the plain number, above it the nearest whole K (round-half-up). A
// quota window sums many calls and easily reaches 6 digits, unlike
// emit.Footer's ≈N (a single call, small enough to read as-is) — same ≈
// convention, just abbreviated for a report that aggregates instead of one
// call's own cost.
func formatK(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%dK", (n+500)/1000)
}

const quotaClock = "2006-01-02 15:04"

func renderQuotaTOON(w io.Writer, q Quota) {
	fmt.Fprintf(w, "quota (source=agent, last %dd): %d calls, %d with baseline, %d dedup stubs\n",
		q.Days, q.Calls, q.WithBaseline, q.DedupStubs)
	fmt.Fprintf(w, "emitted ≈%s tok · baseline ≈%s · saved ≈%s (%.0f%%)\n",
		formatK(q.EmittedTokens), formatK(q.BaselineTokens), formatK(q.SavedTokens), q.SavedPct)
	if q.BusiestSaved > 0 {
		fmt.Fprintf(w, "busiest 5h window: %s +5h — saved ≈%s tok\n",
			q.BusiestStart.Format(quotaClock), formatK(q.BusiestSaved))
	}
}

func renderQuotaMarkdown(w io.Writer, q Quota) {
	fmt.Fprintf(w, "# sf cc value --quota\n\n")
	fmt.Fprintf(w, "Source: **agent** only, last **%d** day(s) (%s .. %s)\n\n",
		q.Days, q.Since.Format(quotaClock), q.Until.Format(quotaClock))
	fmt.Fprintf(w, "- Calls: **%d** (%d with a savings baseline, %d dedup stubs)\n", q.Calls, q.WithBaseline, q.DedupStubs)
	fmt.Fprintf(w, "- Emitted ≈%s tok · Baseline ≈%s tok · Saved **≈%s tok (%.0f%%)**\n",
		formatK(q.EmittedTokens), formatK(q.BaselineTokens), formatK(q.SavedTokens), q.SavedPct)
	if q.BusiestSaved > 0 {
		fmt.Fprintf(w, "- Busiest 5h window: %s +5h — saved ≈%s tok\n",
			q.BusiestStart.Format(quotaClock), formatK(q.BusiestSaved))
	}
}
