package tui

import "strings"

// Pricing tables for the two harnesses ccmux runs, used only when the harness
// itself does not hand us a cost figure.
//
// How session cost is computed, and why:
//
//   - Claude Code: the authoritative number is Claude Code's own
//     `claude_code.cost.usage` OpenTelemetry counter, which the in-process
//     collector receives per API request (internal/otelcollector). It is
//     itself an estimate — Claude Code prices `usage` blocks from its
//     embedded table — but it tracks every model, fast-mode and fallback
//     rule the harness knows about without us maintaining a mirror. The
//     JSONL re-derivation here is the fallback for agents whose telemetry is
//     not wired up (a user-supplied OTEL endpoint, a pre-upgrade launcher, a
//     collector that failed to start). It reads every transcript under the
//     project dir (main session plus `<session>/subagents/*.jsonl`), dedupes
//     API calls by message id, and prices each call at the list rates below:
//     input, output, cache read (0.1× input; 0.025× on Fable/Mythos 5.1),
//     5-minute cache write (1.25×), 1-hour cache write (2×), fast mode
//     ($10/$50 on Opus 5 and Opus 4.8) and the 1.1× US-only inference_geo
//     multiplier. Thinking tokens are already inside output_tokens. When a
//     request was re-served by a server-side fallback model, `usage.iterations`
//     carries one entry per model and each is priced at its own rate.
//
//   - Codex CLI: emits no cost metric (its OTel export has only a
//     `codex.turn.token_usage` histogram), so cost is always derived from the
//     rollout transcript under $CODEX_HOME/sessions. Each `token_count` event
//     carries cumulative `total_token_usage`; per-request usage is the delta
//     between consecutive snapshots (the first snapshot of a forked rollout
//     inherits the parent's total, so only `last_token_usage` is counted
//     there). `cached_input_tokens` is a subset of `input_tokens` and is
//     billed at the cached-input rate; `reasoning_output_tokens` is a subset
//     of `output_tokens` and is not billed separately. The model comes from
//     the most recent `turn_context` event.
//
// Both tables are list prices in USD per million tokens, as published on
// platform.claude.com/docs/en/about-claude/pricing and
// developers.openai.com/api/docs/pricing (checked 2026-09-16). Subscription
// users (Claude Max, ChatGPT Team/Pro) are not billed per token, so the
// figure is what the same work would have cost on the API — hence "(est.)"
// everywhere the TUI shows it.

// Anthropic prompt-caching multipliers, constant across the Claude line
// except for the Fable/Mythos 5.1 cache-read rate carried in modelRates.
const (
	claudeCacheCreate5mMult = 1.25 // 5-minute ephemeral write
	claudeCacheCreate1hMult = 2.00 // 1-hour ephemeral write — Claude Code's default TTL
	claudeCacheReadMult     = 0.10 // read/refresh on every model but Fable/Mythos 5.1
	claudeUSInferenceMult   = 1.10 // inference_geo: "us" surcharge on Claude 4.6+
)

// modelRates is the list price for one Claude model family.
type modelRates struct {
	inputPer1M    float64
	outputPer1M   float64
	cacheReadMult float64 // fraction of inputPer1M charged for a cache hit
	// Fast-mode rates; zero when the family has no fast mode. Cache
	// multipliers stack on top of these, per Anthropic's pricing page.
	fastInputPer1M  float64
	fastOutputPer1M float64
}

// modelRatesFor returns the published Anthropic list rates for a model id as
// it appears in the transcript (e.g. "claude-opus-5", "claude-fable-5-1",
// "claude-haiku-4-5-20251001"). Matching is by substring so dated snapshots
// and future point releases inside a family price correctly; a family we
// have never seen falls back to Sonnet 4.6 rates, the mid-tier price.
func modelRatesFor(model string) modelRates {
	m := strings.ToLower(model)
	has := func(s string) bool { return strings.Contains(m, s) }
	switch {
	// Fable 5.1 / Mythos 5.1: $10/$50, cache reads at 0.025× ($0.25/MTok).
	case has("fable-5-1"), has("mythos-5-1"):
		return modelRates{inputPer1M: 10, outputPer1M: 50, cacheReadMult: 0.025}
	// Fable 5 / Mythos 5 / Mythos Preview: same tier, standard 0.1× reads.
	case has("fable"), has("mythos"):
		return modelRates{inputPer1M: 10, outputPer1M: 50, cacheReadMult: claudeCacheReadMult}
	// Opus 4.5 through Opus 5: $5/$25. Fast mode ($10/$50) exists on Opus 5
	// and Opus 4.8; Opus 4.7 rejects speed:"fast" and 4.6 silently runs
	// standard, so a transcript for those never records speed:"fast".
	case isNewOpus(m):
		return modelRates{inputPer1M: 5, outputPer1M: 25, cacheReadMult: claudeCacheReadMult, fastInputPer1M: 10, fastOutputPer1M: 50}
	// Opus 3 / 4 / 4.1 (retired on the first-party API).
	case has("opus"):
		return modelRates{inputPer1M: 15, outputPer1M: 75, cacheReadMult: claudeCacheReadMult}
	// Sonnet 5: $2/$10 (the launch price became permanent in Sept 2026).
	case has("sonnet-5"):
		return modelRates{inputPer1M: 2, outputPer1M: 10, cacheReadMult: claudeCacheReadMult}
	// Haiku 3.5 was $0.80/$4.
	case has("haiku-3-5"), has("haiku-3.5"):
		return modelRates{inputPer1M: 0.8, outputPer1M: 4, cacheReadMult: claudeCacheReadMult}
	// Haiku 4.5 (and any later Haiku until told otherwise): $1/$5.
	case has("haiku"):
		return modelRates{inputPer1M: 1, outputPer1M: 5, cacheReadMult: claudeCacheReadMult}
	// Sonnet 3.5 / 3.7 / 4 / 4.5 / 4.6: $3/$15. Also the fallback for an
	// unrecognised model.
	default:
		return modelRates{inputPer1M: 3, outputPer1M: 15, cacheReadMult: claudeCacheReadMult}
	}
}

func isNewOpus(model string) bool {
	return strings.Contains(model, "opus-4-5") ||
		strings.Contains(model, "opus-4-6") ||
		strings.Contains(model, "opus-4-7") ||
		strings.Contains(model, "opus-4-8") ||
		strings.Contains(model, "opus-4-9") ||
		strings.Contains(model, "opus-5")
}

// claudeTokenCounts is the billable token split of one API call (or one
// iteration of a fallback chain), independent of the JSONL envelope.
type claudeTokenCounts struct {
	input         int64
	output        int64
	cacheRead     int64
	cacheCreate5m int64
	cacheCreate1h int64
}

// priceClaudeCall prices one call at the model's list rate. speed is the
// `usage.speed` value ("fast" selects the fast-mode rate where one exists).
func priceClaudeCall(model string, c claudeTokenCounts, speed string) float64 {
	r := modelRatesFor(model)
	in, out := r.inputPer1M, r.outputPer1M
	if speed == "fast" && r.fastInputPer1M > 0 {
		in, out = r.fastInputPer1M, r.fastOutputPer1M
	}
	cost := float64(c.input) * in / 1_000_000
	cost += float64(c.output) * out / 1_000_000
	cost += float64(c.cacheRead) * (in * r.cacheReadMult) / 1_000_000
	cost += float64(c.cacheCreate5m) * (in * claudeCacheCreate5mMult) / 1_000_000
	cost += float64(c.cacheCreate1h) * (in * claudeCacheCreate1hMult) / 1_000_000
	return cost
}

// estimateCost prices one deduplicated assistant message from a Claude Code
// transcript. The top-level `model` is the model the request was addressed
// to; when the API re-served the request through a fallback model, the
// `iterations` list names the model that actually produced each part and
// each part is priced at that model's rate.
func estimateCost(model string, u claudeUsage) float64 {
	var cost float64
	if its := u.pricedIterations(model); len(its) > 0 {
		for _, it := range its {
			cost += priceClaudeCall(it.model, it.counts, u.Speed)
		}
	} else {
		cost = priceClaudeCall(model, u.counts(), u.Speed)
	}
	if u.InferenceGeo == "us" {
		cost *= claudeUSInferenceMult
	}
	return cost
}

// codexRates is the list price for one OpenAI model family, USD per million
// tokens. cachedInputPer1M is the rate for the cached portion of the input.
type codexRates struct {
	inputPer1M       float64
	cachedInputPer1M float64
	outputPer1M      float64
}

// codexRateTable is matched by prefix, most specific first, against the
// lower-cased model id from the rollout's turn_context (e.g. "gpt-5.5",
// "gpt-5.6-sol", "gpt-5.3-codex"). Cached input is 10% of input across the
// line; the -pro variants have no cache discount.
var codexRateTable = []struct {
	prefix string
	rates  codexRates
}{
	{"gpt-5.6-sol", codexRates{4, 0.40, 20}},
	{"gpt-5.6-terra", codexRates{2, 0.20, 12}},
	{"gpt-5.6-luna", codexRates{0.20, 0.02, 1.20}},
	{"gpt-5.5-pro", codexRates{30, 30, 180}},
	{"gpt-5.5", codexRates{5, 0.50, 30}},
	{"gpt-5.4-pro", codexRates{30, 30, 180}},
	{"gpt-5.4-mini", codexRates{0.75, 0.075, 4.50}},
	{"gpt-5.4-nano", codexRates{0.20, 0.02, 1.25}},
	{"gpt-5.4", codexRates{2.50, 0.25, 15}},
	{"gpt-5.3-codex", codexRates{1.75, 0.175, 14}},
	{"gpt-5.2", codexRates{1.75, 0.175, 14}},
	{"gpt-5.1-codex-mini", codexRates{0.25, 0.025, 2}},
	{"gpt-5.1", codexRates{1.25, 0.125, 10}},
	{"gpt-5-codex-mini", codexRates{0.25, 0.025, 2}},
	{"gpt-5-mini", codexRates{0.25, 0.025, 2}},
	{"gpt-5-nano", codexRates{0.05, 0.005, 0.40}},
	{"gpt-5", codexRates{1.25, 0.125, 10}},
}

// codexDefaultRates prices a model id the table does not know. Codex's
// default model at the time of writing is gpt-5.5, and an unknown id is far
// more likely a newer flagship than a mini, so over-estimating at the
// flagship rate is the safer error.
var codexDefaultRates = codexRates{5, 0.50, 30}

// codexRatesFor returns the list rates for a Codex model id.
func codexRatesFor(model string) codexRates {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, e := range codexRateTable {
		if strings.HasPrefix(m, e.prefix) {
			return e.rates
		}
	}
	return codexDefaultRates
}

// priceCodexCall prices one request's token delta. cached must already be
// the subset of input served from the prompt cache; reasoning tokens are
// inside output and carry no separate charge.
func priceCodexCall(model string, input, cached, output int64) float64 {
	r := codexRatesFor(model)
	if cached > input {
		cached = input
	}
	cost := float64(input-cached) * r.inputPer1M / 1_000_000
	cost += float64(cached) * r.cachedInputPer1M / 1_000_000
	cost += float64(output) * r.outputPer1M / 1_000_000
	return cost
}
