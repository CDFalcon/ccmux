package tui

import (
	"math"
	"testing"
)

func assertCost(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("expected cost %.9f, got %.9f", want, got)
	}
}

// Fable 5.1 was previously falling through to the Sonnet default ($3/$15) —
// a 3.3× under-estimate on the most expensive model in the line.
func TestEstimateCost_ShouldUseFablePricing_GivenFable51(t *testing.T) {
	// Setup.
	usage := claudeUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	// Execute.
	cost := estimateCost("claude-fable-5-1", usage)

	// Assert. $10 input + $50 output.
	assertCost(t, cost, 60)
}

// Fable/Mythos 5.1 cache hits are 0.025× base input ($0.25/MTok), not 0.1×.
func TestEstimateCost_ShouldPriceCacheReadAt0025x_GivenFable51(t *testing.T) {
	// Setup.
	usage := claudeUsage{CacheReadInputTokens: 1_000_000}

	// Execute / Assert.
	assertCost(t, estimateCost("claude-fable-5-1", usage), 0.25)
	assertCost(t, estimateCost("claude-mythos-5-1", usage), 0.25)
}

// Fable 5 keeps the standard 0.1× cache-read multiplier ($1/MTok).
func TestEstimateCost_ShouldPriceCacheReadAt01x_GivenFable5(t *testing.T) {
	// Setup.
	usage := claudeUsage{CacheReadInputTokens: 1_000_000}

	// Execute / Assert.
	assertCost(t, estimateCost("claude-fable-5", usage), 1.0)
}

// Sonnet 5 is $2/$10; Sonnet 4.6 and earlier stay at $3/$15.
func TestEstimateCost_ShouldUseSonnet5Pricing_GivenSonnet5(t *testing.T) {
	// Setup.
	usage := claudeUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	// Execute / Assert.
	assertCost(t, estimateCost("claude-sonnet-5", usage), 12)
	assertCost(t, estimateCost("claude-sonnet-4-6", usage), 18)
	assertCost(t, estimateCost("claude-sonnet-4-5-20250929", usage), 18)
}

func TestEstimateCost_ShouldUseOpus5Pricing_GivenOpus5(t *testing.T) {
	// Setup.
	usage := claudeUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	// Execute / Assert. $5 + $25.
	assertCost(t, estimateCost("claude-opus-5", usage), 30)
	assertCost(t, estimateCost("claude-opus-4-8", usage), 30)
}

// Fast mode on Opus 5 / 4.8 is $10/$50, and the cache multipliers stack on
// the fast-mode input rate.
func TestEstimateCost_ShouldUseFastModeRates_GivenSpeedFast(t *testing.T) {
	// Setup.
	usage := claudeUsage{
		InputTokens:          1_000_000,
		OutputTokens:         1_000_000,
		CacheReadInputTokens: 1_000_000,
		Speed:                "fast",
	}

	// Execute.
	cost := estimateCost("claude-opus-5", usage)

	// Assert. $10 + $50 + $10 × 0.1.
	assertCost(t, cost, 61)
}

// Models with no fast mode ignore speed:"fast" rather than mis-pricing.
func TestEstimateCost_ShouldIgnoreSpeedFast_GivenModelWithoutFastMode(t *testing.T) {
	// Setup.
	usage := claudeUsage{InputTokens: 1_000_000, Speed: "fast"}

	// Execute / Assert.
	assertCost(t, estimateCost("claude-sonnet-5", usage), 2)
}

// US-only inference is a 1.1× multiplier on every token category.
func TestEstimateCost_ShouldApply11x_GivenInferenceGeoUS(t *testing.T) {
	// Setup.
	usage := claudeUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000, InferenceGeo: "us"}

	// Execute / Assert. ($5 + $25) × 1.1.
	assertCost(t, estimateCost("claude-opus-5", usage), 33)
}

func TestEstimateCost_ShouldNotApplyMultiplier_GivenInferenceGeoGlobalOrNotAvailable(t *testing.T) {
	// Setup.
	usage := claudeUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000, InferenceGeo: "not_available"}

	// Execute / Assert.
	assertCost(t, estimateCost("claude-opus-5", usage), 30)
}

// When a request is re-served through a server-side fallback, the top-level
// usage only reflects the final leg and names the model the request was
// addressed to. Each iteration must be priced at the model that actually
// ran it. This shape is copied from a real transcript: Fable 5.1 refused
// after 125 output tokens and Opus 4.8 produced the answer.
func TestEstimateCost_ShouldPriceEachLeg_GivenFallbackIterations(t *testing.T) {
	// Setup.
	usage := claudeUsage{
		InputTokens:              32,
		OutputTokens:             1762,
		CacheReadInputTokens:     226160,
		CacheCreationInputTokens: 0,
		CacheCreation:            claudeCacheCreation{Ephemeral1hInputTokens: 2524},
		Speed:                    "standard",
		Iterations: []claudeIteration{
			{
				Type: "message", Model: "claude-fable-5-1",
				InputTokens: 32, OutputTokens: 125,
				CacheReadInputTokens: 268718, CacheCreationInputTokens: 2524,
				CacheCreation: claudeCacheCreation{Ephemeral1hInputTokens: 2524},
			},
			{
				Type: "fallback_message", Model: "claude-opus-4-8",
				InputTokens: 32, OutputTokens: 1762,
				CacheReadInputTokens: 226160,
			},
		},
	}

	// Execute.
	cost := estimateCost("claude-fable-5-1", usage)

	// Assert.
	fable := priceClaudeCall("claude-fable-5-1", claudeTokenCounts{
		input: 32, output: 125, cacheRead: 268718, cacheCreate1h: 2524,
	}, "standard")
	opus := priceClaudeCall("claude-opus-4-8", claudeTokenCounts{
		input: 32, output: 1762, cacheRead: 226160,
	}, "standard")
	assertCost(t, cost, fable+opus)

	// Pricing the top-level block at Fable rates would have charged Opus's
	// 1762 output tokens at $50/MTok — make sure that is not what happens.
	naive := priceClaudeCall("claude-fable-5-1", usage.counts(), "standard")
	if math.Abs(cost-naive) < 1e-9 {
		t.Errorf("fallback legs were not priced individually (got naive top-level price %.9f)", naive)
	}
}

// A single iteration restates the top-level block (with a null model) and
// must not change the price.
func TestEstimateCost_ShouldUseTopLevel_GivenSingleIteration(t *testing.T) {
	// Setup.
	usage := claudeUsage{
		InputTokens: 2, OutputTokens: 214, CacheCreationInputTokens: 42890,
		CacheCreation: claudeCacheCreation{Ephemeral1hInputTokens: 42890},
		Iterations: []claudeIteration{{
			Type: "message", InputTokens: 2, OutputTokens: 214, CacheCreationInputTokens: 42890,
			CacheCreation: claudeCacheCreation{Ephemeral1hInputTokens: 42890},
		}},
	}

	// Execute / Assert.
	assertCost(t, estimateCost("claude-fable-5-1", usage),
		priceClaudeCall("claude-fable-5-1", usage.counts(), ""))
}

// An iteration without a model (older transcripts) is priced at the
// request's model rather than the fallback default.
func TestEstimateCost_ShouldUseRequestModel_GivenIterationWithoutModel(t *testing.T) {
	// Setup.
	usage := claudeUsage{
		OutputTokens: 200,
		Iterations: []claudeIteration{
			{Type: "message", OutputTokens: 100},
			{Type: "fallback_message", OutputTokens: 100},
		},
	}

	// Execute / Assert. 200 output tokens at Opus 5's $25/MTok.
	assertCost(t, estimateCost("claude-opus-5", usage), 200*25.0/1_000_000)
}

func TestModelRatesFor_ShouldFallBackToSonnet46_GivenUnknownModel(t *testing.T) {
	// Execute.
	r := modelRatesFor("claude-something-new")

	// Assert.
	if r.inputPer1M != 3 || r.outputPer1M != 15 || r.cacheReadMult != claudeCacheReadMult {
		t.Errorf("unexpected fallback rates: %+v", r)
	}
}

func TestModelRatesFor_ShouldUseHaiku35Pricing_GivenHaiku35(t *testing.T) {
	// Execute.
	r := modelRatesFor("claude-3-5-haiku-20241022")
	r2 := modelRatesFor("claude-haiku-3-5")

	// Assert. The dated 3.5 id spells it "3-5-haiku", which the table
	// does not special-case: it prices at the Haiku 4.5 rate (a
	// deliberate slight over-estimate), while the family id is exact.
	if r.inputPer1M != 1 || r.outputPer1M != 5 {
		t.Errorf("expected Haiku default rates for dated 3.5 id, got %+v", r)
	}
	if r2.inputPer1M != 0.8 || r2.outputPer1M != 4 {
		t.Errorf("expected Haiku 3.5 rates, got %+v", r2)
	}
}

// Codex: cached input is a subset of input billed at the cached rate;
// reasoning tokens ride inside output.
func TestPriceCodexCall_ShouldBillCachedSubsetAtCachedRate_GivenGPT55(t *testing.T) {
	// Execute. 1M input of which 600k cached, 100k output at gpt-5.5
	// ($5 / $0.50 cached / $30).
	cost := priceCodexCall("gpt-5.5", 1_000_000, 600_000, 100_000)

	// Assert. 400k × $5 + 600k × $0.50 + 100k × $30 = 2 + 0.3 + 3.
	assertCost(t, cost, 5.3)
}

func TestPriceCodexCall_ShouldClampCached_GivenCachedExceedsInput(t *testing.T) {
	// Execute. Malformed delta: cached > input. Never bill negative
	// uncached input.
	cost := priceCodexCall("gpt-5.5", 100, 200, 0)

	// Assert. All 100 billed as cached.
	assertCost(t, cost, 100*0.50/1_000_000)
}

func TestCodexRatesFor_ShouldMatchMostSpecificPrefix(t *testing.T) {
	cases := map[string]codexRates{
		"gpt-5.6-sol":        {4, 0.40, 20},
		"gpt-5.5":            {5, 0.50, 30},
		"gpt-5.5-pro":        {30, 30, 180},
		"gpt-5.4-mini":       {0.75, 0.075, 4.50},
		"gpt-5.4":            {2.50, 0.25, 15},
		"gpt-5.3-codex":      {1.75, 0.175, 14},
		"gpt-5.1-codex-mini": {0.25, 0.025, 2},
		"gpt-5-codex":        {1.25, 0.125, 10},
		"GPT-5.5":            {5, 0.50, 30},
	}
	for model, want := range cases {
		if got := codexRatesFor(model); got != want {
			t.Errorf("%s: expected %+v, got %+v", model, want, got)
		}
	}
}

func TestCodexRatesFor_ShouldFallBackToFlagship_GivenUnknownModel(t *testing.T) {
	// Execute / Assert. An unknown id (or an empty one, when a rollout
	// has no turn_context yet) prices at the flagship rate.
	if got := codexRatesFor("gpt-7-omega"); got != codexDefaultRates {
		t.Errorf("expected default rates, got %+v", got)
	}
	if got := codexRatesFor(""); got != codexDefaultRates {
		t.Errorf("expected default rates for empty model, got %+v", got)
	}
}
