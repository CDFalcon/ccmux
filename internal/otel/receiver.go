package otel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CDFalcon/ccmux/internal/logging"
)

type otlpExportRequest struct {
	ResourceMetrics []resourceMetrics `json:"resourceMetrics"`
}

type resourceMetrics struct {
	Resource     resource      `json:"resource"`
	ScopeMetrics []scopeMetrics `json:"scopeMetrics"`
}

type resource struct {
	Attributes []attribute `json:"attributes"`
}

type scopeMetrics struct {
	Metrics []metric `json:"metrics"`
}

type metric struct {
	Name string    `json:"name"`
	Sum  *sumData  `json:"sum"`
}

type sumData struct {
	DataPoints []dataPoint `json:"dataPoints"`
}

type flexInt64 struct {
	Value int64
	Set   bool
}

func (f flexInt64) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(f.Value, 10)), nil
}

func (f *flexInt64) UnmarshalJSON(data []byte) error {
	s := string(data)
	if s == "null" {
		return nil
	}
	f.Set = true
	s = strings.Trim(s, "\"")
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("flexInt64: cannot parse %q: %w", string(data), err)
	}
	f.Value = v
	return nil
}

type dataPoint struct {
	AsDouble   *float64    `json:"asDouble"`
	AsInt      *flexInt64  `json:"asInt"`
	Attributes []attribute `json:"attributes"`
}

type attribute struct {
	Key   string         `json:"key"`
	Value attributeValue `json:"value"`
}

type attributeValue struct {
	StringValue *string  `json:"stringValue"`
	IntValue    *int64   `json:"intValue"`
	DoubleValue *float64 `json:"doubleValue"`
}

type Receiver struct {
	mu      sync.RWMutex
	metrics map[string]*AgentMetrics
	server  *http.Server
	ln      net.Listener
}

func NewReceiver() *Receiver {
	return &Receiver{
		metrics: make(map[string]*AgentMetrics),
	}
}

func (r *Receiver) Start() (int, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/metrics", r.handleMetrics)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to listen: %w", err)
	}
	r.ln = ln

	r.server = &http.Server{Handler: mux}
	go r.server.Serve(ln)

	port := ln.Addr().(*net.TCPAddr).Port
	logging.Log("otel: receiver started on port %d", port)
	return port, nil
}

func (r *Receiver) Stop(ctx context.Context) error {
	if r.server == nil {
		return nil
	}
	return r.server.Shutdown(ctx)
}

func (r *Receiver) GetMetrics(agentID string) *AgentMetrics {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if m, ok := r.metrics[agentID]; ok {
		cp := *m
		return &cp
	}
	return nil
}

func (r *Receiver) GetAllMetrics() map[string]*AgentMetrics {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]*AgentMetrics, len(r.metrics))
	for k, v := range r.metrics {
		cp := *v
		result[k] = &cp
	}
	return result
}

func (r *Receiver) RemoveAgent(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.metrics, agentID)
}

func (r *Receiver) handleMetrics(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		logging.Log("otel: failed to read request body: %v", err)
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer req.Body.Close()

	var export otlpExportRequest
	if err := json.Unmarshal(body, &export); err != nil {
		logging.Log("otel: failed to parse JSON (content-type: %s, body length: %d): %v", req.Header.Get("Content-Type"), len(body), err)
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	r.processExport(&export)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{}`))
}

func (r *Receiver) processExport(export *otlpExportRequest) {
	for _, rm := range export.ResourceMetrics {
		agentID := extractAgentID(rm.Resource.Attributes)
		if agentID == "" {
			var attrKeys []string
			for _, a := range rm.Resource.Attributes {
				attrKeys = append(attrKeys, a.Key)
			}
			logging.Log("otel: skipping metrics export with no ccmux.agent_id (resource attrs: %v)", attrKeys)
			continue
		}

		r.mu.Lock()
		am, ok := r.metrics[agentID]
		if !ok {
			am = &AgentMetrics{}
			r.metrics[agentID] = am
			logging.Log("otel: first metrics received for agent %s", agentID)
		}

		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				r.applyMetric(am, &m)
			}
		}
		am.LastUpdated = time.Now()
		r.mu.Unlock()
	}
}

func (r *Receiver) applyMetric(am *AgentMetrics, m *metric) {
	if m.Sum == nil {
		return
	}

	switch m.Name {
	case "claude_code.cost.usage":
		for _, dp := range m.Sum.DataPoints {
			if v := dp.AsDouble; v != nil {
				am.CostUSD += *v
			}
		}
	case "claude_code.token.usage":
		for _, dp := range m.Sum.DataPoints {
			tokenType := getAttributeString(dp.Attributes, "type")
			val := getDataPointInt(dp)
			switch tokenType {
			case "input":
				am.TokensIn += val
			case "output":
				am.TokensOut += val
			case "cacheRead":
				am.TokensCacheRead += val
			case "cacheCreation":
				am.TokensCacheCreate += val
			}
		}
	case "claude_code.active_time.total":
		for _, dp := range m.Sum.DataPoints {
			if v := dp.AsDouble; v != nil {
				am.ActiveTimeSec = *v
			}
		}
	}
}

func extractAgentID(attrs []attribute) string {
	for _, a := range attrs {
		if a.Key == "ccmux.agent_id" && a.Value.StringValue != nil {
			return *a.Value.StringValue
		}
	}
	return ""
}

func getAttributeString(attrs []attribute, key string) string {
	for _, a := range attrs {
		if a.Key == key && a.Value.StringValue != nil {
			return *a.Value.StringValue
		}
	}
	return ""
}

func getDataPointInt(dp dataPoint) int64 {
	if dp.AsInt != nil && dp.AsInt.Set {
		return dp.AsInt.Value
	}
	if dp.AsDouble != nil {
		return int64(*dp.AsDouble)
	}
	return 0
}
