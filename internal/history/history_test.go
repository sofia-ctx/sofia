package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
)

func TestRecentWindow_NewestFirst(t *testing.T) {
	entries := []calllog.Entry{
		{Tool: "a", Timestamp: "2026-01-01T00:00:00Z"},
		{Tool: "b", Timestamp: "2026-01-02T00:00:00Z"},
		{Tool: "c", Timestamp: "2026-01-03T00:00:00Z"},
		{Tool: "d", Timestamp: "2026-01-04T00:00:00Z"},
	}

	// limit 0 = all, reversed to newest-first.
	all := recentWindow(entries, 0)
	if got := []string{all[0].Tool, all[1].Tool, all[2].Tool, all[3].Tool}; got[0] != "d" || got[3] != "a" {
		t.Errorf("newest-first order = %v, want [d c b a]", got)
	}

	// limit 2 keeps the two most recent, newest first.
	last2 := recentWindow(entries, 2)
	if len(last2) != 2 || last2[0].Tool != "d" || last2[1].Tool != "c" {
		t.Errorf("recentWindow(limit=2) = %+v, want [d c]", last2)
	}

	// the input slice must not be mutated.
	if entries[0].Tool != "a" || entries[3].Tool != "d" {
		t.Errorf("recentWindow mutated its input: %+v", entries)
	}
}

func TestReadEntries_FailedOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", dir)
	log := `{"ts":"2026-01-01T00:00:00Z","tool":"a","exit":0}
{"ts":"2026-01-02T00:00:00Z","tool":"b","exit":1}
{"ts":"2026-01-03T00:00:00Z","tool":"c","exit":0}
{"ts":"2026-01-04T00:00:00Z","tool":"d","exit":2}
`
	if err := os.WriteFile(filepath.Join(dir, "calls.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	all, err := readEntries(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("readEntries(all) = %d entries, want 4", len(all))
	}

	failed, err := readEntries(Filter{FailedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 2 {
		t.Fatalf("readEntries(FailedOnly) = %d entries, want 2", len(failed))
	}
	for _, e := range failed {
		if e.ExitCode == 0 {
			t.Errorf("FailedOnly returned a success entry: %+v", e)
		}
	}
}

func TestReadEntries_SessionAndTag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", dir)
	log := `{"ts":"2026-01-01T00:00:00Z","tool":"a","sid":"abc123","tag":"app1"}
{"ts":"2026-01-02T00:00:00Z","tool":"b","sid":"abc999","tag":"app1"}
{"ts":"2026-01-03T00:00:00Z","tool":"c","sid":"def456","tag":"app2"}
{"ts":"2026-01-04T00:00:00Z","tool":"d"}
`
	if err := os.WriteFile(filepath.Join(dir, "calls.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := readEntries(Filter{Session: "abc"}) // prefix matches abc123 & abc999
	if err != nil {
		t.Fatal(err)
	}
	if len(sess) != 2 {
		t.Fatalf("Session prefix abc = %d entries, want 2", len(sess))
	}

	tag, err := readEntries(Filter{Tag: "app2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tag) != 1 || tag[0].Tool != "c" {
		t.Fatalf("Tag app2 = %+v, want single tool c", tag)
	}
}

func TestBuildAdoption(t *testing.T) {
	entries := []calllog.Entry{
		{Tool: "code", Source: "agent", SessionID: "s1", Tag: "app1", ExitCode: 0, OutputTokens: 100},
		{Tool: "grep", Source: "agent", SessionID: "s1", Tag: "app1", ExitCode: 1, OutputTokens: 50},
		{Tool: "code", Source: "agent", SessionID: "s2", Tag: "app1", ExitCode: 0, OutputTokens: 30},
		{Tool: "code", Source: "manual", SessionID: "", Tag: "app1", ExitCode: 0, OutputTokens: 10},
		{Tool: "code", Source: "", SessionID: "s3", Tag: "", ExitCode: 0, OutputTokens: 5}, // legacy → agent, "?"
	}
	rows := buildAdoption(entries)

	// app1/agent should be the busiest row (3 calls).
	if rows[0].Tag != "app1" || rows[0].Source != "agent" || rows[0].Calls != 3 {
		t.Fatalf("top row = %+v, want app1/agent/3", rows[0])
	}
	if rows[0].Sessions != 2 {
		t.Errorf("app1/agent distinct sessions = %d, want 2", rows[0].Sessions)
	}
	if rows[0].Errors != 1 {
		t.Errorf("app1/agent errors = %d, want 1", rows[0].Errors)
	}
	if rows[0].FailedPct < 33.0 || rows[0].FailedPct > 33.4 {
		t.Errorf("app1/agent failed%% = %.2f, want ~33.3", rows[0].FailedPct)
	}
	if rows[0].Tokens != 180 {
		t.Errorf("app1/agent tokens = %d, want 180", rows[0].Tokens)
	}

	// Find the legacy/untagged row: source coerced to agent, tag to "?".
	var found bool
	for _, r := range rows {
		if r.Tag == "?" {
			found = true
			if r.Source != "agent" {
				t.Errorf("untagged source = %q, want agent", r.Source)
			}
		}
	}
	if !found {
		t.Error("expected a '?'-tag row for the untagged entry")
	}
}

func TestParseSince(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"30m", 30 * time.Minute, false},
		{"1h", time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"3d", 3 * 24 * time.Hour, false},
		{"", 0, true},
		{"abc", 0, true},
		{"3w", 0, true},
	}
	for _, c := range cases {
		got, err := parseSince(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSince(%q) expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSince(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseSince(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPercentile(t *testing.T) {
	cases := []struct {
		name string
		durs []int64
		p    int
		want int64
	}{
		{"empty", nil, 50, 0},
		{"single", []int64{10}, 50, 10},
		{"two_p50", []int64{10, 20}, 50, 20},
		{"five_p50", []int64{1, 2, 3, 4, 5}, 50, 3},
		{"five_p95", []int64{1, 2, 3, 4, 5}, 95, 5},
		{"twenty_p95", []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, 95, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := percentile(c.durs, c.p)
			if got != c.want {
				t.Errorf("percentile(%v, %d) = %d, want %d", c.durs, c.p, got, c.want)
			}
		})
	}
}

func TestAggregate(t *testing.T) {
	entries := []calllog.Entry{
		{Tool: "a", Timestamp: "2026-01-01T00:00:00Z", DurationMs: 100, ExitCode: 0, OutputBytes: 1000, OutputTokens: 250,
			Summary: map[string]any{"inputs": []any{"FOO"}}},
		{Tool: "a", Timestamp: "2026-01-02T00:00:00Z", DurationMs: 200, ExitCode: 0, OutputBytes: 2000, OutputTokens: 500,
			Summary: map[string]any{"inputs": []any{"FOO", "BAR"}}},
		{Tool: "a", Timestamp: "2026-01-03T00:00:00Z", DurationMs: 300, ExitCode: 1, OutputBytes: 0},
		{Tool: "b", Timestamp: "2026-01-04T00:00:00Z", DurationMs: 50},
	}
	aggs, top := aggregate(entries, 5)
	if len(aggs) != 2 {
		t.Fatalf("expected 2 tool aggregates, got %d", len(aggs))
	}
	// "a" has 3 calls (sorted by count desc), "b" has 1.
	a := aggs[0]
	if a.Tool != "a" {
		t.Errorf("expected tool=a first, got %q", a.Tool)
	}
	if a.Calls != 3 {
		t.Errorf("Calls = %d, want 3", a.Calls)
	}
	if a.Errors != 1 {
		t.Errorf("Errors = %d, want 1", a.Errors)
	}
	if a.TotalMs != 600 || a.MeanMs != 200 || a.MaxMs != 300 {
		t.Errorf("totals: total=%d mean=%d max=%d", a.TotalMs, a.MeanMs, a.MaxMs)
	}
	if a.TotalBytes != 3000 {
		t.Errorf("TotalBytes = %d, want 3000", a.TotalBytes)
	}
	if a.TotalTokens != 750 {
		t.Errorf("TotalTokens = %d, want 750", a.TotalTokens)
	}
	if a.First != "2026-01-01T00:00:00Z" || a.Last != "2026-01-03T00:00:00Z" {
		t.Errorf("First/Last: %q / %q", a.First, a.Last)
	}

	// Top inputs across the data: FOO appears 2x, BAR appears 1x — both under tool "a".
	if len(top) == 0 {
		t.Fatal("expected top inputs to be populated")
	}
	if top[0].Input != "FOO" || top[0].Count != 2 {
		t.Errorf("top[0] = %+v, want FOO x2", top[0])
	}
}

func TestExtractInputs(t *testing.T) {
	cases := []struct {
		name string
		in   calllog.Entry
		want []string
	}{
		{"nil_summary", calllog.Entry{}, nil},
		{"missing_inputs", calllog.Entry{Summary: map[string]any{"other": 1}}, nil},
		{"any_slice", calllog.Entry{Summary: map[string]any{"inputs": []any{"a", "b"}}}, []string{"a", "b"}},
		{"string_slice", calllog.Entry{Summary: map[string]any{"inputs": []string{"x"}}}, []string{"x"}},
		{"non_string_elements_dropped",
			calllog.Entry{Summary: map[string]any{"inputs": []any{"a", 1, "b"}}}, []string{"a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractInputs(c.in)
			if !equalStrs(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseHistogramBucket(t *testing.T) {
	cases := []struct {
		in      string
		want    HistogramBucket
		wantErr bool
	}{
		{"hour", HistogramHour, false},
		{"HOUR", HistogramHour, false},
		{"h", HistogramHour, false},
		{"day", HistogramDay, false},
		{"d", HistogramDay, false},
		{"week", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := parseHistogramBucket(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseHistogramBucket(%q) expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHistogramBucket(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseHistogramBucket(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBuildHistogram_Hour(t *testing.T) {
	entries := []calllog.Entry{
		{Timestamp: "2026-05-21T03:15:00Z", DurationMs: 100, OutputTokens: 10},
		{Timestamp: "2026-05-21T03:45:00Z", DurationMs: 200, OutputTokens: 20},
		{Timestamp: "2026-05-21T10:00:00Z", DurationMs: 50, OutputTokens: 5},
		{Timestamp: "bogus", DurationMs: 999}, // dropped silently
	}
	rows := buildHistogram(entries, HistogramHour)
	if len(rows) != 2 {
		t.Fatalf("expected 2 hour buckets, got %d", len(rows))
	}
	if rows[0].Bucket != "03" || rows[0].Count != 2 || rows[0].TotalMs != 300 || rows[0].TotalTokens != 30 {
		t.Errorf("bucket 03: %+v", rows[0])
	}
	if rows[1].Bucket != "10" || rows[1].Count != 1 || rows[1].TotalTokens != 5 {
		t.Errorf("bucket 10: %+v", rows[1])
	}
}

func TestBuildHistogram_Day(t *testing.T) {
	entries := []calllog.Entry{
		{Timestamp: "2026-05-20T23:00:00Z", DurationMs: 100},
		{Timestamp: "2026-05-21T01:00:00Z", DurationMs: 200},
		{Timestamp: "2026-05-21T11:00:00Z", DurationMs: 300},
	}
	rows := buildHistogram(entries, HistogramDay)
	if len(rows) != 2 {
		t.Fatalf("expected 2 day buckets, got %d", len(rows))
	}
	if rows[0].Bucket != "2026-05-20" || rows[0].Count != 1 {
		t.Errorf("day 20: %+v", rows[0])
	}
	if rows[1].Bucket != "2026-05-21" || rows[1].Count != 2 {
		t.Errorf("day 21: %+v", rows[1])
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
