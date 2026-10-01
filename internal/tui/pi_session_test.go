package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CDFalcon/ccmux/internal/agent"
)

func TestPiEncodeCwd_ShouldMatchPiLayout(t *testing.T) {
	if got := piEncodeCwd("/Users/me/Code/ccmux-ab12"); got != "--Users-me-Code-ccmux-ab12--" {
		t.Errorf("got %q", got)
	}
}

func writePiSession(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	data := ""
	for _, l := range lines {
		data += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanPiSessions_ShouldSumUsage_DedupeForks_AndPriceSubscription(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	wt := "/wt/agent-a"
	dir := filepath.Join(agentDir, "sessions", piEncodeCwd(wt))

	writePiSession(t, dir, "2026-10-01T10-00-00-000Z_s1.jsonl",
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-10-01T10:00:00.000Z","cwd":"/wt/agent-a"}`,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-10-01T10:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-10-01T10:00:02.000Z","message":{"role":"assistant","provider":"openai","model":"gpt-6-astra","responseId":"resp_1","usage":{"input":100,"output":10,"cacheRead":0,"cacheWrite":0,"totalTokens":110,"cost":{"total":0.5}},"timestamp":2}}`,
		`{"type":"usage","id":"w1","parentId":"a1","timestamp":"2026-10-02T09:00:00.000Z","kind":"cache_warm","provider":"anthropic","model":"x","usage":{"input":0,"output":0,"cacheRead":50,"cacheWrite":0,"totalTokens":50,"cost":{"total":0.25}}}`,
	)
	// A fork copies a1 (same responseId) and adds a subscription-priced turn.
	writePiSession(t, dir, "2026-10-01T11-00-00-000Z_s2.jsonl",
		`{"type":"session","version":3,"id":"s2","timestamp":"2026-10-01T11:00:00.000Z","cwd":"/wt/agent-a","parentSession":"x.jsonl"}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-10-01T10:00:02.000Z","message":{"role":"assistant","provider":"openai","model":"gpt-6-astra","responseId":"resp_1","usage":{"input":100,"output":10,"cacheRead":0,"cacheWrite":0,"totalTokens":110,"cost":{"total":0.5}},"timestamp":2}}`,
		`{"type":"message","id":"a2","parentId":"a1","timestamp":"2026-10-01T11:00:02.000Z","message":{"role":"assistant","provider":"openai-codex","model":"gpt-6-astra","responseId":"resp_2","usage":{"input":1000000,"output":1000000,"cacheRead":1000000,"cacheWrite":0,"totalTokens":3000000,"cost":{"total":0}},"timestamp":3}}`,
	)
	// A sibling worktree's session must not be counted.
	writePiSession(t, filepath.Join(agentDir, "sessions", piEncodeCwd("/wt/agent-b")), "s3.jsonl",
		`{"type":"session","version":3,"id":"s3","timestamp":"2026-10-01T10:00:00.000Z","cwd":"/wt/agent-b"}`,
		`{"type":"message","id":"b1","parentId":null,"timestamp":"2026-10-01T10:00:02.000Z","message":{"role":"assistant","provider":"openai","model":"gpt-6-astra","usage":{"input":9,"output":9,"cacheRead":0,"cacheWrite":0,"totalTokens":18,"cost":{"total":9}},"timestamp":2}}`,
	)

	u := agentSessionUsage(&agent.Agent{Harness: "pi", WorktreePath: wt})
	// 0.5 (a1, once) + 0.25 (cache warm) + 61 (a2 at $10 in / $1 cached / $50 out).
	if got, want := u.tokens.CostUSD, 0.5+0.25+61.0; got < want-1e-9 || got > want+1e-9 {
		t.Errorf("cost = %v, want %v", got, want)
	}
	if u.tokens.In != 1_000_100 || u.tokens.Out != 1_000_010 || u.tokens.CacheRead != 1_000_050 {
		t.Errorf("tokens = %+v", u.tokens)
	}
	if u.daily["2026-10-02"] != 0.25 {
		t.Errorf("daily = %v", u.daily)
	}
}

func TestScanPiSessions_ShouldFilterByHeaderCwd_GivenSharedSessionDir(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", shared)
	writePiSession(t, shared, "a.jsonl",
		`{"type":"session","version":3,"id":"a","timestamp":"t","cwd":"/wt/agent-a"}`,
		`{"type":"message","id":"m","parentId":null,"timestamp":"2026-10-01T00:00:00Z","message":{"role":"assistant","provider":"anthropic","model":"x","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"total":1}},"timestamp":1}}`)
	writePiSession(t, shared, "b.jsonl",
		`{"type":"session","version":3,"id":"b","timestamp":"t","cwd":"/wt/agent-b"}`,
		`{"type":"message","id":"m","parentId":null,"timestamp":"2026-10-01T00:00:00Z","message":{"role":"assistant","provider":"anthropic","model":"x","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"total":5}},"timestamp":1}}`)
	if got := summarizeUsage(scanPiSessions("/wt/agent-a")).tokens.CostUSD; got != 1 {
		t.Errorf("cost = %v, want 1", got)
	}
}
