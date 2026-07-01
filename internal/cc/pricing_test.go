package cc

import "testing"

func TestPriceFor(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		wantOK  bool
		wantOut float64 // output $/1M, only checked when wantOK
	}{
		{"exact match", "claude-opus-4-8", true, 25.00},
		{"dated snapshot resolves via prefix", "claude-haiku-4-5-20251001", true, 5.00},
		{"another dated snapshot", "claude-opus-4-1-20250805", true, 25.00},
		{"sonnet 5 exact", "claude-sonnet-5", true, 15.00},
		{"synthetic housekeeping model is unpriced", "<synthetic>", false, 0},
		{"unknown future model is unpriced, not guessed", "claude-opus-9-9", true /* placeholder overwritten below */, 0},
		{"empty model is unpriced", "", false, 0},
	}
	// claude-opus-9-9 must NOT resolve — fix the fixture above (it's a
	// deliberately unrecognized string; separate case to keep the table
	// readable without a stray boolean flip).
	cases[5].wantOK = false

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			price, ok := priceFor(c.model)
			if ok != c.wantOK {
				t.Fatalf("priceFor(%q) ok = %v, want %v", c.model, ok, c.wantOK)
			}
			if ok && price.Output != c.wantOut {
				t.Errorf("priceFor(%q).Output = %v, want %v", c.model, price.Output, c.wantOut)
			}
		})
	}
}

func TestCachePriceMultipliers(t *testing.T) {
	p := cachePrice(4.00, 20.00)
	if p.Input != 4.00 || p.Output != 20.00 {
		t.Fatalf("cachePrice base fields = %+v", p)
	}
	if got, want := p.CacheWrite, 5.00; got != want {
		t.Errorf("CacheWrite = %v, want %v (1.25x input)", got, want)
	}
	if got, want := p.CacheRead, 0.40; got != want {
		t.Errorf("CacheRead = %v, want %v (0.1x input)", got, want)
	}
}

func TestModelPricesNoAmbiguousPrefixes(t *testing.T) {
	// priceFor's prefix fallback assumes at most one known key can prefix-
	// match any given model string. Guard the table against a future
	// addition silently breaking that assumption.
	for a := range modelPrices {
		for b := range modelPrices {
			if a == b {
				continue
			}
			if len(a) < len(b) && b[:len(a)+1] == a+"-" {
				t.Errorf("model key %q is an ambiguous prefix of %q", a, b)
			}
		}
	}
}

func TestCost(t *testing.T) {
	if got, want := cost(1_000_000, 3.0), 3.0; got != want {
		t.Errorf("cost(1M, $3/M) = %v, want %v", got, want)
	}
	if got, want := cost(500_000, 3.0), 1.5; got != want {
		t.Errorf("cost(500k, $3/M) = %v, want %v", got, want)
	}
	if got := cost(0, 3.0); got != 0 {
		t.Errorf("cost(0, ...) = %v, want 0", got)
	}
}
