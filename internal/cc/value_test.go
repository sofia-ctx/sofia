package cc

import (
	"strings"
	"testing"
	"time"
)

func TestBuildValue(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	const days = 7
	// curStart  = now - 7d  (2026-06-25 12:00)
	// prevStart = now - 14d (2026-06-18 12:00)

	sessions := []*Session{
		{ // current window, priced (sonnet 4.6): 1M output tokens -> $15
			ID: "s1", Model: "claude-sonnet-4-6",
			End:          now.AddDate(0, 0, -1),
			OutputTokens: 1_000_000,
		},
		{ // previous window, priced (opus 4.8): 1M output tokens -> $25
			ID: "s2", Model: "claude-opus-4-8",
			End:          now.AddDate(0, 0, -8),
			OutputTokens: 1_000_000,
		},
		{ // outside both windows entirely -> must not be counted anywhere
			ID: "s3", Model: "claude-opus-4-8",
			End:          now.AddDate(0, 0, -20),
			OutputTokens: 9_999_999,
		},
		{ // current window, unpriced model: tokens counted, $ excluded
			ID: "s4", Model: "claude-nope-1",
			End:             now.AddDate(0, 0, -2),
			CacheReadTokens: 2_000_000,
		},
		{ // previous window, unpriced model
			ID: "s5", Model: "claude-nope-1",
			End:         now.AddDate(0, 0, -9),
			InputTokens: 500_000,
		},
		{ // no End timestamp -> falls back to Start
			ID: "s6", Model: "claude-sonnet-4-6",
			Start:        now.AddDate(0, 0, -1),
			OutputTokens: 200_000, // -> $3
		},
	}

	v := buildValue(sessions, now, days)

	if v.Current.Sessions != 3 {
		t.Fatalf("Current.Sessions = %d, want 3 (s1, s4, s6)", v.Current.Sessions)
	}
	if want := 15.00 + 3.00; v.Current.CostUSD != want {
		t.Errorf("Current.CostUSD = %v, want %v", v.Current.CostUSD, want)
	}
	if v.Current.UnpricedTokens != 2_000_000 {
		t.Errorf("Current.UnpricedTokens = %d, want 2000000", v.Current.UnpricedTokens)
	}

	if v.Previous.Sessions != 2 {
		t.Fatalf("Previous.Sessions = %d, want 2 (s2, s5)", v.Previous.Sessions)
	}
	if v.Previous.CostUSD != 25.00 {
		t.Errorf("Previous.CostUSD = %v, want 25", v.Previous.CostUSD)
	}
	if v.Previous.UnpricedTokens != 500_000 {
		t.Errorf("Previous.UnpricedTokens = %d, want 500000", v.Previous.UnpricedTokens)
	}

	wantDelta := (15.00 + 3.00) - 25.00
	if v.DeltaUSD != wantDelta {
		t.Errorf("DeltaUSD = %v, want %v", v.DeltaUSD, wantDelta)
	}
	wantPct := wantDelta / 25.00 * 100
	if v.DeltaPct != wantPct {
		t.Errorf("DeltaPct = %v, want %v", v.DeltaPct, wantPct)
	}

	// by_type: cache_read (2M, from unpriced s4) outranks output (1.2M
	// combined from s1+s6) by token volume, so it sorts first.
	if len(v.Current.ByType) != 2 {
		t.Fatalf("Current.ByType = %+v, want 2 entries", v.Current.ByType)
	}
	if v.Current.ByType[0].Type != "cache_read" || v.Current.ByType[0].Tokens != 2_000_000 {
		t.Errorf("Current.ByType[0] = %+v, want cache_read/2000000", v.Current.ByType[0])
	}
	if v.Current.ByType[1].Type != "output" || v.Current.ByType[1].Tokens != 1_200_000 || v.Current.ByType[1].CostUSD != 18.00 {
		t.Errorf("Current.ByType[1] = %+v, want output/1200000/18.00", v.Current.ByType[1])
	}

	// by_model: current window only (s1+s6 merge under sonnet-4-6; s4 under
	// the unpriced model), sorted by cost_usd desc.
	if len(v.ByModel) != 2 {
		t.Fatalf("ByModel = %+v, want 2 entries", v.ByModel)
	}
	if v.ByModel[0].Model != "claude-sonnet-4-6" || v.ByModel[0].Sessions != 2 || v.ByModel[0].CostUSD != 18.00 || !v.ByModel[0].Priced {
		t.Errorf("ByModel[0] = %+v, want claude-sonnet-4-6/2/18.00/priced", v.ByModel[0])
	}
	if v.ByModel[1].Model != "claude-nope-1" || v.ByModel[1].Priced {
		t.Errorf("ByModel[1] = %+v, want claude-nope-1/unpriced", v.ByModel[1])
	}
}

func TestBuildValueNoPreviousCostAvoidsDivByZero(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	sessions := []*Session{
		{ID: "s1", Model: "claude-opus-4-8", End: now.AddDate(0, 0, -1), OutputTokens: 100_000},
	}
	v := buildValue(sessions, now, 7)
	if v.Previous.CostUSD != 0 {
		t.Fatalf("Previous.CostUSD = %v, want 0", v.Previous.CostUSD)
	}
	if v.DeltaPct != 0 {
		t.Errorf("DeltaPct = %v, want 0 when Previous.CostUSD is 0", v.DeltaPct)
	}
	if v.DeltaUSD != v.Current.CostUSD {
		t.Errorf("DeltaUSD = %v, want equal to Current.CostUSD (%v)", v.DeltaUSD, v.Current.CostUSD)
	}
}

// TestValueQuotaFlagRegistered locks --quota's presence and its off-by-default,
// so the $ report stays the command's default behaviour.
func TestValueQuotaFlagRegistered(t *testing.T) {
	f := newValueCommand().Flags().Lookup("quota")
	if f == nil {
		t.Fatal("expected --quota flag to be registered")
	}
	if f.DefValue != "false" {
		t.Errorf("--quota default = %q, want false", f.DefValue)
	}
}

// TestValueQuotaProjectConflict: --project filters Claude Code transcripts,
// which the quota report never reads (it reads calls.jsonl instead) — the
// combination must error rather than silently ignore one of the two flags.
func TestValueQuotaProjectConflict(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	cmd := newValueCommand()
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--quota", "--project", "myapp"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--project applies to the $ report only") {
		t.Fatalf("err = %v, want the --project/--quota conflict message", err)
	}
}

func TestBuildValueEmptySessions(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	v := buildValue(nil, now, 7)
	if v.Current.Sessions != 0 || v.Previous.Sessions != 0 {
		t.Fatalf("expected zero sessions on both sides, got %+v", v)
	}
	if v.ByModel != nil {
		t.Errorf("ByModel = %+v, want nil/empty", v.ByModel)
	}
}
