package otelcollector

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// metricsPayload assembles a minimal OTLP/HTTP+JSON envelope with one
// `claude_code.cost.usage` data point. Helper to keep the test bodies
// focused on the assertion rather than the wire format.
func metricsPayload(t *testing.T, dpAttrs, resAttrs map[string]string, costUSD float64) []byte {
	t.Helper()
	mkAttrs := func(m map[string]string) []map[string]any {
		out := make([]map[string]any, 0, len(m))
		for k, v := range m {
			out = append(out, map[string]any{
				"key":   k,
				"value": map[string]any{"stringValue": v},
			})
		}
		return out
	}
	env := map[string]any{
		"resourceMetrics": []map[string]any{{
			"resource": map[string]any{"attributes": mkAttrs(resAttrs)},
			"scopeMetrics": []map[string]any{{
				"metrics": []map[string]any{{
					"name": "claude_code.cost.usage",
					"sum": map[string]any{
						"dataPoints": []map[string]any{{
							"attributes":   mkAttrs(dpAttrs),
							"timeUnixNano": "1700000000000000000",
							"asDouble":     costUSD,
						}},
					},
				}},
			}},
		}},
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// withCollector starts a collector with a temp endpoint file and shuts it
// down at the end of the test. Returns the running collector. Using
// TempDir for the endpoint file keeps tests from clobbering each other's
// ~/.ccmux/otel-endpoint when run in parallel.
func withCollector(t *testing.T) *Collector {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("CCMUX_OTEL_ENDPOINT_FILE", filepath.Join(tmp, "endpoint"))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	c, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return c
}

func TestCollector_ShouldAttributeCostToAgentID_GivenResourceAttribute(t *testing.T) {
	// Setup. Launcher script sets OTEL_RESOURCE_ATTRIBUTES, so the agent.id
	// arrives on the resource block, not the data point. This is the
	// canonical hot path.
	c := withCollector(t)
	payload := metricsPayload(t,
		nil,
		map[string]string{"ccmux.agent.id": "agent-abc"},
		0.0234,
	)

	// Execute. POST the metric to /v1/metrics.
	resp, err := http.Post(c.Endpoint()+"/v1/metrics", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	// Assert.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	got, ok := c.Cost("agent-abc")
	if !ok {
		t.Fatal("expected cost to be recorded")
	}
	if math.Abs(got-0.0234) > 1e-9 {
		t.Errorf("expected $0.0234, got $%.6f", got)
	}
}

func TestCollector_ShouldSumAcrossExports_GivenDeltaTemporality(t *testing.T) {
	// Setup. Claude Code's cost.usage is a delta-temporality sum — each
	// export reports new USD for that window. Three exports of $0.01 each
	// should land at $0.03 cumulative.
	c := withCollector(t)
	res := map[string]string{"ccmux.agent.id": "agent-delta"}

	// Execute.
	for i := 0; i < 3; i++ {
		resp, err := http.Post(c.Endpoint()+"/v1/metrics", "application/json",
			bytes.NewReader(metricsPayload(t, nil, res, 0.01)))
		if err != nil {
			t.Fatalf("POST %d: %v", i, err)
		}
		resp.Body.Close()
	}

	// Assert.
	got, ok := c.Cost("agent-delta")
	if !ok {
		t.Fatal("expected cost to be recorded")
	}
	if math.Abs(got-0.03) > 1e-9 {
		t.Errorf("expected $0.03, got $%.6f", got)
	}
}

func TestCollector_ShouldPreferDataPointAttrOverResource_GivenBothPresent(t *testing.T) {
	// Setup. If both attribute scopes carry an agent.id the data-point
	// override wins (per-export override semantics). Pins the precedence
	// against an accidental refactor.
	c := withCollector(t)
	payload := metricsPayload(t,
		map[string]string{"ccmux.agent.id": "dp-wins"},
		map[string]string{"ccmux.agent.id": "res-loses"},
		1.0,
	)

	// Execute.
	resp, err := http.Post(c.Endpoint()+"/v1/metrics", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	// Assert.
	if _, ok := c.Cost("res-loses"); ok {
		t.Error("expected resource-attr id to not receive cost when datapoint-attr id is present")
	}
	got, ok := c.Cost("dp-wins")
	if !ok || math.Abs(got-1.0) > 1e-9 {
		t.Errorf("expected dp-wins to receive $1.00, got %.4f ok=%v", got, ok)
	}
}

func TestCollector_ShouldFallBackToSessionID_GivenNoAgentStamp(t *testing.T) {
	// Setup. Agents from a pre-upgrade ccmux won't have the ccmux.agent.id
	// stamp but Claude Code always emits session.id. The cost should still
	// land in DailyCosts() (under a "session:" pseudo-id), so the running
	// total isn't silently dropped.
	c := withCollector(t)
	payload := metricsPayload(t,
		map[string]string{"session.id": "claude-sess-xyz"},
		nil,
		0.5,
	)

	// Execute.
	resp, err := http.Post(c.Endpoint()+"/v1/metrics", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	// Assert.
	if _, ok := c.Cost("claude-sess-xyz"); ok {
		t.Error("session id should NOT match the bare agent lookup")
	}
	if _, ok := c.Cost("session:claude-sess-xyz"); !ok {
		t.Error("session-scoped pseudo-id should be retrievable")
	}
	daily := c.DailyCosts()
	var sum float64
	for _, v := range daily {
		sum += v
	}
	if math.Abs(sum-0.5) > 1e-9 {
		t.Errorf("expected daily sum $0.50, got $%.4f", sum)
	}
}

func TestCollector_ShouldIgnoreNonCostMetrics_GivenMixedPayload(t *testing.T) {
	// Setup. A payload with token.usage AND cost.usage — only the latter
	// should contribute to cost. (Claude Code emits both side-by-side.)
	c := withCollector(t)
	res := map[string]string{"ccmux.agent.id": "mixed"}
	env := map[string]any{
		"resourceMetrics": []map[string]any{{
			"resource": map[string]any{"attributes": []map[string]any{{
				"key": "ccmux.agent.id", "value": map[string]any{"stringValue": "mixed"},
			}}},
			"scopeMetrics": []map[string]any{{
				"metrics": []map[string]any{
					{"name": "claude_code.token.usage", "sum": map[string]any{
						"dataPoints": []map[string]any{{
							"attributes":   []map[string]any{},
							"timeUnixNano": "1700000000000000000",
							"asDouble":     999.0, // 999 tokens — must NOT add to cost
						}},
					}},
					{"name": "claude_code.cost.usage", "sum": map[string]any{
						"dataPoints": []map[string]any{{
							"attributes":   []map[string]any{},
							"timeUnixNano": "1700000000000000000",
							"asDouble":     0.42,
						}},
					}},
				},
			}},
		}},
	}
	b, _ := json.Marshal(env)
	_ = res

	// Execute.
	resp, err := http.Post(c.Endpoint()+"/v1/metrics", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	// Assert.
	got, ok := c.Cost("mixed")
	if !ok {
		t.Fatal("expected cost to be recorded for mixed payload")
	}
	if math.Abs(got-0.42) > 1e-9 {
		t.Errorf("expected $0.42 (only cost metric), got $%.4f", got)
	}
}

func TestCollector_ShouldRecordPerDayBreakdown_GivenTimestampedDatapoints(t *testing.T) {
	// Setup. Two data points stamped two different days. DailyCostsForAgent
	// must split them so the dailycost store can persist accurate per-day
	// totals at teardown.
	c := withCollector(t)
	res := map[string]string{"ccmux.agent.id": "agent-days"}

	day1Nano := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC).UnixNano()
	day2Nano := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC).UnixNano()

	mkSingle := func(nanos int64, usd float64) []byte {
		env := map[string]any{
			"resourceMetrics": []map[string]any{{
				"resource": map[string]any{"attributes": []map[string]any{{
					"key": "ccmux.agent.id", "value": map[string]any{"stringValue": "agent-days"},
				}}},
				"scopeMetrics": []map[string]any{{
					"metrics": []map[string]any{{"name": "claude_code.cost.usage",
						"sum": map[string]any{"dataPoints": []map[string]any{{
							"attributes":   []map[string]any{},
							"timeUnixNano": jsonNum(nanos),
							"asDouble":     usd,
						}}},
					}},
				}},
			}},
		}
		b, _ := json.Marshal(env)
		return b
	}

	// Execute.
	for _, payload := range [][]byte{mkSingle(day1Nano, 1.0), mkSingle(day2Nano, 2.5)} {
		resp, err := http.Post(c.Endpoint()+"/v1/metrics", "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		resp.Body.Close()
	}
	_ = res

	// Assert.
	daily := c.DailyCostsForAgent("agent-days")
	if got := daily["2026-06-01"]; math.Abs(got-1.0) > 1e-9 {
		t.Errorf("expected 2026-06-01 = $1.00, got $%.4f", got)
	}
	if got := daily["2026-06-02"]; math.Abs(got-2.5) > 1e-9 {
		t.Errorf("expected 2026-06-02 = $2.50, got $%.4f", got)
	}
}

func TestCollector_ShouldForgetAgent_GivenForgetAgentCall(t *testing.T) {
	// Setup. Two agents with cost recorded.
	c := withCollector(t)
	for _, id := range []string{"keep", "drop"} {
		resp, err := http.Post(c.Endpoint()+"/v1/metrics", "application/json",
			bytes.NewReader(metricsPayload(t, nil, map[string]string{"ccmux.agent.id": id}, 1.0)))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		resp.Body.Close()
	}

	// Execute.
	c.ForgetAgent("drop")

	// Assert.
	if _, ok := c.Cost("drop"); ok {
		t.Error("expected forgotten agent to have no cost")
	}
	if _, ok := c.Cost("keep"); !ok {
		t.Error("expected kept agent to still have cost")
	}
}

func TestCollector_ShouldWriteAndCleanUpEndpointFile_GivenStartShutdown(t *testing.T) {
	// Setup.
	tmp := t.TempDir()
	endpointFile := filepath.Join(tmp, "endpoint")
	t.Setenv("CCMUX_OTEL_ENDPOINT_FILE", endpointFile)

	ctx, cancel := context.WithCancel(context.Background())
	c, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Assert (file written).
	data, err := os.ReadFile(endpointFile)
	if err != nil {
		t.Fatalf("read endpoint file: %v", err)
	}
	if string(data) != c.Endpoint() {
		t.Errorf("expected endpoint file to contain %q, got %q", c.Endpoint(), string(data))
	}
	if !strings.HasPrefix(c.Endpoint(), "http://127.0.0.1:") {
		t.Errorf("expected loopback endpoint, got %q", c.Endpoint())
	}
	if ReadEndpoint() != c.Endpoint() {
		t.Errorf("ReadEndpoint should match running collector's endpoint")
	}

	// Execute (shutdown).
	cancel()

	// Give the cleanup goroutine a moment to run. (The server shutdown
	// itself is bounded by a 2s timeout inside Start; we don't need to
	// wait that long for the file removal that follows.)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(endpointFile); os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("expected endpoint file to be removed after shutdown")
}

func TestCollector_ShouldNotClobberOtherInstancesEndpointFile_GivenForeignContents(t *testing.T) {
	// Setup. Simulate another ccmux process writing its endpoint into the
	// file between our shutdown and our cleanup goroutine winning.
	tmp := t.TempDir()
	endpointFile := filepath.Join(tmp, "endpoint")
	t.Setenv("CCMUX_OTEL_ENDPOINT_FILE", endpointFile)

	ctx, cancel := context.WithCancel(context.Background())
	c, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = c
	if err := os.WriteFile(endpointFile, []byte("http://other-process:9999"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Execute.
	cancel()

	// Assert. The cleanup must NOT delete a file it doesn't own. Poll for a
	// short while to give the cleanup goroutine time to run and (correctly)
	// not delete.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(endpointFile)
	if err != nil {
		t.Fatalf("expected endpoint file to still exist, got err: %v", err)
	}
	if string(data) != "http://other-process:9999" {
		t.Errorf("expected foreign contents preserved, got %q", string(data))
	}
}

func TestCollector_ShouldAcceptLogsAndTraces_GivenOTelSDKFlush(t *testing.T) {
	// Setup. OTel SDK flushes all configured signal types on shutdown. We
	// don't process logs/traces but we must accept them so the agent
	// doesn't log connection errors every time it exits.
	c := withCollector(t)

	// Execute.
	for _, path := range []string{"/v1/logs", "/v1/traces"} {
		resp, err := http.Post(c.Endpoint()+path, "application/json",
			bytes.NewReader([]byte(`{"resourceLogs":[]}`)))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Assert.
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, resp.StatusCode)
		}
		if strings.TrimSpace(string(body)) != "{}" {
			t.Errorf("%s: expected empty-object response, got %q", path, string(body))
		}
	}
}

// jsonNum wraps an int64 in the same string-encoded form OTLP/HTTP+JSON
// uses for the nano timestamps.
func jsonNum(n int64) string {
	return formatInt(n)
}

func formatInt(n int64) string {
	// stdlib only — strconv would be fine too but keeps this self-contained.
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
