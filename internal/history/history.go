// Package history reads the sofia call log and surfaces invocation
// history. Three views are supported:
//   - recent (default): chronological list of last N calls.
//   - --stats: per-tool aggregates (count, errors, duration percentiles,
//     output bytes/tokens, top inputs).
//   - --histogram=hour|day: distribution of calls by time bucket.
package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/cliflags"
	"github.com/sofia-ctx/sofia/internal/toon"
)

type Filter struct {
	Tool       string
	Since      time.Time
	Source     string // "", "agent", "manual", "test" — empty = all
	Session    string // prefix-match on the session id (sid); empty = all
	Tag        string // exact project tag; empty = all
	FailedOnly bool   // keep only entries that ended in error (ExitCode != 0)
}

// NewCommand returns the `sf history` Cobra command.
func NewCommand() *cobra.Command {
	var (
		toolFlag     string
		sinceFlag    string
		histogramVal string
		limit        int
		stats        bool
		clear        bool
		topInputs    int
		format       string
		sourceFlag   string
		sessionFlag  string
		tagFlag      string
		adoption     bool
		failedFlag   bool
	)
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show invocation history of sf tools",
		Long: `history reads the sofia call log (XDG-located JSONL) and reports one
of three views:

  default        the last N invocations, newest first
  --stats        per-tool aggregates: count, errors, total/mean/p50/p95/max
                 duration, total output bytes & tokens, top-N inputs
  --histogram=hour|day
                 distribution of calls by hour-of-day or by date
  --adoption     who actually uses sf: per project×source totals (calls,
                 distinct sessions, tokens, failed%)

Examples:
  sf history                                # last 20 calls, newest first
  sf history --failed                       # only calls that ended in error
  sf history --tool composer --since 24h    # only composer tools, past day
  sf history --stats --top-inputs 10        # aggregated, top 10 inputs
  sf history --histogram=hour               # 24-row hour distribution
  sf history --histogram=day --since 7d     # last week, daily buckets
  sf history --session 6bd96fc7             # one session (joins with sf cc)
  sf history --tag myapp --source agent     # one project's agent traffic
  sf history --adoption --since 7d          # adoption by project, past week
  sf history --clear                        # truncate the log`,
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&toolFlag, "tool", "", "filter by tool name (prefix match)")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "show entries since duration (e.g. 30m, 1h, 24h, 7d)")
	cmd.Flags().IntVar(&limit, "limit", 20, "max entries to show in recent view (0 = unlimited)")
	cmd.Flags().BoolVar(&stats, "stats", false, "show per-tool aggregated stats instead of the recent list")
	cmd.Flags().BoolVar(&clear, "clear", false, "truncate the log and exit")
	cmd.Flags().IntVar(&topInputs, "top-inputs", 5, "with --stats: show top N inputs across all calls")
	cmd.Flags().StringVar(&histogramVal, "histogram", "", "show call distribution by 'hour' or 'day'")
	cmd.Flags().StringVar(&sourceFlag, "source", "", "filter by source: agent|manual|test (empty = all)")
	cmd.Flags().StringVar(&sessionFlag, "session", "", "filter by session id (prefix match); joins with `sf cc`")
	cmd.Flags().StringVar(&tagFlag, "tag", "", "filter by project tag")
	cmd.Flags().BoolVar(&adoption, "adoption", false, "show per project×source adoption totals")
	cmd.Flags().BoolVar(&failedFlag, "failed", false, "only calls that ended in error (exit != 0)")
	cliflags.AttachFormatFlags(cmd, &format)

	_ = cmd.RegisterFlagCompletionFunc("source", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"agent\tas Claude runs it (piped)", "manual\thand-run in a terminal", "test\tgo test"}, cobra.ShellCompDirectiveNoFileComp
	})

	_ = cmd.RegisterFlagCompletionFunc("histogram", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"hour\tbucket by hour-of-day (0-23)", "day\tbucket by date"}, cobra.ShellCompDirectiveNoFileComp
	})

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		if clear {
			return os.WriteFile(calllog.Path(), nil, 0o644)
		}
		f := Filter{Tool: toolFlag, Source: sourceFlag, Session: sessionFlag, Tag: tagFlag, FailedOnly: failedFlag}
		if sinceFlag != "" {
			d, err := parseSince(sinceFlag)
			if err != nil {
				return err
			}
			f.Since = time.Now().Add(-d)
		}
		entries, err := readEntries(f)
		if err != nil {
			return err
		}

		switch {
		case adoption:
			return render(os.Stdout, format, adoptionView{Rows: buildAdoption(entries), Total: len(entries)})
		case histogramVal != "":
			bucket, err := parseHistogramBucket(histogramVal)
			if err != nil {
				return err
			}
			return render(os.Stdout, format, histogramView{
				Bucket:  histogramVal,
				Buckets: buildHistogram(entries, bucket),
				Total:   len(entries),
			})
		case stats:
			aggs, top := aggregate(entries, topInputs)
			return render(os.Stdout, format, statsView{Stats: aggs, TopInputs: top, Total: len(entries)})
		default:
			return render(os.Stdout, format, recentView{Entries: recentWindow(entries, limit), Total: len(entries)})
		}
	}
	return cmd
}

// recentWindow returns the most recent entries newest-first. Entries arrive
// in append order (oldest first); we take the last `limit` (all if limit<=0)
// and reverse so the newest call is on top.
func recentWindow(entries []calllog.Entry, limit int) []calllog.Entry {
	window := entries
	if limit > 0 && len(window) > limit {
		window = window[len(window)-limit:]
	}
	out := make([]calllog.Entry, len(window))
	for i, e := range window {
		out[len(window)-1-i] = e
	}
	return out
}

func parseSince(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, fmt.Errorf("invalid --since %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --since %q (use 30m, 1h, 24h, 7d)", s)
	}
	return d, nil
}

// sourceMatch reports whether an entry's source satisfies the filter.
// Legacy entries (written before source tagging) have no source and are
// treated as "agent".
func sourceMatch(have, want string) bool {
	if have == "" {
		have = "agent"
	}
	return have == want
}

func readEntries(f Filter) ([]calllog.Entry, error) {
	file, err := os.Open(calllog.Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var out []calllog.Entry
	for sc.Scan() {
		var e calllog.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if f.Tool != "" && !strings.HasPrefix(e.Tool, f.Tool) {
			continue
		}
		if f.Source != "" && !sourceMatch(e.Source, f.Source) {
			continue
		}
		if f.Session != "" && !strings.HasPrefix(e.SessionID, f.Session) {
			continue
		}
		if f.Tag != "" && e.Tag != f.Tag {
			continue
		}
		if f.FailedOnly && e.ExitCode == 0 {
			continue
		}
		if !f.Since.IsZero() {
			t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
			if err != nil {
				continue
			}
			if t.Before(f.Since) {
				continue
			}
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Aggregate is one row of the per-tool stats view.
type Aggregate struct {
	Tool        string `json:"tool"`
	Calls       int    `json:"calls"`
	Errors      int    `json:"errors"`
	TotalMs     int64  `json:"total_ms"`
	MeanMs      int64  `json:"mean_ms"`
	P50Ms       int64  `json:"p50_ms"`
	P95Ms       int64  `json:"p95_ms"`
	MaxMs       int64  `json:"max_ms"`
	TotalBytes  int64  `json:"total_out_bytes"`
	TotalTokens int64  `json:"total_out_tokens"`
	First       string `json:"first"`
	Last        string `json:"last"`
}

type InputCount struct {
	Tool  string `json:"tool"`
	Input string `json:"input"`
	Count int    `json:"count"`
}

func aggregate(entries []calllog.Entry, topN int) ([]Aggregate, []InputCount) {
	byTool := make(map[string]*Aggregate)
	durations := make(map[string][]int64)
	inputCounts := make(map[string]*InputCount) // key = tool + "\x1f" + input

	for _, e := range entries {
		a, ok := byTool[e.Tool]
		if !ok {
			a = &Aggregate{Tool: e.Tool, First: e.Timestamp}
			byTool[e.Tool] = a
		}
		a.Calls++
		a.TotalMs += e.DurationMs
		if e.DurationMs > a.MaxMs {
			a.MaxMs = e.DurationMs
		}
		a.TotalBytes += e.OutputBytes
		a.TotalTokens += e.OutputTokens
		if e.ExitCode != 0 {
			a.Errors++
		}
		if e.Timestamp != "" && (a.First == "" || e.Timestamp < a.First) {
			a.First = e.Timestamp
		}
		if e.Timestamp > a.Last {
			a.Last = e.Timestamp
		}
		durations[e.Tool] = append(durations[e.Tool], e.DurationMs)

		for _, in := range extractInputs(e) {
			key := e.Tool + "\x1f" + in
			ic, exists := inputCounts[key]
			if !exists {
				ic = &InputCount{Tool: e.Tool, Input: in}
				inputCounts[key] = ic
			}
			ic.Count++
		}
	}

	for tool, durs := range durations {
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		a := byTool[tool]
		if a.Calls > 0 {
			a.MeanMs = a.TotalMs / int64(a.Calls)
		}
		a.P50Ms = percentile(durs, 50)
		a.P95Ms = percentile(durs, 95)
	}

	aggs := make([]Aggregate, 0, len(byTool))
	for _, a := range byTool {
		aggs = append(aggs, *a)
	}
	sort.Slice(aggs, func(i, j int) bool { return aggs[i].Calls > aggs[j].Calls })

	ics := make([]InputCount, 0, len(inputCounts))
	for _, ic := range inputCounts {
		ics = append(ics, *ic)
	}
	sort.Slice(ics, func(i, j int) bool { return ics[i].Count > ics[j].Count })
	if topN > 0 && len(ics) > topN {
		ics = ics[:topN]
	}
	return aggs, ics
}

func percentile(sortedDurs []int64, p int) int64 {
	if len(sortedDurs) == 0 {
		return 0
	}
	idx := (len(sortedDurs) * p) / 100
	if idx >= len(sortedDurs) {
		idx = len(sortedDurs) - 1
	}
	return sortedDurs[idx]
}

// HistogramBucket identifies how entries are grouped.
type HistogramBucket int

const (
	HistogramHour HistogramBucket = iota
	HistogramDay
)

func parseHistogramBucket(s string) (HistogramBucket, error) {
	switch strings.ToLower(s) {
	case "hour", "h":
		return HistogramHour, nil
	case "day", "d":
		return HistogramDay, nil
	default:
		return 0, fmt.Errorf("--histogram %q: use hour|day", s)
	}
}

// HistogramRow is one bucket: a label plus the totals for the entries
// that fell into it.
type HistogramRow struct {
	Bucket      string `json:"bucket"`
	Count       int    `json:"count"`
	TotalMs     int64  `json:"total_ms"`
	TotalBytes  int64  `json:"total_out_bytes"`
	TotalTokens int64  `json:"total_out_tokens"`
}

// buildHistogram returns one row per non-empty bucket. Hour buckets are
// "00".."23" (sorted lexically = chronologically). Day buckets are
// YYYY-MM-DD (also chronological lexically).
func buildHistogram(entries []calllog.Entry, b HistogramBucket) []HistogramRow {
	rows := make(map[string]*HistogramRow)
	for _, e := range entries {
		t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil {
			continue
		}
		var key string
		switch b {
		case HistogramHour:
			key = fmt.Sprintf("%02d", t.Hour())
		case HistogramDay:
			key = t.Format("2006-01-02")
		}
		r, ok := rows[key]
		if !ok {
			r = &HistogramRow{Bucket: key}
			rows[key] = r
		}
		r.Count++
		r.TotalMs += e.DurationMs
		r.TotalBytes += e.OutputBytes
		r.TotalTokens += e.OutputTokens
	}
	out := make([]HistogramRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket < out[j].Bucket })
	return out
}

// AdoptionRow aggregates one project×source cell: how much that project's
// agent/manual traffic actually exercises sf, and how reliably.
type AdoptionRow struct {
	Tag       string  `json:"tag"`
	Source    string  `json:"source"`
	Calls     int     `json:"calls"`
	Sessions  int     `json:"sessions"` // distinct non-empty session ids
	Errors    int     `json:"errors"`
	FailedPct float64 `json:"failed_pct"`
	Tokens    int64   `json:"out_tokens"`
}

// buildAdoption groups entries by (tag, source). Legacy entries with no source
// count as "agent" (matching sourceMatch); a missing tag renders as "?". Rows
// are sorted by call count, busiest first.
func buildAdoption(entries []calllog.Entry) []AdoptionRow {
	type acc struct {
		row  AdoptionRow
		sids map[string]struct{}
	}
	groups := make(map[string]*acc)
	for _, e := range entries {
		source := e.Source
		if source == "" {
			source = "agent"
		}
		tag := e.Tag
		if tag == "" {
			tag = "?"
		}
		key := tag + "\x1f" + source
		a, ok := groups[key]
		if !ok {
			a = &acc{row: AdoptionRow{Tag: tag, Source: source}, sids: map[string]struct{}{}}
			groups[key] = a
		}
		a.row.Calls++
		a.row.Tokens += e.OutputTokens
		if e.ExitCode != 0 {
			a.row.Errors++
		}
		if e.SessionID != "" {
			a.sids[e.SessionID] = struct{}{}
		}
	}
	out := make([]AdoptionRow, 0, len(groups))
	for _, a := range groups {
		a.row.Sessions = len(a.sids)
		if a.row.Calls > 0 {
			a.row.FailedPct = float64(a.row.Errors) * 100 / float64(a.row.Calls)
		}
		out = append(out, a.row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}

// extractInputs pulls the canonical input list out of an entry's summary
// map. We tolerate either []string or []any in the JSON shape.
func extractInputs(e calllog.Entry) []string {
	if e.Summary == nil {
		return nil
	}
	raw, ok := e.Summary["inputs"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

type recentView struct {
	Entries []calllog.Entry
	Total   int
}

type statsView struct {
	Stats     []Aggregate
	TopInputs []InputCount
	Total     int
}

type histogramView struct {
	Bucket  string
	Buckets []HistogramRow
	Total   int
}

type adoptionView struct {
	Rows  []AdoptionRow
	Total int
}

func render(w io.Writer, format string, v any) error {
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

var (
	recentFields    = []string{"ts", "tool", "dur_ms", "exit", "out_bytes", "out_tokens", "args"}
	statsFields     = []string{"tool", "calls", "errors", "total_ms", "mean_ms", "p50_ms", "p95_ms", "max_ms", "total_out_bytes", "total_out_tokens", "first", "last"}
	topFields       = []string{"tool", "input", "count"}
	histogramFields = []string{"bucket", "count", "total_ms", "total_out_bytes", "total_out_tokens"}
	adoptionFields  = []string{"tag", "source", "calls", "sessions", "errors", "failed_pct", "out_tokens"}
)

func renderTOON(w io.Writer, v any) error {
	switch r := v.(type) {
	case recentView:
		fmt.Fprintf(w, "recent[%d]{%s}:\n", len(r.Entries), strings.Join(recentFields, ","))
		for _, e := range r.Entries {
			fmt.Fprintf(w, "%s%s,%s,%d,%d,%d,%d,%s\n",
				toon.Indent,
				toon.Scalar(e.Timestamp),
				toon.Scalar(e.Tool),
				e.DurationMs,
				e.ExitCode,
				e.OutputBytes,
				e.OutputTokens,
				toon.Scalar(strings.Join(e.Args, " ")),
			)
		}
		if r.Total > len(r.Entries) {
			fmt.Fprintf(w, "# +%d older entries not shown\n", r.Total-len(r.Entries))
		}
	case statsView:
		fmt.Fprintf(w, "stats[%d]{%s}:\n", len(r.Stats), strings.Join(statsFields, ","))
		for _, a := range r.Stats {
			fmt.Fprintf(w, "%s%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%s,%s\n",
				toon.Indent,
				toon.Scalar(a.Tool),
				a.Calls, a.Errors,
				a.TotalMs, a.MeanMs, a.P50Ms, a.P95Ms, a.MaxMs,
				a.TotalBytes, a.TotalTokens,
				toon.Scalar(a.First), toon.Scalar(a.Last),
			)
		}
		if len(r.TopInputs) > 0 {
			fmt.Fprintf(w, "top_inputs[%d]{%s}:\n", len(r.TopInputs), strings.Join(topFields, ","))
			for _, ic := range r.TopInputs {
				fmt.Fprintf(w, "%s%s,%s,%d\n",
					toon.Indent, toon.Scalar(ic.Tool), toon.Scalar(ic.Input), ic.Count)
			}
		}
		fmt.Fprintf(w, "total_calls: %d\n", r.Total)
	case histogramView:
		fmt.Fprintf(w, "histogram[%d]{%s}: # bucket=%s\n",
			len(r.Buckets), strings.Join(histogramFields, ","), r.Bucket)
		for _, b := range r.Buckets {
			fmt.Fprintf(w, "%s%s,%d,%d,%d,%d\n",
				toon.Indent,
				toon.Scalar(b.Bucket),
				b.Count, b.TotalMs, b.TotalBytes, b.TotalTokens,
			)
		}
		fmt.Fprintf(w, "total_calls: %d\n", r.Total)
	case adoptionView:
		fmt.Fprintf(w, "adoption[%d]{%s}:\n", len(r.Rows), strings.Join(adoptionFields, ","))
		for _, a := range r.Rows {
			fmt.Fprintf(w, "%s%s,%s,%d,%d,%d,%.1f,%d\n",
				toon.Indent,
				toon.Scalar(a.Tag), toon.Scalar(a.Source),
				a.Calls, a.Sessions, a.Errors, a.FailedPct, a.Tokens,
			)
		}
		fmt.Fprintf(w, "total_calls: %d\n", r.Total)
	}
	return nil
}

func renderMarkdown(w io.Writer, v any) error {
	switch r := v.(type) {
	case recentView:
		fmt.Fprintf(w, "# Recent invocations (%d shown / %d total)\n\n", len(r.Entries), r.Total)
		for _, e := range r.Entries {
			status := "ok"
			if e.ExitCode != 0 {
				status = "FAIL"
			}
			fmt.Fprintf(w, "- `%s` **%s** %dms %s — %s\n",
				e.Timestamp, e.Tool, e.DurationMs, status, strings.Join(e.Args, " "))
		}
	case statsView:
		fmt.Fprintf(w, "# Stats (%d calls total)\n\n", r.Total)
		fmt.Fprintln(w, "| Tool | Calls | Errors | Total | Mean | P50 | P95 | Max | Bytes | Tokens |")
		fmt.Fprintln(w, "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
		for _, a := range r.Stats {
			fmt.Fprintf(w, "| %s | %d | %d | %s | %s | %s | %s | %s | %s | %d |\n",
				a.Tool, a.Calls, a.Errors,
				fmtMs(a.TotalMs), fmtMs(a.MeanMs), fmtMs(a.P50Ms), fmtMs(a.P95Ms), fmtMs(a.MaxMs),
				fmtBytes(a.TotalBytes), a.TotalTokens,
			)
		}
		if len(r.TopInputs) > 0 {
			fmt.Fprintln(w, "\n## Top inputs")
			for _, ic := range r.TopInputs {
				fmt.Fprintf(w, "- `%s` × %d (%s)\n", ic.Input, ic.Count, ic.Tool)
			}
		}
	case histogramView:
		fmt.Fprintf(w, "# Histogram by %s (%d calls total)\n\n", r.Bucket, r.Total)
		fmt.Fprintln(w, "| Bucket | Calls | Total time | Tokens |")
		fmt.Fprintln(w, "| --- | ---: | ---: | ---: |")
		for _, b := range r.Buckets {
			fmt.Fprintf(w, "| %s | %d | %s | %d |\n",
				b.Bucket, b.Count, fmtMs(b.TotalMs), b.TotalTokens)
		}
	case adoptionView:
		fmt.Fprintf(w, "# Adoption (%d calls total)\n\n", r.Total)
		fmt.Fprintln(w, "| Project | Source | Calls | Sessions | Errors | Failed% | Tokens |")
		fmt.Fprintln(w, "| --- | --- | ---: | ---: | ---: | ---: | ---: |")
		for _, a := range r.Rows {
			fmt.Fprintf(w, "| %s | %s | %d | %d | %d | %.1f%% | %d |\n",
				a.Tag, a.Source, a.Calls, a.Sessions, a.Errors, a.FailedPct, a.Tokens)
		}
	}
	return nil
}

func fmtMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.2fs", float64(ms)/1000)
}

func fmtBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
}
