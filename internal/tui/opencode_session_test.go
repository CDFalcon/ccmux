package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/CDFalcon/ccmux/internal/agent"
)

func TestOpenCodeRecords_ShouldUseOpenCodeCost_AndPriceSubscriptionOpenAI(t *testing.T) {
	day := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	recs := openCodeRecords([]openCodeMessageRow{
		{Created: day, Provider: "anthropic", Model: "claude-x", Cost: 0.25, Input: 100, Output: 10},
		// ChatGPT-subscription Astra: OpenCode records no cost.
		{Created: day, Provider: "openai", Model: "gpt-6-astra", Input: 1_000_000, CacheRead: 1_000_000, Output: 500_000, Reasoning: 500_000},
		{Created: day, Provider: "opencode", Model: "free-model", Input: 50, Output: 5},
		{Created: day, Provider: "openai", Model: "gpt-6-astra"}, // empty: skipped
	})
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3", len(recs))
	}
	if recs[0].costUSD != 0.25 {
		t.Errorf("OpenCode's own cost should be used, got %v", recs[0].costUSD)
	}
	// $10/M input + $1/M cached + $50/M on 1M output (incl. reasoning).
	if got, want := recs[1].costUSD, 10.0+1.0+50.0; got < want-1e-9 || got > want+1e-9 {
		t.Errorf("astra subscription cost = %v, want %v", got, want)
	}
	if recs[1].out != 1_000_000 || recs[1].cacheRead != 1_000_000 || recs[1].date != "2026-10-01" {
		t.Errorf("astra record = %+v", recs[1])
	}
	if recs[2].costUSD != 0 {
		t.Errorf("free model should stay free, got %v", recs[2].costUSD)
	}
}

func TestScanOpenCodeSessions_ShouldSumOnlyTheAgentsWorktree(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not on PATH")
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("OPENCODE_DB", "")
	os.MkdirAll(filepath.Join(data, "opencode"), 0o755)
	db := filepath.Join(data, "opencode", "opencode.db")
	schema := `
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT NOT NULL, parent_id TEXT);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, data TEXT NOT NULL);
INSERT INTO session VALUES ('s1', '/wt/agent-a', NULL), ('s1c', '/wt/agent-a', 's1'), ('s2', '/wt/agent-b', NULL);
INSERT INTO message VALUES
 ('m1', 's1', 1790000000000, '{"role":"user"}'),
 ('m2', 's1', 1790000001000, '{"role":"assistant","providerID":"anthropic","modelID":"x","cost":1.5,"tokens":{"input":10,"output":2,"reasoning":1,"cache":{"read":4,"write":3}}}'),
 ('m3', 's1c', 1790000002000, '{"role":"assistant","providerID":"anthropic","modelID":"x","cost":0.5,"tokens":{"input":1,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}'),
 ('m4', 's2', 1790000003000, '{"role":"assistant","providerID":"anthropic","modelID":"x","cost":9,"tokens":{"input":99,"output":99,"reasoning":0,"cache":{"read":0,"write":0}}}');
`
	if out, err := exec.Command(sqlite, db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}

	u := agentSessionUsage(&agent.Agent{Harness: "opencode", WorktreePath: "/wt/agent-a"})
	if u.tokens.CostUSD != 2.0 {
		t.Errorf("cost = %v, want 2.0 (root + sub-agent session, not the sibling)", u.tokens.CostUSD)
	}
	if u.tokens.In != 11 || u.tokens.Out != 4 || u.tokens.CacheRead != 4 || u.tokens.CacheCreate != 3 {
		t.Errorf("tokens = %+v", u.tokens)
	}
	if u := agentSessionUsage(&agent.Agent{Harness: "opencode", WorktreePath: "/wt/it's-quoted"}); u.tokens.Total != 0 {
		t.Errorf("unknown worktree should have no usage, got %+v", u.tokens)
	}
}
