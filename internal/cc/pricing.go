package cc

import "strings"

// ModelPrice is USD price per 1,000,000 tokens, by token type, for one
// Claude model.
type ModelPrice struct {
	Input      float64 // $ / 1M base (uncached) input tokens
	Output     float64 // $ / 1M output tokens
	CacheWrite float64 // $ / 1M cache-creation tokens (5-minute TTL)
	CacheRead  float64 // $ / 1M cache-read tokens
}

// cachePrice derives the cache write/read rates from a model's published
// input rate using Anthropic's documented cache multipliers (write
// ~1.25x base input, read ~0.1x base input). This is the same ratio that
// underlies the cache_read ~$0.30/M and cache_creation ~$3.75/M figures
// docs/measurements/evaluation/micro.md reports for sonnet against its
// $3/M input rate — see docs/measurements/tools/cc-value.md for the full
// sourcing note, including the one number (bare input rate) that
// micro.md never states outright.
func cachePrice(input, output float64) ModelPrice {
	return ModelPrice{Input: input, Output: output, CacheWrite: input * 1.25, CacheRead: input * 0.1}
}

// modelPrices is a hardcoded snapshot of Anthropic's published per-token
// pricing, not a live feed — it drifts whenever Anthropic changes rates or
// ships a new model. Update alongside docs/measurements/tools/cc-value.md. A model
// string not in this table (including the synthetic "<synthetic>" model
// Claude Code emits for zero-usage housekeeping entries) is unpriced, not
// zero-priced — see priceFor and Value.Current.UnpricedTokens.
var modelPrices = map[string]ModelPrice{
	"claude-opus-4-8":   cachePrice(5.00, 25.00),
	"claude-opus-4-7":   cachePrice(5.00, 25.00),
	"claude-opus-4-6":   cachePrice(5.00, 25.00),
	"claude-opus-4-5":   cachePrice(5.00, 25.00),
	"claude-opus-4-1":   cachePrice(5.00, 25.00),
	"claude-opus-4-0":   cachePrice(5.00, 25.00),
	"claude-fable-5":    cachePrice(10.00, 50.00),
	"claude-mythos-5":   cachePrice(10.00, 50.00),
	"claude-sonnet-5":   cachePrice(3.00, 15.00),
	"claude-sonnet-4-6": cachePrice(3.00, 15.00),
	"claude-sonnet-4-5": cachePrice(3.00, 15.00),
	"claude-sonnet-4-0": cachePrice(3.00, 15.00),
	"claude-haiku-4-5":  cachePrice(1.00, 5.00),
}

// priceFor resolves a transcript's raw model string to a price. It tries
// an exact match first, then a known-model prefix (so a dated snapshot
// like "claude-haiku-4-5-20251001" resolves via "claude-haiku-4-5"). ok is
// false for anything not covered — callers must treat that as "unpriced",
// never silently price it at $0.
func priceFor(model string) (ModelPrice, bool) {
	if p, ok := modelPrices[model]; ok {
		return p, true
	}
	for known, p := range modelPrices {
		if strings.HasPrefix(model, known+"-") {
			return p, true
		}
	}
	return ModelPrice{}, false
}

// cost converts a token count of one type to USD at a $/1M rate.
func cost(tokens int64, ratePerMillion float64) float64 {
	return float64(tokens) / 1_000_000 * ratePerMillion
}
