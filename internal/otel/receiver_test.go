package otel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

func setupReceiver(t *testing.T) (*Receiver, int) {
	t.Helper()
	r := NewReceiver()
	port, err := r.Start()
	if err != nil {
		t.Fatalf("failed to start receiver: %v", err)
	}
	t.Cleanup(func() {
		r.Stop(context.Background())
	})
	return r, port
}

func postMetrics(t *testing.T, port int, payload *otlpExportRequest) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/v1/metrics", port), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func strPtr(s string) *string    { return &s }
func floatPtr(f float64) *float64 { return &f }
func flexIntPtr(i int64) *flexInt64 { return &flexInt64{Value: i, Set: true} }

func buildPayload(agentID string, metrics []metric) *otlpExportRequest {
	return &otlpExportRequest{
		ResourceMetrics: []resourceMetrics{
			{
				Resource: resource{
					Attributes: []attribute{
						{Key: "ccmux.agent_id", Value: attributeValue{StringValue: strPtr(agentID)}},
					},
				},
				ScopeMetrics: []scopeMetrics{
					{Metrics: metrics},
				},
			},
		},
	}
}

func TestReceiver_ShouldParseCostMetric_GivenValidPayload(t *testing.T) {
	// Setup.
	r, port := setupReceiver(t)

	payload := buildPayload("agent-1", []metric{
		{
			Name: "claude_code.cost.usage",
			Sum:  &sumData{DataPoints: []dataPoint{{AsDouble: floatPtr(0.25)}}},
		},
	})

	// Execute.
	postMetrics(t, port, payload)

	// Assert.
	m := r.GetMetrics("agent-1")
	if m == nil {
		t.Fatal("expected metrics for agent-1")
	}
	if m.CostUSD != 0.25 {
		t.Errorf("expected cost 0.25, got %f", m.CostUSD)
	}
}

func TestReceiver_ShouldAccumulateDeltaCost_GivenMultipleExports(t *testing.T) {
	// Setup.
	r, port := setupReceiver(t)

	payload := buildPayload("agent-1", []metric{
		{
			Name: "claude_code.cost.usage",
			Sum:  &sumData{DataPoints: []dataPoint{{AsDouble: floatPtr(0.10)}}},
		},
	})

	// Execute.
	postMetrics(t, port, payload)
	postMetrics(t, port, payload)

	// Assert.
	m := r.GetMetrics("agent-1")
	if m == nil {
		t.Fatal("expected metrics for agent-1")
	}
	if m.CostUSD < 0.19 || m.CostUSD > 0.21 {
		t.Errorf("expected cost ~0.20, got %f", m.CostUSD)
	}
}

func TestReceiver_ShouldParseTokenMetrics_GivenTypedDataPoints(t *testing.T) {
	// Setup.
	r, port := setupReceiver(t)

	payload := buildPayload("agent-2", []metric{
		{
			Name: "claude_code.token.usage",
			Sum: &sumData{DataPoints: []dataPoint{
				{AsInt: flexIntPtr(5000), Attributes: []attribute{{Key: "type", Value: attributeValue{StringValue: strPtr("input")}}}},
				{AsInt: flexIntPtr(1200), Attributes: []attribute{{Key: "type", Value: attributeValue{StringValue: strPtr("output")}}}},
				{AsInt: flexIntPtr(8000), Attributes: []attribute{{Key: "type", Value: attributeValue{StringValue: strPtr("cacheRead")}}}},
			}},
		},
	})

	// Execute.
	postMetrics(t, port, payload)

	// Assert.
	m := r.GetMetrics("agent-2")
	if m == nil {
		t.Fatal("expected metrics for agent-2")
	}
	if m.TokensIn != 5000 {
		t.Errorf("expected TokensIn 5000, got %d", m.TokensIn)
	}
	if m.TokensOut != 1200 {
		t.Errorf("expected TokensOut 1200, got %d", m.TokensOut)
	}
	if m.TokensCacheRead != 8000 {
		t.Errorf("expected TokensCacheRead 8000, got %d", m.TokensCacheRead)
	}
}

func TestReceiver_ShouldIgnorePayload_GivenMissingAgentID(t *testing.T) {
	// Setup.
	r, port := setupReceiver(t)

	payload := &otlpExportRequest{
		ResourceMetrics: []resourceMetrics{
			{
				Resource: resource{Attributes: []attribute{}},
				ScopeMetrics: []scopeMetrics{
					{Metrics: []metric{
						{Name: "claude_code.cost.usage", Sum: &sumData{DataPoints: []dataPoint{{AsDouble: floatPtr(1.0)}}}},
					}},
				},
			},
		},
	}

	// Execute.
	postMetrics(t, port, payload)

	// Assert.
	all := r.GetAllMetrics()
	if len(all) != 0 {
		t.Errorf("expected no metrics, got %d agents", len(all))
	}
}

func TestReceiver_ShouldTrackMultipleAgents_GivenDifferentIDs(t *testing.T) {
	// Setup.
	r, port := setupReceiver(t)

	p1 := buildPayload("a1", []metric{{Name: "claude_code.cost.usage", Sum: &sumData{DataPoints: []dataPoint{{AsDouble: floatPtr(0.10)}}}}})
	p2 := buildPayload("a2", []metric{{Name: "claude_code.cost.usage", Sum: &sumData{DataPoints: []dataPoint{{AsDouble: floatPtr(0.20)}}}}})

	// Execute.
	postMetrics(t, port, p1)
	postMetrics(t, port, p2)

	// Assert.
	all := r.GetAllMetrics()
	if len(all) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(all))
	}
	if all["a1"].CostUSD != 0.10 {
		t.Errorf("expected a1 cost 0.10, got %f", all["a1"].CostUSD)
	}
	if all["a2"].CostUSD != 0.20 {
		t.Errorf("expected a2 cost 0.20, got %f", all["a2"].CostUSD)
	}
}

func TestReceiver_ShouldRemoveAgent_GivenRemoveCall(t *testing.T) {
	// Setup.
	r, port := setupReceiver(t)

	payload := buildPayload("agent-x", []metric{{Name: "claude_code.cost.usage", Sum: &sumData{DataPoints: []dataPoint{{AsDouble: floatPtr(0.50)}}}}})
	postMetrics(t, port, payload)

	// Execute.
	r.RemoveAgent("agent-x")

	// Assert.
	m := r.GetMetrics("agent-x")
	if m != nil {
		t.Error("expected nil after removal")
	}
}

func TestReceiver_ShouldHandleConcurrentAccess_GivenParallelPosts(t *testing.T) {
	// Setup.
	r, port := setupReceiver(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agentID := fmt.Sprintf("agent-%d", id%5)
			payload := buildPayload(agentID, []metric{
				{Name: "claude_code.cost.usage", Sum: &sumData{DataPoints: []dataPoint{{AsDouble: floatPtr(0.01)}}}},
			})
			postMetrics(t, port, payload)
		}(i)
	}

	// Execute.
	wg.Wait()

	// Assert.
	all := r.GetAllMetrics()
	if len(all) != 5 {
		t.Errorf("expected 5 agents, got %d", len(all))
	}
	for id, m := range all {
		if m.CostUSD < 0.01 {
			t.Errorf("agent %s: expected cost >= 0.01, got %f", id, m.CostUSD)
		}
	}
}

func TestReceiver_ShouldParseTokens_GivenStringEncodedInt64(t *testing.T) {
	// Setup.
	r, port := setupReceiver(t)

	rawJSON := `{
		"resourceMetrics": [{
			"resource": {
				"attributes": [{"key": "ccmux.agent_id", "value": {"stringValue": "agent-str"}}]
			},
			"scopeMetrics": [{
				"metrics": [{
					"name": "claude_code.token.usage",
					"sum": {
						"dataPoints": [
							{"asInt": "7500", "attributes": [{"key": "type", "value": {"stringValue": "input"}}]},
							{"asInt": "2000", "attributes": [{"key": "type", "value": {"stringValue": "output"}}]}
						]
					}
				}]
			}]
		}]
	}`

	// Execute.
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/v1/metrics", port),
		"application/json",
		bytes.NewBufferString(rawJSON),
	)
	if err != nil {
		t.Fatalf("failed to post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Assert.
	m := r.GetMetrics("agent-str")
	if m == nil {
		t.Fatal("expected metrics for agent-str")
	}
	if m.TokensIn != 7500 {
		t.Errorf("expected TokensIn 7500, got %d", m.TokensIn)
	}
	if m.TokensOut != 2000 {
		t.Errorf("expected TokensOut 2000, got %d", m.TokensOut)
	}
}

func TestReceiver_ShouldParseActiveTime_GivenCumulativeValue(t *testing.T) {
	// Setup.
	r, port := setupReceiver(t)

	payload := buildPayload("agent-t", []metric{
		{
			Name: "claude_code.active_time.total",
			Sum:  &sumData{DataPoints: []dataPoint{{AsDouble: floatPtr(120.5)}}},
		},
	})

	// Execute.
	postMetrics(t, port, payload)

	// Assert.
	m := r.GetMetrics("agent-t")
	if m == nil {
		t.Fatal("expected metrics")
	}
	if m.ActiveTimeSec != 120.5 {
		t.Errorf("expected 120.5, got %f", m.ActiveTimeSec)
	}
}
