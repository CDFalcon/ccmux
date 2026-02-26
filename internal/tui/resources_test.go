package tui

import (
	"os"
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

func TestFormatResourceLine_ShouldIncludeTokens_GivenNonZeroTokens(t *testing.T) {
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
	expected := "CPU: 45%  Mem: 1.5Gb (3%)  Disk: 156Mb  Tokens: 2.3M"
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

func TestGetAgentSessionTokens_ShouldSumTokens_GivenNestedMessageUsage(t *testing.T) {
	// Setup.
	worktreePath := "/tmp/ccmux-test-tokens-" + t.Name()
	projectDir := setupSessionDir(t, worktreePath)

	jsonl := `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":300}}}
{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":80,"output_tokens":40,"cache_creation_input_tokens":0,"cache_read_input_tokens":150}}}
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
