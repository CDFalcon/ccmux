package tui

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Sub-agent turns (the Agent tool) are logged to
// <project>/<session-id>/subagents/agent-*.jsonl, not the main session file.
// They are billed like any other call and must be counted.
func TestScanClaudeSession_ShouldIncludeSubagentTranscripts_GivenSubagentsDir(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-subagents-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	main := `{"type":"assistant","timestamp":"2026-09-01T10:00:00.000Z","message":{"id":"msg_main","role":"assistant","model":"claude-opus-5","usage":{"input_tokens":1000,"output_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	sub := `{"type":"assistant","isSidechain":true,"timestamp":"2026-09-01T10:01:00.000Z","message":{"id":"msg_sub","role":"assistant","model":"claude-haiku-4-5-20251001","usage":{"input_tokens":3,"output_tokens":1,"cache_creation_input_tokens":18526,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":18526,"ephemeral_1h_input_tokens":0}}}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "sess.jsonl"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(projectDir, "sess", "subagents")
	os.MkdirAll(subDir, 0o755)
	if err := os.WriteFile(filepath.Join(subDir, "agent-abc.jsonl"), []byte(sub), 0o644); err != nil {
		t.Fatal(err)
	}
	// Auto-memory notes live next to the transcripts and must be skipped.
	os.MkdirAll(filepath.Join(projectDir, "memory"), 0o755)
	os.WriteFile(filepath.Join(projectDir, "memory", "notes.jsonl"), []byte(main), 0o644)

	// Execute.
	result := getAgentSessionTokens(worktreePath)

	// Assert.
	if result.In != 1003 || result.Out != 101 || result.CacheCreate != 18526 {
		t.Errorf("expected main + subagent tokens (1003/101/18526), got %+v", result)
	}
	want := estimateCost("claude-opus-5", claudeUsage{InputTokens: 1000, OutputTokens: 100}) +
		estimateCost("claude-haiku-4-5-20251001", claudeUsage{
			InputTokens: 3, OutputTokens: 1, CacheCreationInputTokens: 18526,
			CacheCreation: claudeCacheCreation{Ephemeral5mInputTokens: 18526},
		})
	if math.Abs(result.CostUSD-want) > 1e-9 {
		t.Errorf("expected cost %.9f, got %.9f", want, result.CostUSD)
	}
}

// A resumed session re-logs prior history into a new file. The same
// message id across files is one API call.
func TestScanClaudeSession_ShouldDedupAcrossFiles_GivenSameMessageID(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-xfile-dedup-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	line := `{"type":"assistant","timestamp":"2026-09-01T10:00:00.000Z","message":{"id":"msg_same","role":"assistant","model":"claude-opus-5","usage":{"input_tokens":1000,"output_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	other := `{"type":"assistant","timestamp":"2026-09-01T11:00:00.000Z","message":{"id":"msg_other","role":"assistant","model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	os.WriteFile(filepath.Join(projectDir, "first.jsonl"), []byte(line), 0o644)
	os.WriteFile(filepath.Join(projectDir, "resumed.jsonl"), []byte(line+other), 0o644)

	// Execute.
	result := getAgentSessionTokens(worktreePath)

	// Assert.
	if result.In != 1010 || result.Out != 101 {
		t.Errorf("expected 1010 in / 101 out after cross-file dedup, got %+v", result)
	}
}

// Two different requests can share a message id only when the requestId
// differs (a retried request); both must count.
func TestScanClaudeSession_ShouldKeepBoth_GivenSameMessageIDDifferentRequestID(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-reqid-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"assistant","requestId":"req_1","timestamp":"2026-09-01T10:00:00.000Z","message":{"id":"msg_x","role":"assistant","model":"claude-opus-5","usage":{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","requestId":"req_2","timestamp":"2026-09-01T10:00:05.000Z","message":{"id":"msg_x","role":"assistant","model":"claude-opus-5","usage":{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	os.WriteFile(filepath.Join(projectDir, "s.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionTokens(worktreePath)

	// Assert.
	if result.In != 200 {
		t.Errorf("expected both requests counted (200 in), got %+v", result)
	}
}

// The line that carries the largest output_tokens for a message is the
// final one; its whole usage block (not just the output figure) is priced,
// so fields that only settle at the end — iterations, speed — are honoured.
func TestScanClaudeSession_ShouldPriceFinalUsageBlock_GivenGrowingContentBlocks(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-final-usage-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"assistant","timestamp":"2026-09-01T10:00:00.000Z","message":{"id":"msg_g","role":"assistant","model":"claude-opus-5","usage":{"input_tokens":100,"output_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-09-01T10:00:01.000Z","message":{"id":"msg_g","role":"assistant","model":"claude-opus-5","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"speed":"fast"}}}
`
	os.WriteFile(filepath.Join(projectDir, "s.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionTokens(worktreePath)

	// Assert. 100 in + 50 out at fast-mode Opus 5 ($10/$50).
	want := 100*10.0/1_000_000 + 50*50.0/1_000_000
	if math.Abs(result.CostUSD-want) > 1e-9 {
		t.Errorf("expected fast-mode price %.9f from the final block, got %.9f", want, result.CostUSD)
	}
}

// Daily costs come from the same single pass as the totals, so the two can
// never disagree.
func TestGetAgentSessionDailyCosts_ShouldSumToTotalCost_GivenMultiDaySession(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-daily-eq-total-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"assistant","timestamp":"2026-09-01T23:00:00.000Z","message":{"id":"a","role":"assistant","model":"claude-fable-5-1","usage":{"input_tokens":1000,"output_tokens":500,"cache_creation_input_tokens":0,"cache_read_input_tokens":40000}}}
{"type":"assistant","timestamp":"2026-09-02T01:00:00.000Z","message":{"id":"b","role":"assistant","model":"claude-fable-5-1","usage":{"input_tokens":2000,"output_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	os.WriteFile(filepath.Join(projectDir, "s.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	daily := getAgentSessionDailyCosts(worktreePath)
	total := getAgentSessionTokens(worktreePath).CostUSD

	// Assert.
	var sum float64
	for _, c := range daily {
		sum += c
	}
	if math.Abs(sum-total) > 1e-12 {
		t.Errorf("daily sum %.12f != total %.12f", sum, total)
	}
}
