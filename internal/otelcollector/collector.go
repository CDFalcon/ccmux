// Package otelcollector implements a minimal in-process OpenTelemetry
// receiver. Claude Code emits a built-in `claude_code.cost.usage` counter
// when its OpenTelemetry exporter is configured (CLAUDE_CODE_ENABLE_TELEMETRY
// + OTEL_METRICS_EXPORTER=otlp + OTEL_EXPORTER_OTLP_ENDPOINT). Pointing the
// exporter at this collector gives us Anthropic's own cost figure per turn,
// which is more accurate than re-deriving cost from the JSONL transcript
// (and works for any current/future model without a pricing-table refresh).
//
// The collector is intentionally tiny: it speaks only OTLP/HTTP+JSON, only
// listens on a loopback address, and only persists the per-agent and
// per-day cost totals it cares about. Logs and traces are accepted and
// discarded so the OTel SDK on the client side doesn't error out when it
// tries to flush all three signal types.
//
// Attribution: the launcher script stamps every spawned agent with
// `OTEL_RESOURCE_ATTRIBUTES=ccmux.agent.id=<id>,...`, which the OTel SDK
// folds into each metric's resource block. We key cost by that attribute so
// the collector doesn't have to know about Claude's internal session.id ↔
// ccmux agent.id mapping. Agents that somehow ship without the stamp are
// keyed by session.id with a `session:` prefix and surface in the daily
// total but don't attribute to a specific agent — better than dropping the
// cost entirely.
package otelcollector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EndpointFilePath is where the running collector advertises its endpoint
// (e.g. "http://127.0.0.1:53421") so out-of-process launcher scripts can
// read it without us having to thread the value through every spawn path.
//
// Writers replace the file atomically; readers tolerate it being absent
// (the agent then runs without telemetry, exactly as it did before this
// collector existed).
func EndpointFilePath() string {
	if v := os.Getenv("CCMUX_OTEL_ENDPOINT_FILE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ccmux", "otel-endpoint")
}

// Collector is a single-process OTLP/HTTP+JSON receiver for the
// `claude_code.cost.usage` metric. It serves on a loopback port assigned at
// Start() time and keeps every aggregate in memory; on Shutdown() it
// removes the endpoint advertisement file but does NOT persist totals —
// per-day persistence is the dailycost store's job and happens at agent
// teardown.
type Collector struct {
	server   *http.Server
	listener net.Listener

	mu               sync.Mutex
	perAgentTotal    map[string]float64            // agent.id → cumulative USD
	perAgentDaily    map[string]map[string]float64 // agent.id → "YYYY-MM-DD" → USD
	perAgentLastSeen map[string]time.Time          // for staleness sweeps later

	endpointFile string
}

// Start binds a loopback listener and serves OTLP/HTTP+JSON on it. The
// context cancels the server; the listener is also closed if context
// cancellation arrives after Start returns. Errors propagate from the
// listener bind only — once the server is running, ingest failures are
// swallowed (they're recoverable on the next export) and only surface in
// stderr.
func Start(ctx context.Context) (*Collector, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("otelcollector: bind loopback: %w", err)
	}

	c := &Collector{
		listener:         ln,
		perAgentTotal:    map[string]float64{},
		perAgentDaily:    map[string]map[string]float64{},
		perAgentLastSeen: map[string]time.Time{},
		endpointFile:     EndpointFilePath(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/metrics", c.handleMetrics)
	mux.HandleFunc("/v1/logs", c.handleDrain)
	mux.HandleFunc("/v1/traces", c.handleDrain)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	c.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	if err := c.writeEndpoint(); err != nil {
		_ = ln.Close()
		return nil, err
	}

	go func() {
		if err := c.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "[otelcollector] serve stopped: %v\n", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.server.Shutdown(shutdownCtx)
		c.removeEndpoint()
	}()

	return c, nil
}

// Endpoint returns the base URL the OTel SDK should target — i.e. the value
// to set in OTEL_EXPORTER_OTLP_ENDPOINT. The SDK appends /v1/metrics etc.
// per the OTLP/HTTP spec, so we do NOT include any path here.
func (c *Collector) Endpoint() string {
	return "http://" + c.listener.Addr().String()
}

// Cost returns the cumulative USD spend the collector has observed for the
// given ccmux agent ID. The second return is false when we've never seen
// any cost.usage data point for that agent — callers use that to fall back
// to the JSONL-derived estimate.
func (c *Collector) Cost(agentID string) (float64, bool) {
	if agentID == "" {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.perAgentTotal[agentID]
	if !ok || v <= 0 {
		return 0, false
	}
	return v, true
}

// DailyCosts returns the sum of observed cost grouped by UTC date across all
// agents. Used by the TUI's "Today's cost" badge so the running total
// reflects Claude's own number rather than our estimate.
func (c *Collector) DailyCosts() map[string]float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]float64)
	for _, days := range c.perAgentDaily {
		for d, v := range days {
			out[d] += v
		}
	}
	return out
}

// DailyCostsForAgent returns the per-day breakdown for one agent. Used at
// agent cleanup so the dailycost store gets the collector's accurate
// number per day, not just the final session total.
func (c *Collector) DailyCostsForAgent(agentID string) map[string]float64 {
	if agentID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	src := c.perAgentDaily[agentID]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]float64, len(src))
	for d, v := range src {
		out[d] = v
	}
	return out
}

// ForgetAgent drops every aggregate for one agent. Called at agent
// teardown after the per-day totals have been persisted to the dailycost
// store so a re-spawned agent with the same id (rare, but possible across
// cleanup/respawn paths) doesn't double-count.
func (c *Collector) ForgetAgent(agentID string) {
	if agentID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.perAgentTotal, agentID)
	delete(c.perAgentDaily, agentID)
	delete(c.perAgentLastSeen, agentID)
}

// --- OTLP envelope types -------------------------------------------------
//
// Minimal subset of the OTLP/HTTP+JSON shape — we only decode the metric
// path we care about. Everything else parses into an empty struct or is
// ignored. Adding fields here is cheap as long as they stay JSON-tagged.

type otlpEnvelope struct {
	ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
}

type otlpResourceMetrics struct {
	Resource     otlpResource       `json:"resource"`
	ScopeMetrics []otlpScopeMetrics `json:"scopeMetrics"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScopeMetrics struct {
	Metrics []otlpMetric `json:"metrics"`
}

type otlpMetric struct {
	Name string  `json:"name"`
	Sum  otlpSum `json:"sum"`
	// Gauge / Histogram / ExponentialHistogram intentionally omitted — the
	// cost metric is a delta-temporality Sum, and that's the only metric
	// this collector consumes.
}

type otlpSum struct {
	DataPoints []otlpNumberDataPoint `json:"dataPoints"`
	// IsMonotonic / AggregationTemporality intentionally omitted — we sum
	// every data point regardless. Cumulative-vs-delta would matter if we
	// were doing diff arithmetic, but Claude Code emits delta for cost and
	// stops re-sending past windows, so naive addition is correct.
}

type otlpNumberDataPoint struct {
	Attributes    []otlpKeyValue `json:"attributes"`
	TimeUnixNano  json.Number    `json:"timeUnixNano"`
	StartTimeNano json.Number    `json:"startTimeUnixNano"`
	AsDouble      *float64       `json:"asDouble"`
	AsInt         *json.Number   `json:"asInt"`
}

type otlpKeyValue struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue *string  `json:"stringValue"`
	IntValue    *string  `json:"intValue"` // OTLP spec: int64 is JSON-encoded as a string
	DoubleValue *float64 `json:"doubleValue"`
	BoolValue   *bool    `json:"boolValue"`
}

func (v otlpAnyValue) asString() string {
	if v.StringValue != nil {
		return *v.StringValue
	}
	if v.IntValue != nil {
		return *v.IntValue
	}
	return ""
}

func attrsToMap(kvs []otlpKeyValue) map[string]string {
	if len(kvs) == 0 {
		return nil
	}
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		out[kv.Key] = kv.Value.asString()
	}
	return out
}

func numberValue(dp otlpNumberDataPoint) float64 {
	if dp.AsDouble != nil {
		return *dp.AsDouble
	}
	if dp.AsInt != nil {
		n, err := dp.AsInt.Float64()
		if err == nil {
			return n
		}
	}
	return 0
}

func nanoToDate(n json.Number) string {
	if n == "" {
		return ""
	}
	ns, err := n.Int64()
	if err != nil || ns <= 0 {
		return ""
	}
	return time.Unix(0, ns).UTC().Format("2006-01-02")
}

// --- HTTP handlers -------------------------------------------------------

func (c *Collector) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err == nil {
		c.ingest(body)
	}
	// OTLP spec: return 200 with an empty ExportMetricsServiceResponse on
	// success. We always return success — the SDK retries on 4xx/5xx, and
	// dropping a metric batch is preferable to making the agent stall on
	// transport errors.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

func (c *Collector) handleDrain(w http.ResponseWriter, r *http.Request) {
	// Logs and traces aren't useful to ccmux today but the OTel SDK will
	// still try to flush them on shutdown. Accept and discard so the agent
	// doesn't log connection errors on every exit.
	if r.Method == http.MethodPost {
		_, _ = io.Copy(io.Discard, r.Body)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

func (c *Collector) ingest(body []byte) {
	var env otlpEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return
	}
	now := time.Now()
	for _, rm := range env.ResourceMetrics {
		resAttrs := attrsToMap(rm.Resource.Attributes)
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name != "claude_code.cost.usage" {
					continue
				}
				for _, dp := range m.Sum.DataPoints {
					dpAttrs := attrsToMap(dp.Attributes)
					agentID := agentIDFromAttrs(dpAttrs, resAttrs)
					if agentID == "" {
						continue
					}
					val := numberValue(dp)
					if val <= 0 {
						continue
					}
					date := nanoToDate(dp.TimeUnixNano)
					c.add(agentID, val, date, now)
				}
			}
		}
	}
}

// agentIDFromAttrs picks the most specific attribution available. Prefer
// the data-point attribute (per-export overrides), then the resource
// attribute (process-wide), then fall back to a session-scoped pseudo-id
// so cost from agents that somehow ship without the stamp still rolls up
// into the daily total.
func agentIDFromAttrs(dp, res map[string]string) string {
	if v := dp["ccmux.agent.id"]; v != "" {
		return v
	}
	if v := res["ccmux.agent.id"]; v != "" {
		return v
	}
	if v := dp["session.id"]; v != "" {
		return "session:" + v
	}
	if v := res["session.id"]; v != "" {
		return "session:" + v
	}
	return ""
}

func (c *Collector) add(agentID string, usd float64, date string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.perAgentTotal[agentID] += usd
	if date == "" {
		date = now.UTC().Format("2006-01-02")
	}
	if c.perAgentDaily[agentID] == nil {
		c.perAgentDaily[agentID] = map[string]float64{}
	}
	c.perAgentDaily[agentID][date] += usd
	c.perAgentLastSeen[agentID] = now
}

// --- endpoint file -------------------------------------------------------

func (c *Collector) writeEndpoint() error {
	if err := os.MkdirAll(filepath.Dir(c.endpointFile), 0o755); err != nil {
		return fmt.Errorf("otelcollector: mkdir for endpoint file: %w", err)
	}
	tmp := c.endpointFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(c.Endpoint()), 0o644); err != nil {
		return fmt.Errorf("otelcollector: write endpoint file: %w", err)
	}
	if err := os.Rename(tmp, c.endpointFile); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("otelcollector: rename endpoint file: %w", err)
	}
	return nil
}

func (c *Collector) removeEndpoint() {
	// Only delete if we still own it — another ccmux instance may have
	// started between our shutdown and the cleanup goroutine winning the
	// race. Comparing the file contents to our own endpoint is a cheap
	// way to avoid clobbering theirs.
	data, err := os.ReadFile(c.endpointFile)
	if err != nil {
		return
	}
	if string(data) != c.Endpoint() {
		return
	}
	_ = os.Remove(c.endpointFile)
}

// ReadEndpoint returns the endpoint advertised by a running collector, or
// "" if no advertisement file exists. Used by out-of-process spawn paths
// (e.g. the user-facing `ccmux task` command) that don't run their own
// collector but want to point spawned agents at the TUI's.
func ReadEndpoint() string {
	data, err := os.ReadFile(EndpointFilePath())
	if err != nil {
		return ""
	}
	return string(data)
}
