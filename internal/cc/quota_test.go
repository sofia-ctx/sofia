package cc

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
)

// writeCallLog hand-writes entries into a calls.jsonl fixture using the real
// calllog.Entry type (so a field-name/type drift in calllog.go fails to
// compile rather than silently producing a fixture calllog.Read can't
// parse), then points SOFIA_LOG_DIR at it so calllog.Read() — the same
// reader runQuota uses — is what each test actually exercises.
func writeCallLog(t *testing.T, entries []calllog.Entry) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SOFIA_LOG_DIR", dir)
	f, err := os.Create(filepath.Join(dir, "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
}

// agentEntry builds a minimal real-agent-traffic entry at ts, with the given
// out_tokens and summary (nil for a summary-less entry, e.g. a validation
// failure).
func agentEntry(ts time.Time, outTokens int64, summary map[string]any) calllog.Entry {
	return calllog.Entry{
		Timestamp:    ts.UTC().Format(time.RFC3339Nano),
		Tool:         "code",
		Source:       "agent",
		Args:         []string{"x.go"},
		OutputTokens: outTokens,
		Summary:      summary,
	}
}

func readQuotaEntries(t *testing.T) []calllog.Entry {
	t.Helper()
	entries, err := calllog.Read()
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestBuildQuotaBaselineMath covers the core savings arithmetic: a full call
// (tok_raw) with real savings, a full call whose baseline doesn't beat its
// own output (must clamp to 0, never negative), a dedup stub (tok_rep), and
// an entry with no baseline at all (grep-shaped: counts toward calls/emitted
// only). All within the default 7-day window.
func TestBuildQuotaBaselineMath(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	writeCallLog(t, []calllog.Entry{
		agentEntry(now.Add(-1*time.Hour), 100, map[string]any{"files": 1, "raw": 0, "tok_raw": 500}),      // saved 400
		agentEntry(now.Add(-2*time.Hour), 50, map[string]any{"files": 1, "raw": 0, "tok_raw": 10}),        // saved clamps to 0
		agentEntry(now.Add(-3*time.Hour), 5, map[string]any{"dedup": true, "dup_of": 1, "tok_rep": 300}),  // saved 295
		agentEntry(now.Add(-4*time.Hour), 80, map[string]any{"patterns": []string{"x"}, "total_hits": 3}), // no baseline
	})

	q := buildQuota(readQuotaEntries(t), now, 7)

	if q.Calls != 4 {
		t.Fatalf("Calls = %d, want 4", q.Calls)
	}
	if q.WithBaseline != 3 {
		t.Errorf("WithBaseline = %d, want 3", q.WithBaseline)
	}
	if q.DedupStubs != 1 {
		t.Errorf("DedupStubs = %d, want 1", q.DedupStubs)
	}
	if q.EmittedTokens != 235 { // 100+50+5+80
		t.Errorf("EmittedTokens = %d, want 235", q.EmittedTokens)
	}
	if q.BaselineTokens != 810 { // 500+10+300
		t.Errorf("BaselineTokens = %d, want 810", q.BaselineTokens)
	}
	if q.SavedTokens != 695 { // 400+0+295
		t.Errorf("SavedTokens = %d, want 695", q.SavedTokens)
	}
	wantPct := float64(695) / float64(810) * 100
	if q.SavedPct != wantPct {
		t.Errorf("SavedPct = %v, want %v", q.SavedPct, wantPct)
	}
}

// TestBuildQuotaSourceFilter: only Source == "agent" counts — manual/test
// traffic must not pollute the report, baseline included.
func TestBuildQuotaSourceFilter(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	manual := agentEntry(now.Add(-1*time.Hour), 10, map[string]any{"tok_raw": 1000})
	manual.Source = "manual"
	testSrc := agentEntry(now.Add(-1*time.Hour), 10, map[string]any{"tok_raw": 1000})
	testSrc.Source = "test"
	writeCallLog(t, []calllog.Entry{
		manual,
		testSrc,
		agentEntry(now.Add(-1*time.Hour), 10, map[string]any{"tok_raw": 100}), // the only real agent call
	})

	q := buildQuota(readQuotaEntries(t), now, 7)
	if q.Calls != 1 {
		t.Fatalf("Calls = %d, want 1 (only source=agent counts)", q.Calls)
	}
	if q.BaselineTokens != 100 {
		t.Errorf("BaselineTokens = %d, want 100 (manual/test entries must not leak in)", q.BaselineTokens)
	}
}

// TestBuildQuotaDaysCutoff: an entry older than --days must be dropped, even
// though it's real agent traffic with a baseline.
func TestBuildQuotaDaysCutoff(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	writeCallLog(t, []calllog.Entry{
		agentEntry(now.Add(-3*24*time.Hour), 10, map[string]any{"tok_raw": 100}),  // inside 7d
		agentEntry(now.Add(-30*24*time.Hour), 10, map[string]any{"tok_raw": 999}), // outside 7d
	})

	q := buildQuota(readQuotaEntries(t), now, 7)
	if q.Calls != 1 {
		t.Fatalf("Calls = %d, want 1 (30-day-old entry must be cut off)", q.Calls)
	}
	if q.BaselineTokens != 100 {
		t.Errorf("BaselineTokens = %d, want 100 (the old entry's 999 must not count)", q.BaselineTokens)
	}
}

// TestBuildQuotaBusiestWindow pins the two-pointer sliding-window scan: two
// clusters of saves, far enough apart that they can't share a single 5h
// window, and close enough within each cluster that the scan must actually
// slide rather than bucket by a fixed clock grid.
func TestBuildQuotaBusiestWindow(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	a1 := now.Add(-10 * time.Hour) // cluster A: 02:00 saved=10
	a2 := now.Add(-8 * time.Hour)  //            04:00 saved=15 (span vs a1: 2h)
	b1 := now.Add(-4 * time.Hour)  // cluster B: 08:00 saved=20 (span vs a2: 4h — still <=5h)
	b2 := now.Add(-1 * time.Hour)  //            11:00 saved=40 (span vs b1: 3h; span vs a2: 7h > 5h)

	writeCallLog(t, []calllog.Entry{
		agentEntry(a1, 0, map[string]any{"tok_raw": 10}),
		agentEntry(a2, 0, map[string]any{"tok_raw": 15}),
		agentEntry(b1, 0, map[string]any{"tok_raw": 20}),
		agentEntry(b2, 0, map[string]any{"tok_raw": 40}),
	})

	q := buildQuota(readQuotaEntries(t), now, 7)
	// Best window: [b1,b2] (3h apart) = 20+40 = 60, beating [a2,b1] (4h
	// apart) = 15+20 = 35 and [a1,a2] (2h apart) = 10+15 = 25.
	if q.BusiestSaved != 60 {
		t.Errorf("BusiestSaved = %d, want 60", q.BusiestSaved)
	}
	if !q.BusiestStart.Equal(b1) {
		t.Errorf("BusiestStart = %v, want %v (b1)", q.BusiestStart, b1)
	}
}

// TestBuildQuotaNoSavingsOmitsBusiest: when nothing was saved, there's no
// busiest window to report.
func TestBuildQuotaNoSavingsOmitsBusiest(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	writeCallLog(t, []calllog.Entry{
		agentEntry(now.Add(-1*time.Hour), 80, map[string]any{"total_hits": 3}), // no baseline at all
	})

	q := buildQuota(readQuotaEntries(t), now, 7)
	if q.BusiestSaved != 0 || !q.BusiestStart.IsZero() {
		t.Errorf("BusiestSaved/Start = %d/%v, want 0/zero", q.BusiestSaved, q.BusiestStart)
	}
}

// TestRenderQuotaTOON pins the exact rendering, including formatK's
// compaction and the busiest-window line.
func TestRenderQuotaTOON(t *testing.T) {
	q := Quota{
		Days: 7, Calls: 214, WithBaseline: 118, DedupStubs: 14,
		EmittedTokens: 61000, BaselineTokens: 183000, SavedTokens: 122000,
		SavedPct:     float64(122000) / float64(183000) * 100,
		BusiestStart: time.Date(2026, 7, 5, 14, 2, 0, 0, time.UTC),
		BusiestSaved: 41000,
	}
	var buf bytes.Buffer
	renderQuotaTOON(&buf, q)
	want := "quota (source=agent, last 7d): 214 calls, 118 with baseline, 14 dedup stubs\n" +
		"emitted ≈61K tok · baseline ≈183K · saved ≈122K (67%)\n" +
		"busiest 5h window: 2026-07-05 14:02 +5h — saved ≈41K tok\n"
	if got := buf.String(); got != want {
		t.Errorf("renderQuotaTOON =\n%q\nwant\n%q", got, want)
	}
}

// TestRenderQuotaTOONOmitsBusiestLine: no savings at all → no busiest line.
func TestRenderQuotaTOONOmitsBusiestLine(t *testing.T) {
	q := Quota{Days: 7, Calls: 3, EmittedTokens: 30}
	var buf bytes.Buffer
	renderQuotaTOON(&buf, q)
	if got := buf.String(); strings.Contains(got, "busiest") {
		t.Errorf("busiest line should be omitted with zero savings, got:\n%s", got)
	}
}

func TestRenderQuotaMarkdown(t *testing.T) {
	q := Quota{
		Days:  7,
		Since: time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC), Until: time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
		Calls: 214, WithBaseline: 118, DedupStubs: 14,
		EmittedTokens: 61000, BaselineTokens: 183000, SavedTokens: 122000,
		SavedPct:     float64(122000) / float64(183000) * 100,
		BusiestStart: time.Date(2026, 7, 5, 14, 2, 0, 0, time.UTC),
		BusiestSaved: 41000,
	}
	var buf bytes.Buffer
	renderQuotaMarkdown(&buf, q)
	want := "# sf cc value --quota\n\n" +
		"Source: **agent** only, last **7** day(s) (2026-06-29 12:00 .. 2026-07-06 12:00)\n\n" +
		"- Calls: **214** (118 with a savings baseline, 14 dedup stubs)\n" +
		"- Emitted ≈61K tok · Baseline ≈183K tok · Saved **≈122K tok (67%)**\n" +
		"- Busiest 5h window: 2026-07-05 14:02 +5h — saved ≈41K tok\n"
	if got := buf.String(); got != want {
		t.Errorf("renderQuotaMarkdown =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderQuotaJSON(t *testing.T) {
	q := Quota{
		Days: 7, Since: time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC), Until: time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
		Calls: 214, WithBaseline: 118, DedupStubs: 14,
		EmittedTokens: 61000, BaselineTokens: 183000, SavedTokens: 122000,
		SavedPct:     float64(122000) / float64(183000) * 100,
		BusiestStart: time.Date(2026, 7, 5, 14, 2, 0, 0, time.UTC),
		BusiestSaved: 41000,
	}
	b, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	var got Quota
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Calls != q.Calls || got.SavedTokens != q.SavedTokens || got.BusiestSaved != q.BusiestSaved {
		t.Errorf("round-tripped Quota = %+v, want %+v", got, q)
	}
	if !got.BusiestStart.Equal(q.BusiestStart) {
		t.Errorf("BusiestStart round-trip = %v, want %v", got.BusiestStart, q.BusiestStart)
	}
}
