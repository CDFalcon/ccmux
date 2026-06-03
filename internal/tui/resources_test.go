package tui

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatBytes_ShouldReturnGb_GivenGigabyteValue(t *testing.T) {
	// Setup.
	bytes := int64(2 * 1024 * 1024 * 1024)

	// Execute.
	result := formatBytes(bytes)

	// Assert.
	if result != "2.0Gb" {
		t.Errorf("expected '2.0Gb', got '%s'", result)
	}
}

func TestFormatBytes_ShouldReturnFractionalGb_GivenFractionalGigabytes(t *testing.T) {
	// Setup.
	bytes := int64(1.5 * 1024 * 1024 * 1024)

	// Execute.
	result := formatBytes(bytes)

	// Assert.
	if result != "1.5Gb" {
		t.Errorf("expected '1.5Gb', got '%s'", result)
	}
}

func TestFormatBytes_ShouldReturnMb_GivenMegabyteValue(t *testing.T) {
	// Setup.
	bytes := int64(256 * 1024 * 1024)

	// Execute.
	result := formatBytes(bytes)

	// Assert.
	if result != "256Mb" {
		t.Errorf("expected '256Mb', got '%s'", result)
	}
}

func TestFormatBytes_ShouldReturnKb_GivenSmallValue(t *testing.T) {
	// Setup.
	bytes := int64(512 * 1024)

	// Execute.
	result := formatBytes(bytes)

	// Assert.
	if result != "512Kb" {
		t.Errorf("expected '512Kb', got '%s'", result)
	}
}

func TestFormatBytes_ShouldReturnZeroMb_GivenZero(t *testing.T) {
	// Setup.
	bytes := int64(0)

	// Execute.
	result := formatBytes(bytes)

	// Assert.
	if result != "0Mb" {
		t.Errorf("expected '0Mb', got '%s'", result)
	}
}

func TestFormatResourceLine_ShouldReturnEmpty_GivenNil(t *testing.T) {
	// Execute.
	result := formatResourceLine(nil)

	// Assert.
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestFormatResourceLine_ShouldFormatAll_GivenResources(t *testing.T) {
	// Setup.
	r := &AgentResources{
		CPUPercent: 45,
		MemBytes:   int64(1.5 * 1024 * 1024 * 1024),
		MemPercent: 3,
		DiskBytes:  int64(156 * 1024 * 1024),
	}

	// Execute.
	result := formatResourceLine(r)

	// Assert.
	expected := "CPU: 45%  Mem: 1.5Gb (3%)  Disk: 156Mb"
	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
}

func TestFormatResourceLine_ShouldExcludeTokens_GivenNonZeroTokens(t *testing.T) {
	// Setup.
	r := &AgentResources{
		CPUPercent:  45,
		MemBytes:    int64(1.5 * 1024 * 1024 * 1024),
		MemPercent:  3,
		DiskBytes:   int64(156 * 1024 * 1024),
		TotalTokens: 2_340_000,
	}

	// Execute.
	result := formatResourceLine(r)

	// Assert.
	expected := "CPU: 45%  Mem: 1.5Gb (3%)  Disk: 156Mb"
	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
}

func TestFormatTokens_ShouldReturnMillions_GivenLargeValue(t *testing.T) {
	// Execute.
	result := formatTokens(5_500_000)

	// Assert.
	if result != "5.5M" {
		t.Errorf("expected '5.5M', got '%s'", result)
	}
}

func TestFormatTokens_ShouldReturnThousands_GivenMediumValue(t *testing.T) {
	// Execute.
	result := formatTokens(42_000)

	// Assert.
	if result != "42k" {
		t.Errorf("expected '42k', got '%s'", result)
	}
}

func TestFormatTokens_ShouldReturnEmpty_GivenZero(t *testing.T) {
	// Execute.
	result := formatTokens(0)

	// Assert.
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestComputeCPUPercent_ShouldReturnCorrectPct_GivenDelta(t *testing.T) {
	// Setup.
	prevTicks := int64(1000)
	currTicks := int64(1200)
	deltaSeconds := 2.0
	clkTck := int64(100)
	numCPU := 1

	// Execute.
	result := computeCPUPercent(prevTicks, currTicks, deltaSeconds, clkTck, numCPU)

	// Assert.
	if result != 100.0 {
		t.Errorf("expected 100.0, got %.2f", result)
	}
}

func TestComputeCPUPercent_ShouldNormalizeByNCPU_GivenMultipleCPUs(t *testing.T) {
	// Setup.
	prevTicks := int64(1000)
	currTicks := int64(1200)
	deltaSeconds := 2.0
	clkTck := int64(100)
	numCPU := 4

	// Execute.
	result := computeCPUPercent(prevTicks, currTicks, deltaSeconds, clkTck, numCPU)

	// Assert.
	expected := 25.0
	if result != expected {
		t.Errorf("expected %.2f, got %.2f", expected, result)
	}
}

func TestComputeCPUPercent_ShouldClampToZero_GivenNegativeDelta(t *testing.T) {
	// Setup.
	prevTicks := int64(1200)
	currTicks := int64(1000)
	deltaSeconds := 2.0
	clkTck := int64(100)
	numCPU := 1

	// Execute.
	result := computeCPUPercent(prevTicks, currTicks, deltaSeconds, clkTck, numCPU)

	// Assert.
	if result != 0.0 {
		t.Errorf("expected 0.0, got %.2f", result)
	}
}

func TestComputeCPUPercent_ShouldReturnZero_GivenZeroClkTck(t *testing.T) {
	// Execute.
	result := computeCPUPercent(0, 100, 2.0, 0, 1)

	// Assert.
	if result != 0.0 {
		t.Errorf("expected 0.0, got %.2f", result)
	}
}

func TestFindDescendants_ShouldReturnAllDescendants_GivenProcessTree(t *testing.T) {
	// Setup.
	procs := map[int]*procInfo{
		1:   {pid: 1, ppid: 0, rss: 0},
		100: {pid: 100, ppid: 1, rss: 1000},
		200: {pid: 200, ppid: 100, rss: 2000},
		300: {pid: 300, ppid: 100, rss: 500},
		400: {pid: 400, ppid: 200, rss: 1500},
		999: {pid: 999, ppid: 1, rss: 5000},
	}

	// Execute.
	result := findDescendants(100, procs)

	// Assert.
	resultSet := make(map[int]bool)
	for _, pid := range result {
		resultSet[pid] = true
	}
	expectedPIDs := []int{100, 200, 300, 400}
	for _, pid := range expectedPIDs {
		if !resultSet[pid] {
			t.Errorf("expected PID %d in descendants", pid)
		}
	}
	if resultSet[1] {
		t.Error("PID 1 should not be in descendants")
	}
	if resultSet[999] {
		t.Error("PID 999 should not be in descendants")
	}
	if len(result) != 4 {
		t.Errorf("expected 4 descendants, got %d", len(result))
	}
}

func setupSessionDir(t *testing.T, worktreePath string) string {
	t.Helper()
	homeDir, _ := os.UserHomeDir()
	projectKey := strings.ReplaceAll(worktreePath, "/", "-")
	projectDir := filepath.Join(homeDir, ".claude", "projects", projectKey)
	os.MkdirAll(projectDir, 0o755)
	t.Cleanup(func() { os.RemoveAll(projectDir) })
	return projectDir
}

func TestGetAgentSessionTokens_ShouldSumTokens_GivenDistinctAPITurns(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-tokens-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}
{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":80,"output_tokens":40,"cache_creation_input_tokens":0,"cache_read_input_tokens":150}}}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionTokens(worktreePath)

	// Assert.
	expectedTotal := int64(100 + 50 + 200 + 300 + 80 + 40 + 0 + 150)
	if result.Total != expectedTotal {
		t.Errorf("expected total %d, got %d", expectedTotal, result.Total)
	}
	if result.In != 180 {
		t.Errorf("expected In 180, got %d", result.In)
	}
	if result.Out != 90 {
		t.Errorf("expected Out 90, got %d", result.Out)
	}
	if result.CacheRead != 450 {
		t.Errorf("expected CacheRead 450, got %d", result.CacheRead)
	}
	if result.CacheCreate != 200 {
		t.Errorf("expected CacheCreate 200, got %d", result.CacheCreate)
	}
}

func TestGetAgentSessionTokens_ShouldDedup_GivenDuplicateContentBlocks(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-dedup-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":100,"output_tokens":8,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}
{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}
{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionTokens(worktreePath)

	// Assert.
	if result.In != 100 {
		t.Errorf("expected In 100 (deduped), got %d", result.In)
	}
	if result.Out != 50 {
		t.Errorf("expected Out 50 (max from group), got %d", result.Out)
	}
	if result.CacheRead != 300 {
		t.Errorf("expected CacheRead 300 (deduped), got %d", result.CacheRead)
	}
	if result.CacheCreate != 200 {
		t.Errorf("expected CacheCreate 200 (deduped), got %d", result.CacheCreate)
	}
}

func TestGetAgentSessionTokens_ShouldSkipSyntheticMessages(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-synthetic-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":300}}}
{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","stop_reason":"stop_sequence","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":80,"output_tokens":40,"cache_creation_input_tokens":0,"cache_read_input_tokens":200}}}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionTokens(worktreePath)

	// Assert.
	if result.In != 180 {
		t.Errorf("expected In 180, got %d", result.In)
	}
	if result.Out != 90 {
		t.Errorf("expected Out 90, got %d", result.Out)
	}
}

func TestGetAgentSessionTokens_ShouldReturnZero_GivenNonAssistantMessages(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-noassist-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"file-history-snapshot"}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionTokens(worktreePath)

	// Assert.
	if result.Total != 0 {
		t.Errorf("expected 0, got %d", result.Total)
	}
}

func TestFormatResourceLine_ShouldPrefixTilde_GivenReflinked(t *testing.T) {
	// Setup.
	r := &AgentResources{
		CPUPercent:    10,
		MemBytes:      int64(512 * 1024 * 1024),
		MemPercent:    1,
		DiskBytes:     int64(2 * 1024 * 1024),
		DiskReflinked: true,
	}

	// Execute.
	result := formatResourceLine(r)

	// Assert.
	expected := "CPU: 10%  Mem: 512Mb (1%)  Disk: ~2Mb"
	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
}

func TestGetDiskUsageIncremental_ShouldReturnZero_GivenCleanRepo(t *testing.T) {
	// Setup.
	tmpDir := t.TempDir()
	exec.Command("git", "init", tmpDir).Run()
	exec.Command("git", "-C", tmpDir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", tmpDir, "config", "user.name", "test").Run()
	exec.Command("git", "-C", tmpDir, "commit", "--allow-empty", "-m", "init").Run()

	// Execute.
	result := getDiskUsageIncremental(tmpDir)

	// Assert.
	if result != 0 {
		t.Errorf("expected 0, got %d", result)
	}
}

func TestGetDiskUsageIncremental_ShouldCountModifiedFiles_GivenChanges(t *testing.T) {
	// Setup.
	tmpDir := t.TempDir()
	exec.Command("git", "init", tmpDir).Run()
	exec.Command("git", "-C", tmpDir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", tmpDir, "config", "user.name", "test").Run()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("hello world"), 0o644)
	exec.Command("git", "-C", tmpDir, "add", ".").Run()
	exec.Command("git", "-C", tmpDir, "commit", "-m", "init").Run()
	os.WriteFile(testFile, []byte("modified content here"), 0o644)

	// Execute.
	result := getDiskUsageIncremental(tmpDir)

	// Assert.
	info, _ := os.Stat(testFile)
	if result != info.Size() {
		t.Errorf("expected %d, got %d", info.Size(), result)
	}
}

func TestGetDiskUsageIncremental_ShouldCountUntrackedFiles_GivenNewFiles(t *testing.T) {
	// Setup.
	tmpDir := t.TempDir()
	exec.Command("git", "init", tmpDir).Run()
	exec.Command("git", "-C", tmpDir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", tmpDir, "config", "user.name", "test").Run()
	exec.Command("git", "-C", tmpDir, "commit", "--allow-empty", "-m", "init").Run()
	newFile := filepath.Join(tmpDir, "new.txt")
	os.WriteFile(newFile, []byte("new content"), 0o644)

	// Execute.
	result := getDiskUsageIncremental(tmpDir)

	// Assert.
	info, _ := os.Stat(newFile)
	if result != info.Size() {
		t.Errorf("expected %d, got %d", info.Size(), result)
	}
}

func TestFindDescendants_ShouldReturnOnlyRoot_GivenNoChildren(t *testing.T) {
	// Setup.
	procs := map[int]*procInfo{
		1:   {pid: 1, ppid: 0},
		100: {pid: 100, ppid: 1},
		200: {pid: 200, ppid: 1},
	}

	// Execute.
	result := findDescendants(100, procs)

	// Assert.
	if len(result) != 1 {
		t.Errorf("expected 1 descendant (just root), got %d", len(result))
	}
	if result[0] != 100 {
		t.Errorf("expected root PID 100, got %d", result[0])
	}
}

func TestEstimateCost_ShouldUseNewOpusPricing_GivenOpus46(t *testing.T) {
	// Setup.
	usage := claudeUsage{
		InputTokens:              1000,
		OutputTokens:             1000,
		CacheReadInputTokens:     1_000_000,
		CacheCreationInputTokens: 100_000,
	}

	// Execute.
	cost := estimateCost("claude-opus-4-6", usage)

	// Assert.
	expected := 1000*5.0/1e6 + 1000*25.0/1e6 + 1_000_000*0.50/1e6 + 100_000*6.25/1e6
	if cost != expected {
		t.Errorf("expected %.6f, got %.6f", expected, cost)
	}
}

func TestEstimateCost_ShouldUseOldOpusPricing_GivenOpus4(t *testing.T) {
	// Setup.
	usage := claudeUsage{
		InputTokens:              1000,
		OutputTokens:             1000,
		CacheReadInputTokens:     1_000_000,
		CacheCreationInputTokens: 100_000,
	}

	// Execute.
	cost := estimateCost("claude-opus-4-20250514", usage)

	// Assert.
	expected := 1000*15.0/1e6 + 1000*75.0/1e6 + 1_000_000*1.50/1e6 + 100_000*18.75/1e6
	if cost != expected {
		t.Errorf("expected %.6f, got %.6f", expected, cost)
	}
}

func TestEstimateCost_ShouldUseSonnetPricing_GivenSonnet(t *testing.T) {
	// Setup.
	usage := claudeUsage{
		InputTokens:  10_000,
		OutputTokens: 5_000,
	}

	// Execute.
	cost := estimateCost("claude-sonnet-4-20250514", usage)

	// Assert.
	expected := 10_000*3.0/1e6 + 5_000*15.0/1e6
	if cost != expected {
		t.Errorf("expected %.6f, got %.6f", expected, cost)
	}
}

func TestEstimateCost_ShouldUseHaikuPricing_GivenHaiku(t *testing.T) {
	// Setup.
	usage := claudeUsage{
		InputTokens:          10_000,
		OutputTokens:         5_000,
		CacheReadInputTokens: 100_000,
	}

	// Execute.
	cost := estimateCost("claude-haiku-4-5-20251001", usage)

	// Assert.
	expected := 10_000*1.0/1e6 + 5_000*5.0/1e6 + 100_000*0.10/1e6
	if math.Abs(cost-expected) > 1e-10 {
		t.Errorf("expected %.6f, got %.6f", expected, cost)
	}
}

// TestEstimateCost_ShouldMatchClaudeInternal_GivenOpus47Sample pins ccmux's
// cost calculation against Claude's own internal computation, captured from
// `claude -p "echo" --output-format json` (which returns the same total_cost_usd
// field Anthropic bills against). Any drift here means our pricing table no
// longer matches Claude's, and the displayed cost will be misleading.
//
// Claude reported total_cost_usd = 0.06278625 for this exact usage block,
// which the assertion below recomputes from list pricing.
func TestEstimateCost_ShouldMatchClaudeInternal_GivenOpus47Sample(t *testing.T) {
	// Setup. Usage block from a real `claude -p` invocation against opus-4-7.
	usage := claudeUsage{
		InputTokens:              6,
		OutputTokens:             11,
		CacheReadInputTokens:     23800,
		CacheCreationInputTokens: 8093,
		CacheCreation: claudeCacheCreation{
			Ephemeral5mInputTokens: 8093,
			Ephemeral1hInputTokens: 0,
		},
	}

	// Execute.
	cost := estimateCost("claude-opus-4-7", usage)

	// Assert. Claude's own total_cost_usd for this sample.
	const claudeReported = 0.06278625
	if math.Abs(cost-claudeReported) > 1e-9 {
		t.Errorf("expected %.10f (Claude's own computation), got %.10f", claudeReported, cost)
	}
}

// TestEstimateCost_ShouldPrice1hCacheAt2x_GivenEphemeral1hWrite captures the
// bug that prompted this change: the previous estimator priced ALL cache
// creation at the 5m rate (1.25x input), undercounting 1h cache writes —
// which are the default for modern Claude Code — by ~37%.
func TestEstimateCost_ShouldPrice1hCacheAt2x_GivenEphemeral1hWrite(t *testing.T) {
	// Setup. 1M tokens written to the 1-hour cache at opus-4-7 ($5/M input).
	usage := claudeUsage{
		CacheCreationInputTokens: 1_000_000,
		CacheCreation: claudeCacheCreation{
			Ephemeral1hInputTokens: 1_000_000,
		},
	}

	// Execute.
	cost := estimateCost("claude-opus-4-7", usage)

	// Assert. 1M tokens × ($5 × 2.0x) / 1M = $10.00.
	const expected = 10.0
	if math.Abs(cost-expected) > 1e-9 {
		t.Errorf("expected %.4f (1h cache at 2.0x input), got %.4f", expected, cost)
	}
}

// TestEstimateCost_ShouldPrice5mCacheAt125x_GivenEphemeral5mWrite confirms
// the 5m path is unchanged and still uses 1.25x — guards against a future
// refactor accidentally swapping the two multipliers.
func TestEstimateCost_ShouldPrice5mCacheAt125x_GivenEphemeral5mWrite(t *testing.T) {
	// Setup. 1M tokens written to the 5-minute cache at opus-4-7 ($5/M input).
	usage := claudeUsage{
		CacheCreationInputTokens: 1_000_000,
		CacheCreation: claudeCacheCreation{
			Ephemeral5mInputTokens: 1_000_000,
		},
	}

	// Execute.
	cost := estimateCost("claude-opus-4-7", usage)

	// Assert. 1M × ($5 × 1.25x) / 1M = $6.25.
	const expected = 6.25
	if math.Abs(cost-expected) > 1e-9 {
		t.Errorf("expected %.4f (5m cache at 1.25x input), got %.4f", expected, cost)
	}
}

// TestEstimateCost_ShouldFallBackToFlat5m_GivenLegacyUsageWithoutSplit pins
// the backward-compat path: older JSONL entries (or test fixtures) that only
// carry the flat `cache_creation_input_tokens` field without the
// `cache_creation` sub-object should still be priced — at the 5m rate, which
// matches the pre-fix behaviour and what every prior test was written
// against.
func TestEstimateCost_ShouldFallBackToFlat5m_GivenLegacyUsageWithoutSplit(t *testing.T) {
	// Setup. No `cache_creation` sub-object — only the flat total.
	usage := claudeUsage{
		CacheCreationInputTokens: 1_000_000,
	}

	// Execute.
	cost := estimateCost("claude-opus-4-7", usage)

	// Assert. Falls back to 5m pricing: 1M × ($5 × 1.25x) / 1M = $6.25.
	const expected = 6.25
	if math.Abs(cost-expected) > 1e-9 {
		t.Errorf("expected %.4f (legacy fallback to 5m), got %.4f", expected, cost)
	}
}

// TestGetAgentSessionTokens_ShouldDedupByMessageID_GivenContentBlockSplit
// guards the canonical fix: a single assistant turn that Claude Code split
// into multiple JSONL lines (one per content block — thinking, text,
// tool_use) shares one message.id. We must fold those into ONE billable
// turn, not three.
//
// Without this fix the dedup logic only collapsed *consecutive entries with
// identical input signatures*, which is correct in most cases but brittle
// when two genuinely distinct turns happened to share input counts.
func TestGetAgentSessionTokens_ShouldDedupByMessageID_GivenContentBlockSplit(t *testing.T) {
	// Setup. Three JSONL entries that share message.id "msg_AAA" — they're
	// the thinking, text, and tool_use blocks of one assistant turn. Their
	// output_tokens grows as more of the message is logged (Claude appears
	// to re-emit the running total).
	worktreePath := "/tmp/ccmux-test-dedup-byid-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"assistant","message":{"id":"msg_AAA","role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":100,"output_tokens":80,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}
{"type":"assistant","message":{"id":"msg_AAA","role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":100,"output_tokens":250,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}
{"type":"assistant","message":{"id":"msg_AAA","role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":100,"output_tokens":900,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionTokens(worktreePath)

	// Assert. One folded turn: input counted once, output taken as the max.
	if result.In != 100 {
		t.Errorf("expected In 100 (one folded turn), got %d", result.In)
	}
	if result.Out != 900 {
		t.Errorf("expected Out 900 (max across content blocks), got %d", result.Out)
	}
	if result.CacheRead != 300 {
		t.Errorf("expected CacheRead 300 (one folded turn), got %d", result.CacheRead)
	}
	if result.CacheCreate != 200 {
		t.Errorf("expected CacheCreate 200 (one folded turn), got %d", result.CacheCreate)
	}
}

// TestGetAgentSessionTokens_ShouldNotMergeDistinctTurns_GivenIdenticalSignatures
// is the contrapositive: two genuinely distinct assistant turns that happen
// to share input counts (small, common turns with full cache hits) but
// carry distinct message.ids must NOT be folded together.
func TestGetAgentSessionTokens_ShouldNotMergeDistinctTurns_GivenIdenticalSignatures(t *testing.T) {
	// Setup. Two distinct turns, same input/cache numbers, different ids.
	worktreePath := "/tmp/ccmux-test-distinct-byid-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"assistant","message":{"id":"msg_FIRST","role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":1000}}}
{"type":"assistant","message":{"id":"msg_SECOND","role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":1000}}}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionTokens(worktreePath)

	// Assert. Both turns billed.
	if result.In != 2 {
		t.Errorf("expected In 2 (two distinct turns), got %d", result.In)
	}
	if result.Out != 100 {
		t.Errorf("expected Out 100 (two distinct turns), got %d", result.Out)
	}
	if result.CacheRead != 2000 {
		t.Errorf("expected CacheRead 2000 (two distinct turns), got %d", result.CacheRead)
	}
}

func TestIsNewOpus_ShouldReturnTrue_GivenOpus45Plus(t *testing.T) {
	// Assert.
	if !isNewOpus("claude-opus-4-5-20251101") {
		t.Error("expected true for opus-4-5")
	}
	if !isNewOpus("claude-opus-4-6") {
		t.Error("expected true for opus-4-6")
	}
}

func TestIsNewOpus_ShouldReturnFalse_GivenOldOpus(t *testing.T) {
	// Assert.
	if isNewOpus("claude-opus-4-20250514") {
		t.Error("expected false for opus-4")
	}
	if isNewOpus("claude-opus-4-1-20250805") {
		t.Error("expected false for opus-4-1")
	}
	if isNewOpus("claude-opus-3") {
		t.Error("expected false for opus-3")
	}
}

func TestIsProcessTreeActiveFromPID_ShouldReturnTrue_GivenSignificantCPUDelta(t *testing.T) {
	// Setup.
	procs := map[int]*procInfo{
		100: {pid: 100, ppid: 1, rss: 1000},
		200: {pid: 200, ppid: 100, rss: 2000},
	}
	currentTicks := map[int]int64{100: 500, 200: 300}
	prevTicks := map[int]int64{100: 400, 200: 200}
	clkTck := int64(100)

	// Execute.
	result := isProcessTreeActiveFromPID(100, procs, currentTicks, prevTicks, clkTck)

	// Assert.
	if !result {
		t.Error("expected active when CPU delta is significant")
	}
}

func TestIsProcessTreeActiveFromPID_ShouldReturnFalse_GivenNoCPUDelta(t *testing.T) {
	// Setup.
	procs := map[int]*procInfo{
		100: {pid: 100, ppid: 1, rss: 1000},
		200: {pid: 200, ppid: 100, rss: 2000},
	}
	currentTicks := map[int]int64{100: 500, 200: 300}
	prevTicks := map[int]int64{100: 500, 200: 300}
	clkTck := int64(100)

	// Execute.
	result := isProcessTreeActiveFromPID(100, procs, currentTicks, prevTicks, clkTck)

	// Assert.
	if result {
		t.Error("expected inactive when no CPU delta")
	}
}

func TestIsProcessTreeActiveFromPID_ShouldReturnFalse_GivenNoPrevTicks(t *testing.T) {
	// Setup.
	procs := map[int]*procInfo{
		100: {pid: 100, ppid: 1, rss: 1000},
	}
	currentTicks := map[int]int64{100: 500}
	prevTicks := map[int]int64{}
	clkTck := int64(100)

	// Execute.
	result := isProcessTreeActiveFromPID(100, procs, currentTicks, prevTicks, clkTck)

	// Assert.
	if result {
		t.Error("expected inactive when no previous ticks")
	}
}

func TestIsProcessTreeActiveFromPID_ShouldIgnoreNewlySpawnedChild_GivenPhantomDelta(t *testing.T) {
	// Setup. The root process is idle (no delta), but a short-lived child
	// (e.g. a hook or the ccmux forwarder) appeared since the previous sample
	// and racked up CPU time. It only exists in currentTicks, so its CPU must
	// NOT be counted as agent activity.
	procs := map[int]*procInfo{
		100: {pid: 100, ppid: 1, rss: 1000},
		999: {pid: 999, ppid: 100, rss: 500},
	}
	currentTicks := map[int]int64{100: 500, 999: 9999}
	prevTicks := map[int]int64{100: 500}
	clkTck := int64(100)

	// Execute.
	result := isProcessTreeActiveFromPID(100, procs, currentTicks, prevTicks, clkTck)

	// Assert.
	if result {
		t.Error("expected inactive: newly spawned child must not count as agent activity")
	}
}

func TestIsProcessTreeActiveFromPID_ShouldIgnoreExitedChild_GivenStalePrevTicks(t *testing.T) {
	// Setup. A child that existed in the previous sample has since exited and
	// is gone from the current process tree. Its absence must not be treated
	// as activity, and the still-idle root must remain inactive.
	procs := map[int]*procInfo{
		100: {pid: 100, ppid: 1, rss: 1000},
	}
	currentTicks := map[int]int64{100: 500}
	prevTicks := map[int]int64{100: 500, 999: 4000}
	clkTck := int64(100)

	// Execute.
	result := isProcessTreeActiveFromPID(100, procs, currentTicks, prevTicks, clkTck)

	// Assert.
	if result {
		t.Error("expected inactive: an exited child must not count as agent activity")
	}
}

func TestIsProcessTreeActiveFromPID_ShouldReturnFalse_GivenSubThresholdIdleNoise(t *testing.T) {
	// Setup. A persistent process burns a small amount of CPU between samples
	// (idle render / event-loop noise) that is below cpuActiveThreshold.
	procs := map[int]*procInfo{
		100: {pid: 100, ppid: 1, rss: 1000},
	}
	// 10 ticks / 100 = 0.10 CPU-seconds, below the 0.20 threshold.
	currentTicks := map[int]int64{100: 510}
	prevTicks := map[int]int64{100: 500}
	clkTck := int64(100)

	// Execute.
	result := isProcessTreeActiveFromPID(100, procs, currentTicks, prevTicks, clkTck)

	// Assert.
	if result {
		t.Error("expected inactive when CPU delta is below the active threshold")
	}
}

func TestExtractDate_ShouldReturnDate_GivenValidTimestamp(t *testing.T) {
	// Execute.
	result := extractDate("2026-03-25T19:02:43.170Z")

	// Assert.
	if result != "2026-03-25" {
		t.Errorf("expected '2026-03-25', got '%s'", result)
	}
}

func TestExtractDate_ShouldReturnUnknown_GivenShortString(t *testing.T) {
	// Execute.
	result := extractDate("short")

	// Assert.
	if result != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", result)
	}
}

func TestExtractDate_ShouldReturnUnknown_GivenEmptyString(t *testing.T) {
	// Execute.
	result := extractDate("")

	// Assert.
	if result != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", result)
	}
}

func TestGetAgentSessionDailyCosts_ShouldSplitByDay_GivenMultipleDays(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-daily-costs-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"assistant","timestamp":"2026-03-24T23:30:00.000Z","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":1000,"output_tokens":500,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-03-25T01:00:00.000Z","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":2000,"output_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionDailyCosts(worktreePath)

	// Assert.
	if len(result) != 2 {
		t.Errorf("expected 2 days, got %d: %v", len(result), result)
	}
	if result["2026-03-24"] <= 0 {
		t.Errorf("expected positive cost for 2026-03-24, got %.6f", result["2026-03-24"])
	}
	if result["2026-03-25"] <= 0 {
		t.Errorf("expected positive cost for 2026-03-25, got %.6f", result["2026-03-25"])
	}
}

func TestGetAgentSessionDailyCosts_ShouldReturnSingleDay_GivenSameDayMessages(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-daily-single-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"assistant","timestamp":"2026-03-25T10:00:00.000Z","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":1000,"output_tokens":500,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-03-25T12:00:00.000Z","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":2000,"output_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionDailyCosts(worktreePath)

	// Assert.
	if len(result) != 1 {
		t.Errorf("expected 1 day, got %d: %v", len(result), result)
	}
	if result["2026-03-25"] <= 0 {
		t.Errorf("expected positive cost for 2026-03-25, got %.6f", result["2026-03-25"])
	}
}

func TestGetAgentSessionDailyCosts_ShouldReturnEmpty_GivenNoAssistantMessages(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-daily-empty-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"user","timestamp":"2026-03-25T10:00:00.000Z","message":{"role":"user","content":"hello"}}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionDailyCosts(worktreePath)

	// Assert.
	if len(result) != 0 {
		t.Errorf("expected 0 days, got %d: %v", len(result), result)
	}
}

func TestGetAgentSessionDailyCosts_ShouldSkipSynthetic_GivenSyntheticModel(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-daily-synthetic-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"assistant","timestamp":"2026-03-25T10:00:00.000Z","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":1000,"output_tokens":500,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-03-25T11:00:00.000Z","message":{"role":"assistant","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionDailyCosts(worktreePath)

	// Assert.
	if len(result) != 1 {
		t.Errorf("expected 1 day, got %d", len(result))
	}
	expectedCost := estimateCost("claude-sonnet-4-20250514", claudeUsage{
		InputTokens:  1000,
		OutputTokens: 500,
	})
	if math.Abs(result["2026-03-25"]-expectedCost) > 1e-10 {
		t.Errorf("expected %.6f, got %.6f", expectedCost, result["2026-03-25"])
	}
}

func TestGetAgentSessionDailyCosts_ShouldDedup_GivenDuplicateContentBlocks(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-daily-dedup-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"assistant","timestamp":"2026-03-25T10:00:00.000Z","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":100,"output_tokens":8,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}
{"type":"assistant","timestamp":"2026-03-25T10:00:01.000Z","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}
`
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(jsonl), 0o644)

	// Execute.
	result := getAgentSessionDailyCosts(worktreePath)

	// Assert.
	expectedCost := estimateCost("claude-sonnet-4-20250514", claudeUsage{
		InputTokens:              100,
		OutputTokens:             50,
		CacheCreationInputTokens: 200,
		CacheReadInputTokens:     300,
	})
	if math.Abs(result["2026-03-25"]-expectedCost) > 1e-10 {
		t.Errorf("expected %.6f, got %.6f", expectedCost, result["2026-03-25"])
	}
}
