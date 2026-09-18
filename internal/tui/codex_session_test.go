package tui

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CDFalcon/ccmux/internal/agent"
)

// writeCodexRollout creates a rollout file under a fake CODEX_HOME in the
// given date directory and returns its path. The session_meta header ties
// it to cwd.
func writeCodexRollout(t *testing.T, codexHome, day, name, cwd, body string) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	header := `{"timestamp":"2026-09-03T14:49:23.338Z","type":"session_meta","payload":{"id":"x","timestamp":"2026-09-03T14:49:23.338Z","cwd":"` + cwd + `","originator":"codex-tui","cli_version":"0.153.0"}}` + "\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(header+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const codexTurnContext = `{"timestamp":"2026-09-03T14:49:24.000Z","type":"turn_context","payload":{"cwd":"/w","model":"gpt-5.5","effort":"xhigh"}}` + "\n"

func codexTokenCount(ts string, total, last codexTokenUsage) string {
	f := func(u codexTokenUsage) string {
		return `{"input_tokens":` + itoa(u.InputTokens) +
			`,"cached_input_tokens":` + itoa(u.CachedInputTokens) +
			`,"output_tokens":` + itoa(u.OutputTokens) +
			`,"reasoning_output_tokens":` + itoa(u.ReasoningOutputTokens) +
			`,"total_tokens":` + itoa(u.TotalTokens) + `}`
	}
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":` + f(total) +
		`,"last_token_usage":` + f(last) + `,"model_context_window":258400},"rate_limits":null}}` + "\n"
}

func itoa(n int64) string {
	return string(appendInt(nil, n))
}

func appendInt(b []byte, n int64) []byte {
	if n < 0 {
		b = append(b, '-')
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return append(b, buf[i:]...)
}

// Each token_count carries cumulative totals; per-request usage is the
// delta between consecutive snapshots, attributed to the current model.
func TestScanCodexRollout_ShouldUseDeltasBetweenSnapshots_GivenCumulativeTotals(t *testing.T) {
	// Setup.
	home := t.TempDir()
	body := codexTurnContext +
		codexTokenCount("2026-09-03T14:49:30.000Z",
			codexTokenUsage{InputTokens: 16114, CachedInputTokens: 5504, OutputTokens: 701, ReasoningOutputTokens: 372, TotalTokens: 16815},
			codexTokenUsage{InputTokens: 16114, CachedInputTokens: 5504, OutputTokens: 701, ReasoningOutputTokens: 372, TotalTokens: 16815}) +
		codexTokenCount("2026-09-03T14:50:30.000Z",
			codexTokenUsage{InputTokens: 37673, CachedInputTokens: 21248, OutputTokens: 1102, ReasoningOutputTokens: 449, TotalTokens: 38775},
			codexTokenUsage{InputTokens: 21559, CachedInputTokens: 15744, OutputTokens: 401, ReasoningOutputTokens: 77, TotalTokens: 21960})
	path := writeCodexRollout(t, home, "2026/09/03", "rollout-a.jsonl", "/w", body)

	// Execute.
	recs := scanCodexRollout(path)

	// Assert. Two requests; the totals equal the final cumulative snapshot.
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d: %+v", len(recs), recs)
	}
	u := summarizeUsage(recs)
	if u.tokens.In != 37673-21248 {
		t.Errorf("expected uncached input %d, got %d", 37673-21248, u.tokens.In)
	}
	if u.tokens.CacheRead != 21248 {
		t.Errorf("expected cached input 21248, got %d", u.tokens.CacheRead)
	}
	if u.tokens.Out != 1102 {
		t.Errorf("expected output 1102, got %d", u.tokens.Out)
	}
	if u.tokens.CacheCreate != 0 {
		t.Errorf("Codex has no cache-write charge; got CacheCreate %d", u.tokens.CacheCreate)
	}
	for _, r := range recs {
		if r.model != "gpt-5.5" {
			t.Errorf("expected model from turn_context, got %q", r.model)
		}
	}
	want := priceCodexCall("gpt-5.5", 16114, 5504, 701) + priceCodexCall("gpt-5.5", 21559, 15744, 401)
	if math.Abs(u.tokens.CostUSD-want) > 1e-9 {
		t.Errorf("expected cost %.9f, got %.9f", want, u.tokens.CostUSD)
	}
}

// A rollout forked from a parent thread starts with the parent's cumulative
// total. Only the request that produced the first snapshot may be counted,
// or fan-outs inflate cost by the whole parent history.
func TestScanCodexRollout_ShouldCountOnlyLastUsage_GivenInheritedFirstSnapshot(t *testing.T) {
	// Setup. First snapshot: 1M inherited + a 1k request.
	home := t.TempDir()
	body := codexTurnContext +
		codexTokenCount("2026-09-03T14:49:30.000Z",
			codexTokenUsage{InputTokens: 1_001_000, CachedInputTokens: 900_000, OutputTokens: 50_100, TotalTokens: 1_051_100},
			codexTokenUsage{InputTokens: 1000, CachedInputTokens: 800, OutputTokens: 100, TotalTokens: 1100})
	path := writeCodexRollout(t, home, "2026/09/03", "rollout-fork.jsonl", "/w", body)

	// Execute.
	u := summarizeUsage(scanCodexRollout(path))

	// Assert.
	if u.tokens.In != 200 || u.tokens.CacheRead != 800 || u.tokens.Out != 100 {
		t.Errorf("expected only the last request (200/800/100), got %+v", u.tokens)
	}
}

// If the cumulative counter goes backwards mid-file, the delta is
// meaningless; fall back to the per-request figure rather than dropping or
// negating usage.
func TestScanCodexRollout_ShouldFallBackToLastUsage_GivenCounterReset(t *testing.T) {
	// Setup.
	home := t.TempDir()
	body := codexTurnContext +
		codexTokenCount("2026-09-03T14:49:30.000Z",
			codexTokenUsage{InputTokens: 5000, OutputTokens: 500, TotalTokens: 5500},
			codexTokenUsage{InputTokens: 5000, OutputTokens: 500, TotalTokens: 5500}) +
		codexTokenCount("2026-09-03T14:50:30.000Z",
			codexTokenUsage{InputTokens: 300, OutputTokens: 30, TotalTokens: 330},
			codexTokenUsage{InputTokens: 300, OutputTokens: 30, TotalTokens: 330})
	path := writeCodexRollout(t, home, "2026/09/03", "rollout-reset.jsonl", "/w", body)

	// Execute.
	u := summarizeUsage(scanCodexRollout(path))

	// Assert.
	if u.tokens.In != 5300 || u.tokens.Out != 530 {
		t.Errorf("expected 5300 in / 530 out, got %+v", u.tokens)
	}
}

// token_count events with a null info are rate-limit refreshes.
func TestScanCodexRollout_ShouldSkipRateLimitOnlyEvents_GivenNullInfo(t *testing.T) {
	// Setup.
	home := t.TempDir()
	body := codexTurnContext +
		`{"timestamp":"2026-09-03T14:49:30.000Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex"}}}` + "\n"
	path := writeCodexRollout(t, home, "2026/09/03", "rollout-rl.jsonl", "/w", body)

	// Execute / Assert.
	if recs := scanCodexRollout(path); len(recs) != 0 {
		t.Errorf("expected no records, got %+v", recs)
	}
}

// A model change mid-thread (Codex's /model) re-prices subsequent requests.
func TestScanCodexRollout_ShouldFollowModelChanges_GivenMultipleTurnContexts(t *testing.T) {
	// Setup.
	home := t.TempDir()
	body := codexTurnContext +
		codexTokenCount("2026-09-03T14:49:30.000Z",
			codexTokenUsage{InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100},
			codexTokenUsage{InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100}) +
		`{"timestamp":"2026-09-03T14:51:00.000Z","type":"turn_context","payload":{"cwd":"/w","model":"gpt-5.6-sol"}}` + "\n" +
		codexTokenCount("2026-09-03T14:52:30.000Z",
			codexTokenUsage{InputTokens: 2000, OutputTokens: 200, TotalTokens: 2200},
			codexTokenUsage{InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100})
	path := writeCodexRollout(t, home, "2026/09/03", "rollout-model.jsonl", "/w", body)

	// Execute.
	recs := scanCodexRollout(path)

	// Assert.
	if len(recs) != 2 || recs[0].model != "gpt-5.5" || recs[1].model != "gpt-5.6-sol" {
		t.Fatalf("expected gpt-5.5 then gpt-5.6-sol, got %+v", recs)
	}
	if math.Abs(recs[1].costUSD-priceCodexCall("gpt-5.6-sol", 1000, 0, 100)) > 1e-9 {
		t.Errorf("second request not priced at gpt-5.6-sol rate: %+v", recs[1])
	}
}

// Rollouts are matched to an agent by the cwd in their header, across
// every date directory from the agent's creation day on; other worktrees'
// rollouts and older days are ignored.
func TestScanCodexSessions_ShouldSumMatchingRollouts_GivenMixedWorktrees(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	req := func(ts string) string {
		return codexTurnContext + codexTokenCount(ts,
			codexTokenUsage{InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100},
			codexTokenUsage{InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100})
	}
	writeCodexRollout(t, home, "2026/09/03", "rollout-1.jsonl", "/wt/agent-a", req("2026-09-03T14:49:30.000Z"))
	writeCodexRollout(t, home, "2026/09/04", "rollout-2.jsonl", "/wt/agent-a", req("2026-09-04T10:00:00.000Z"))
	writeCodexRollout(t, home, "2026/09/04", "rollout-3.jsonl", "/wt/agent-b", req("2026-09-04T10:00:00.000Z"))
	// Older than the agent by more than the one-day slack: pruned.
	writeCodexRollout(t, home, "2026/08/20", "rollout-0.jsonl", "/wt/agent-a", req("2026-08-20T10:00:00.000Z"))
	// Not a rollout.
	os.WriteFile(filepath.Join(home, "sessions", "2026", "09", "04", "notes.jsonl"), []byte("{}\n"), 0o644)

	created := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	// Execute.
	u := agentSessionUsage(&agent.Agent{Harness: "codex", WorktreePath: "/wt/agent-a", CreatedAt: created})

	// Assert. Two matching rollouts, split across two days.
	if u.tokens.In != 2000 || u.tokens.Out != 200 {
		t.Errorf("expected 2000 in / 200 out across both rollouts, got %+v", u.tokens)
	}
	if len(u.daily) != 2 || u.daily["2026-09-03"] <= 0 || u.daily["2026-09-04"] <= 0 {
		t.Errorf("expected costs on 2026-09-03 and 2026-09-04, got %v", u.daily)
	}
	if math.Abs(u.daily["2026-09-03"]-u.daily["2026-09-04"]) > 1e-12 {
		t.Errorf("expected equal per-day costs, got %v", u.daily)
	}
}

func TestScanCodexSessions_ShouldIncludeAllDays_GivenZeroCreatedAt(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	body := codexTurnContext + codexTokenCount("2026-01-01T10:00:00.000Z",
		codexTokenUsage{InputTokens: 10, OutputTokens: 1, TotalTokens: 11},
		codexTokenUsage{InputTokens: 10, OutputTokens: 1, TotalTokens: 11})
	writeCodexRollout(t, home, "2026/01/01", "rollout-old.jsonl", "/wt/x", body)

	// Execute.
	u := summarizeUsage(scanCodexSessions("/wt/x", time.Time{}))

	// Assert.
	if u.tokens.In != 10 {
		t.Errorf("expected the old rollout to be included, got %+v", u.tokens)
	}
}

// A rollout that has not written its header yet must not be cached as
// "no cwd", or it would never be attributed once the header lands.
func TestReadCodexRolloutCwd_ShouldNotCacheEmpty_GivenHeaderlessFile(t *testing.T) {
	// Setup.
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "09", "05")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "rollout-late.jsonl")
	os.WriteFile(path, nil, 0o644)

	// Execute. First read sees nothing; header appears; second read must
	// see it.
	first := readCodexRolloutCwd(path)
	writeCodexRollout(t, home, "2026/09/05", "rollout-late.jsonl", "/wt/late", "")
	second := readCodexRolloutCwd(path)

	// Assert.
	if first != "" {
		t.Errorf("expected empty cwd before header, got %q", first)
	}
	if second != "/wt/late" {
		t.Errorf("expected cwd after header landed, got %q", second)
	}
}

// Codex agents get transcript-derived cost in the resource poll, the same
// way Claude agents without telemetry do.
func TestQueryAllAgentResources_ShouldPriceCodexAgent_GivenRollout(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	worktree := t.TempDir()
	body := codexTurnContext + codexTokenCount("2026-09-03T14:49:30.000Z",
		codexTokenUsage{InputTokens: 1_000_000, CachedInputTokens: 600_000, OutputTokens: 100_000, TotalTokens: 1_100_000},
		codexTokenUsage{InputTokens: 1_000_000, CachedInputTokens: 600_000, OutputTokens: 100_000, TotalTokens: 1_100_000})
	writeCodexRollout(t, home, "2026/09/03", "rollout-x.jsonl", worktree, body)

	agents := []*agent.Agent{{
		ID:           "codex-agent",
		Harness:      "codex",
		WorktreePath: worktree,
		CreatedAt:    time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),
	}}

	// Execute.
	resources, _, daily := queryAllAgentResources(agents, nil, 0, 0, cpuSample{}, nil, nil, newDiskProbe(), nil)

	// Assert. 400k × $5 + 600k × $0.50 + 100k × $30 = $5.30.
	res := resources["codex-agent"]
	if res == nil {
		t.Fatal("no resources for codex agent")
	}
	if math.Abs(res.CostUSD-5.3) > 1e-9 {
		t.Errorf("expected $5.30, got %.6f", res.CostUSD)
	}
	if res.TokensIn != 400_000 || res.TokensCacheRead != 600_000 || res.TokensOut != 100_000 {
		t.Errorf("unexpected token split: %+v", res)
	}
	if math.Abs(daily["2026-09-03"]-5.3) > 1e-9 {
		t.Errorf("expected daily rollup $5.30, got %v", daily)
	}
}
