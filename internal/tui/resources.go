package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/harness"
	"github.com/CDFalcon/ccmux/internal/otelcollector"
	"github.com/CDFalcon/ccmux/internal/tmux"
)

// Platform-specific implementations of the following functions live in
// resources_linux.go and resources_darwin.go:
//   - listAllProcesses() map[int]*procInfo
//   - readAllProcTicks() map[int]int64
//   - getTotalMemoryKB() int64
//   - getSystemMemPercent() float64
//   - getDiskUsage(path string) int64

type AgentResources struct {
	CPUPercent  float64
	MemBytes    int64
	MemPercent  float64
	DiskBytes   int64
	DiskReflinked bool
	TotalTokens int64
	TokensIn         int64
	TokensOut        int64
	TokensCacheRead  int64
	TokensCacheCreate int64
	CostUSD     float64
}

type procInfo struct {
	pid  int
	ppid int
	rss  int64
}

// isTerminal reports whether an agent has reached a state it cannot leave.
// Teardown has run or is running, so nothing about the agent will change again,
// and doCleanup has already folded its cost into the persistent daily-cost
// store — re-deriving it from the session JSONL would double-count.
func isTerminal(s agent.Status) bool {
	switch s {
	case agent.StatusMerged, agent.StatusFailed, agent.StatusCleaningUp, agent.StatusKilling:
		return true
	}
	return false
}

// hasLiveCostData reports whether an agent's session JSONL should still be
// parsed for tokens and cost.
//
// This is a weaker gate than isPollable on purpose. The JSONL lives under
// ~/.claude/projects/<mangled worktree path>/, not inside the worktree, so it
// remains readable and meaningful after the worktree directory is gone — and it
// is in-process file I/O, not a subprocess, so it was never part of the
// process-explosion. Only genuinely terminal agents are excluded, because their
// cost has already been rolled into the daily-cost store at teardown.
func hasLiveCostData(a *agent.Agent) bool {
	return a != nil && a.WorktreePath != "" && !isTerminal(a.Status)
}

// isPollable reports whether an agent should still be charged the per-refresh
// *subprocess* work — the disk measurement and the tmux/process-tree queries.
// It is the liveness half of the overload fix: diskProbe bounds how much work a
// single worktree can cause, and this bounds *which* worktrees cause any at
// all.
//
// Without it, cost scaled with the number of agents ever registered rather
// than the number running — 25 worktree snapshots on disk, only 8 belonging to
// a live agent, and every one of the other 17 still being measured every 2s
// because the registry entry (and therefore WorktreePath) outlived the agent.
//
// liveWindows is the set of tmux window IDs that currently exist. A nil map
// means "could not enumerate" — we then fail open and poll, because blanking
// the whole display on a transient tmux hiccup is worse than a wasted probe.
func isPollable(a *agent.Agent, liveWindows map[string]bool) bool {
	if a == nil || a.WorktreePath == "" {
		return false
	}

	// StatusMerged matters most among the terminal states: it is where an entry
	// gets parked when the post-merge `ccmux cleanup` fails to finish, and such
	// an entry is never revisited by any other poller (the CI poll list
	// excludes it). That is precisely the stale entry whose directory
	// accumulated 552 concurrent `git diff` processes.
	if isTerminal(a.Status) {
		return false
	}

	// A registry entry whose tmux window no longer exists is a leftover: the
	// agent is not running, so its disk and token figures are frozen.
	if liveWindows != nil && a.TmuxWindow != "" && !liveWindows[a.TmuxWindow] {
		return false
	}

	// A worktree that is gone from disk cannot be measured. Cheap to check
	// (no fork) and it stops us from paying for a failing subprocess every
	// backoff period.
	if info, err := os.Stat(a.WorktreePath); err != nil || !info.IsDir() {
		return false
	}

	return true
}

func queryAllAgentResources(
	agents []*agent.Agent,
	tmuxMgr *tmux.Manager,
	totalMemKB int64,
	clkTck int64,
	prevCPU cpuSample,
	fastWTProjects map[string]bool,
	collector *otelcollector.Collector,
	probe *diskProbe,
	liveWindows map[string]bool,
) (map[string]*AgentResources, cpuSample, map[string]float64) {
	procs := listAllProcesses()
	current := sampleCPUTicks()
	procTicks := current.ticks
	// How much wall time the CPU deltas below actually span. This used to be
	// hardcoded to 2.0 (the tick interval), which quietly overstated or
	// understated every agent's CPU% whenever a refresh took longer than the
	// tick — which it routinely does, since a refresh forks tmux per agent and
	// parses multi-megabyte transcripts.
	cpuWindow := current.elapsedSince(prevCPU).Seconds()
	resources := make(map[string]*AgentResources)
	numCPU := float64(runtime.NumCPU())

	// Decide once who is worth polling, so the disk, token and process-tree
	// passes below all agree and a stale entry costs nothing anywhere.
	pollable := make(map[string]bool, len(agents))
	keepPaths := make(map[string]bool, len(agents))
	for _, a := range agents {
		if isPollable(a, liveWindows) {
			pollable[a.ID] = true
			keepPaths[a.WorktreePath] = true
		}
	}
	// Forget probe state for worktrees we no longer poll, so a long session
	// does not accumulate one entry per agent ever run.
	probe.Retain(keepPaths)

	var wg sync.WaitGroup
	type usageResult struct {
		agentID string
		usage   sessionUsage
	}
	usageCh := make(chan usageResult, len(agents))

	// Disk is read straight from the probe rather than in a goroutine, because
	// Sample never blocks on a subprocess: it returns the last known figure and
	// starts at most one background measurement per worktree. That is what
	// keeps a slow repo from delaying this refresh and stacking up with the
	// next tick's.
	diskMap := make(map[string]int64)
	diskReflinked := make(map[string]bool)

	for _, a := range agents {
		if pollable[a.ID] {
			isFastWT := fastWTProjects[a.ProjectName]
			diskMap[a.ID], _ = probe.Sample(a.WorktreePath, isFastWT)
			diskReflinked[a.ID] = isFastWT
		}

		if hasLiveCostData(a) {
			wg.Add(1)
			go func(a *agent.Agent) {
				defer wg.Done()
				usageCh <- usageResult{a.ID, agentSessionUsage(a)}
			}(a)
		}
	}

	go func() {
		wg.Wait()
		close(usageCh)
	}()

	// Keep both the per-agent transcript contribution and the rolled-up
	// total so that, if the OTel collector has accurate cost for an agent,
	// we can swap that agent's slice without re-parsing the transcript.
	tokenMap := make(map[string]tokenBreakdown)
	perAgentJSONLDaily := make(map[string]map[string]float64)
	liveDailyCosts := make(map[string]float64)
	for r := range usageCh {
		tokenMap[r.agentID] = r.usage.tokens
		perAgentJSONLDaily[r.agentID] = r.usage.daily
		for date, cost := range r.usage.daily {
			liveDailyCosts[date] += cost
		}
	}

	newCPU := cpuSample{ticks: make(map[int]int64), at: current.at}
	newCPUTicks := newCPU.ticks

	for _, a := range agents {
		res := &AgentResources{}

		// Weaker gate than isPollable on purpose: a spawning agent has a
		// window but no worktree yet, and we still want its CPU/memory. All we
		// require is that the window actually exists, so we don't fork a
		// doomed `tmux display-message` per stale entry per refresh.
		windowLive := a.TmuxWindow != "" && (liveWindows == nil || liveWindows[a.TmuxWindow])
		if windowLive {
			panePID, err := tmuxMgr.GetPanePID(a.TmuxWindow)
			if err == nil && panePID > 0 {
				descendants := findDescendants(panePID, procs)
				var totalRSS int64
				var currentTicks int64
				for _, pid := range descendants {
					if p, ok := procs[pid]; ok {
						totalRSS += p.rss
					}
					if ticks, ok := procTicks[pid]; ok {
						currentTicks += ticks
						newCPUTicks[pid] = ticks
					}
				}

				var prevTotalTicks int64
				for _, pid := range descendants {
					if prev, ok := prevCPU.ticks[pid]; ok {
						prevTotalTicks += prev
					}
				}

				if len(prevCPU.ticks) > 0 && clkTck > 0 && cpuWindow > 0 {
					res.CPUPercent = computeCPUPercent(prevTotalTicks, currentTicks, cpuWindow, clkTck, int(numCPU))
				}

				res.MemBytes = totalRSS * 1024
				if totalMemKB > 0 {
					res.MemPercent = float64(totalRSS) / float64(totalMemKB) * 100
				}
			}
		}

		res.DiskBytes = diskMap[a.ID]
		res.DiskReflinked = diskReflinked[a.ID]
		tb := tokenMap[a.ID]
		res.TotalTokens = tb.Total
		res.TokensIn = tb.In
		res.TokensOut = tb.Out
		res.TokensCacheRead = tb.CacheRead
		res.TokensCacheCreate = tb.CacheCreate
		// Cost source preference, per-agent: the OpenTelemetry collector
		// gets first refusal because that figure is Claude's own
		// total_cost_usd, not our derivation. We fall back to the JSONL
		// estimate when the collector has never seen this agent — which
		// happens for pre-upgrade agents, Codex agents, and any agent
		// that didn't pick up the OTel env (e.g. user has their own
		// exporter already configured, so we don't clobber).
		if collector != nil {
			if c, ok := collector.Cost(a.ID); ok {
				res.CostUSD = c
			} else {
				res.CostUSD = tb.CostUSD
			}
		} else {
			res.CostUSD = tb.CostUSD
		}
		resources[a.ID] = res
	}

	// Same source preference, applied to the daily rollup: every per-agent
	// day the collector has seen overrides whatever the JSONL fallback
	// computed for that agent, so the displayed "Today's cost" matches
	// Claude's own number whenever telemetry is on. Agents the collector
	// has never seen keep their JSONL-derived contribution.
	if collector != nil {
		for _, a := range agents {
			perDay := collector.DailyCostsForAgent(a.ID)
			if len(perDay) == 0 {
				continue
			}
			// Subtract this agent's JSONL contribution from the rollup
			// (captured above, no second JSONL read) so the per-day
			// total isn't double-counted when we add the collector's.
			for date, cost := range perAgentJSONLDaily[a.ID] {
				liveDailyCosts[date] -= cost
				if liveDailyCosts[date] < 0 {
					liveDailyCosts[date] = 0
				}
			}
			for date, cost := range perDay {
				liveDailyCosts[date] += cost
			}
		}
	}

	return resources, newCPU, liveDailyCosts
}

func findDescendants(rootPID int, procs map[int]*procInfo) []int {
	children := make(map[int][]int)
	for pid, p := range procs {
		children[p.ppid] = append(children[p.ppid], pid)
	}

	var result []int
	queue := []int{rootPID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)
		queue = append(queue, children[current]...)
	}
	return result
}

// cpuActiveCoreFraction is how much of one CPU core the persistent process
// tree must average, over the interval between two samples, for an agent to
// count as actively working rather than idle. An agent sitting at its input
// prompt still burns a little CPU on long-lived processes (event loop, render,
// MCP keepalives), so the threshold sits well above that noise floor but far
// below the cost of real tool / subagent work.
//
// This is deliberately a RATE, not a CPU-second budget. It used to be the
// latter (0.20 CPU-seconds, "between two refresh samples ~2s apart"), which
// silently assumed every comparison spanned the same 2 seconds. Nothing
// enforced that: the snapshot these samples are compared against is refreshed
// once per refresh cycle, and a refresh takes as long as it takes (tmux forks,
// `gh` calls, transcript parsing, disk probes), while the CI poller consults
// it from an asynchronous message handler. Whenever the real interval stretched
// past ~4s, an idle long-context agent's ordinary background noise cleared
// 0.20 CPU-seconds and the agent read as permanently busy — which is what kept
// green PRs pinned in "waiting on CI" instead of flipping to ready for review.
// 0.10 of a core over 2s is exactly the old 0.20 CPU-seconds, now independent
// of how far apart the samples happen to be.
const cpuActiveCoreFraction = 0.10

// minCPUSampleWindow is the shortest interval over which a CPU rate means
// anything. `ps` reports cumulative CPU time in centiseconds, so over a very
// short window a single 10ms tick of quantization reads as a large fraction of
// a core. Below this, report "not active" rather than guess.
const minCPUSampleWindow = 500 * time.Millisecond

// cpuSample is a snapshot of per-PID cumulative CPU ticks together with the
// instant it was taken. The timestamp is the whole point: CPU usage is a rate,
// and the difference between two tick snapshots says nothing until you know
// how far apart they are.
type cpuSample struct {
	ticks map[int]int64
	at    time.Time
}

// sampleCPUTicks reads every process's cumulative CPU time and stamps it.
func sampleCPUTicks() cpuSample {
	return cpuSample{ticks: readAllProcTicks(), at: time.Now()}
}

// elapsedSince returns how long this sample is after prev, or 0 when either
// end is missing (an unstamped or empty snapshot can't bound an interval).
func (c cpuSample) elapsedSince(prev cpuSample) time.Duration {
	if c.at.IsZero() || prev.at.IsZero() {
		return 0
	}
	return c.at.Sub(prev.at)
}

func isProcessTreeActive(
	windowID string,
	tmuxMgr *tmux.Manager,
	procs map[int]*procInfo,
	current cpuSample,
	prev cpuSample,
	clkTck int64,
) bool {
	panePID, err := tmuxMgr.GetPanePID(windowID)
	if err != nil || panePID <= 0 {
		return false
	}
	return isProcessTreeActiveFromPID(panePID, procs, current, prev, clkTck)
}

// paneQuietOverride is how long an agent's pane must go without producing a
// single byte before we stop believing the CPU heuristic and call the agent
// idle outright.
//
// The CPU test is a proxy for "is this agent doing work". Output is the direct
// evidence: a harness that is mid-turn prints something — a token, a spinner
// frame, a tool line — far more often than once every two minutes. So when the
// pane has been silent this long, the agent is not mid-turn no matter what its
// background threads are burning, and holding its finished PR hostage to a
// heuristic is strictly worse than believing the silence. Without this, any
// systematic over-read of the CPU signal strands the agent forever, because
// nothing else ever re-examines it.
const paneQuietOverride = 2 * time.Minute

// isAgentBusy reports whether the agent's pane shows recent activity or its
// process tree has measurable CPU usage. It's the same idle test refreshCmd
// uses to flip StatusRunning to StatusReady, exposed so the CI handlers can
// gate "PR ready for review", CI-fix resumes and review resumes on the
// underlying agent actually being idle.
//
// procs/current may be zero — refreshCmd has fresh snapshots from the top of
// its loop and passes them through to avoid the extra fork; one-off callers
// (the CI message handlers) pass the zero value and let us sample inline.
//
// Returns false (treats the agent as idle) when there's no tmux window to
// query or the tmux manager isn't wired up — this keeps tests, which
// construct agents without a real pane, on the existing transition path.
func isAgentBusy(
	a *agent.Agent,
	tmuxMgr *tmux.Manager,
	procs map[int]*procInfo,
	current cpuSample,
	prev cpuSample,
	clkTck int64,
	idleThreshold time.Duration,
) bool {
	if a == nil || a.TmuxWindow == "" || tmuxMgr == nil {
		return false
	}
	if activity, err := tmuxMgr.GetWindowActivity(a.TmuxWindow); err == nil {
		quietFor := time.Since(activity)
		if quietFor <= idleThreshold {
			return true
		}
		if quietFor >= paneQuietOverride {
			return false
		}
	}
	if procs == nil {
		procs = listAllProcesses()
	}
	if current.ticks == nil {
		current = sampleCPUTicks()
	}
	return isProcessTreeActive(a.TmuxWindow, tmuxMgr, procs, current, prev, clkTck)
}

func isProcessTreeActiveFromPID(
	rootPID int,
	procs map[int]*procInfo,
	current cpuSample,
	prev cpuSample,
	clkTck int64,
) bool {
	if len(prev.ticks) == 0 || clkTck <= 0 {
		return false
	}
	// An unmeasurable interval yields an unmeasurable rate. Report "not
	// active" rather than divide by a number we don't trust: the cost of a
	// false idle is one extra poll, the cost of a false busy is an agent
	// stuck out of the state machine.
	window := current.elapsedSince(prev)
	if window < minCPUSampleWindow {
		return false
	}
	currentTicks, prevTicks := current.ticks, prev.ticks
	descendants := findDescendants(rootPID, procs)

	// Only count CPU deltas for processes present in BOTH samples.
	//
	// Claude Code spawns many short-lived child processes (hooks, the ccmux
	// forwarder, statusline scripts, quick bash tool calls) even while the
	// user is idle at the prompt. A process that appears in currentTicks but
	// not prevTicks (newly spawned) — or vice versa (just exited) — would
	// otherwise contribute its entire CPU lifetime as a phantom "delta",
	// consistently inflating the measurement above the threshold and masking
	// genuinely idle agents. Restricting to the common PID set measures only
	// sustained work by the agent's persistent process tree.
	var deltaTicks int64
	for _, pid := range descendants {
		curr, hasCurr := currentTicks[pid]
		prev, hasPrev := prevTicks[pid]
		if !hasCurr || !hasPrev {
			continue
		}
		// Guard against PID reuse producing a negative per-process delta.
		if d := curr - prev; d > 0 {
			deltaTicks += d
		}
	}

	if deltaTicks <= 0 {
		return false
	}
	cpuSeconds := float64(deltaTicks) / float64(clkTck)
	return cpuSeconds/window.Seconds() > cpuActiveCoreFraction
}

// Note: the unbounded getDiskUsageIncremental / getDiskUsage pair that used to
// live here is now measureIncrementalDiskUsage / measureDuDiskUsage in
// diskprobe.go, reachable only through diskProbe.Sample so that every
// invocation carries a timeout, process-group reaping, single-flight and
// backoff. Nothing should shell out per worktree from this file again.

func getClockTicks() int64 {
	cmd := exec.Command("getconf", "CLK_TCK")
	output, err := cmd.Output()
	if err != nil {
		return 100
	}
	val, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 100
	}
	return val
}

func computeCPUPercent(prevTicks int64, currTicks int64, deltaSeconds float64, clkTck int64, numCPU int) float64 {
	if clkTck <= 0 || deltaSeconds <= 0 || numCPU <= 0 {
		return 0
	}
	deltaTicks := currTicks - prevTicks
	cpuPct := (float64(deltaTicks) / (deltaSeconds * float64(clkTck) * float64(numCPU))) * 100.0
	if cpuPct < 0 {
		return 0
	}
	if cpuPct > 100 {
		return 100
	}
	return cpuPct
}

// Note on cost source: Claude Code does NOT write per-turn cost to the session
// JSONL — the `total_cost_usd` and `modelUsage[*].costUSD` fields are emitted
// only by `claude --print --output-format json` and by the OpenTelemetry
// exporter (`claude_code.cost.usage`, which internal/otelcollector receives
// and queryAllAgentResources prefers). The code below re-derives cost from the
// usage blocks Claude already records, for agents without telemetry. The
// pricing model and the rules for reading the transcript are documented in
// pricing.go.
type claudeCacheCreation struct {
	Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
}

// claudeIteration is one leg of a request that the API served through a
// server-side fallback chain: the model that produced this leg and the
// tokens it consumed. `type` is "message" for the original model and
// "fallback_message" for a substitute.
type claudeIteration struct {
	Type                     string              `json:"type"`
	Model                    string              `json:"model"`
	InputTokens              int64               `json:"input_tokens"`
	OutputTokens             int64               `json:"output_tokens"`
	CacheCreationInputTokens int64               `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64               `json:"cache_read_input_tokens"`
	CacheCreation            claudeCacheCreation `json:"cache_creation"`
}

type claudeUsage struct {
	InputTokens              int64               `json:"input_tokens"`
	OutputTokens             int64               `json:"output_tokens"`
	CacheCreationInputTokens int64               `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64               `json:"cache_read_input_tokens"`
	CacheCreation            claudeCacheCreation `json:"cache_creation"`
	// Speed is "fast" when the request ran in fast mode (premium rate).
	Speed string `json:"speed"`
	// InferenceGeo is "us" when US-only inference (1.1×) was requested.
	InferenceGeo string `json:"inference_geo"`
	// Iterations lists each model leg when a fallback re-served the request.
	Iterations []claudeIteration `json:"iterations"`
}

// splitCacheCreate splits cache-creation tokens into the 5m and 1h buckets.
// When the JSONL carries the explicit split (modern Claude Code), prefer
// that; otherwise attribute the whole bucket to 5m, which was the default
// before 1h caching shipped.
func splitCacheCreate(total int64, cc claudeCacheCreation) (c5m, c1h int64) {
	if cc.Ephemeral5mInputTokens != 0 || cc.Ephemeral1hInputTokens != 0 {
		return cc.Ephemeral5mInputTokens, cc.Ephemeral1hInputTokens
	}
	return total, 0
}

// cacheCreate5m returns the portion of cache-creation tokens billed at the
// 5m ephemeral rate.
func (u claudeUsage) cacheCreate5m() int64 {
	c5m, _ := splitCacheCreate(u.CacheCreationInputTokens, u.CacheCreation)
	return c5m
}

// cacheCreate1h returns the portion of cache-creation tokens billed at the
// 1h ephemeral rate.
func (u claudeUsage) cacheCreate1h() int64 {
	_, c1h := splitCacheCreate(u.CacheCreationInputTokens, u.CacheCreation)
	return c1h
}

// counts returns the billable split of the top-level usage block.
func (u claudeUsage) counts() claudeTokenCounts {
	c5m, c1h := splitCacheCreate(u.CacheCreationInputTokens, u.CacheCreation)
	return claudeTokenCounts{
		input:         u.InputTokens,
		output:        u.OutputTokens,
		cacheRead:     u.CacheReadInputTokens,
		cacheCreate5m: c5m,
		cacheCreate1h: c1h,
	}
}

type pricedIteration struct {
	model  string
	counts claudeTokenCounts
}

// pricedIterations returns one entry per fallback leg when the request was
// re-served by another model, else nil (a lone iteration restates the
// top-level block and often has a null model, so it adds nothing). The
// top-level usage of a fallback response only reflects the final leg —
// tokens the refusing model consumed would otherwise go unpriced.
func (u claudeUsage) pricedIterations(defaultModel string) []pricedIteration {
	if len(u.Iterations) < 2 {
		return nil
	}
	out := make([]pricedIteration, 0, len(u.Iterations))
	for _, it := range u.Iterations {
		model := it.Model
		if model == "" {
			model = defaultModel
		}
		c5m, c1h := splitCacheCreate(it.CacheCreationInputTokens, it.CacheCreation)
		out = append(out, pricedIteration{model: model, counts: claudeTokenCounts{
			input:         it.InputTokens,
			output:        it.OutputTokens,
			cacheRead:     it.CacheReadInputTokens,
			cacheCreate5m: c5m,
			cacheCreate1h: c1h,
		}})
	}
	return out
}

type claudeAPIMessage struct {
	ID         string      `json:"id"`
	Model      string      `json:"model"`
	Usage      claudeUsage `json:"usage"`
	StopReason *string     `json:"stop_reason"`
}

type claudeMessage struct {
	Type      string           `json:"type"`
	Message   claudeAPIMessage `json:"message"`
	Timestamp string           `json:"timestamp"`
	RequestID string           `json:"requestId"`
}

// dedupKey returns a stable per-API-call identity for an assistant entry.
// Claude Code logs each content block (thinking/text/tool_use) of one turn as
// a separate JSONL line, all sharing the same message.id and the same usage
// object — without dedup we'd double-count by 2-5x. Prefer the explicit
// message.id (qualified by requestId when present, matching what ccusage
// keys on); fall back to an input-token signature for older formats that
// did not stamp an id.
func dedupKey(m claudeMessage) string {
	if m.Message.ID != "" {
		if m.RequestID != "" {
			return m.Message.ID + ":" + m.RequestID
		}
		return m.Message.ID
	}
	return fmt.Sprintf("sig:%d:%d:%d",
		m.Message.Usage.InputTokens,
		m.Message.Usage.CacheReadInputTokens,
		m.Message.Usage.CacheCreationInputTokens,
	)
}

// usageRecord is one priced API call, in a shape shared by both harnesses.
type usageRecord struct {
	model       string
	date        string // "YYYY-MM-DD" from the transcript timestamp
	in          int64
	out         int64
	cacheRead   int64
	cacheCreate int64
	costUSD     float64
}

type tokenBreakdown struct {
	In          int64
	Out         int64
	CacheRead   int64
	CacheCreate int64
	Total       int64
	CostUSD     float64
}

// sessionUsage is everything the TUI wants from an agent's transcripts:
// the lifetime token/cost totals and the same cost bucketed by UTC day.
type sessionUsage struct {
	tokens tokenBreakdown
	daily  map[string]float64
}

func summarizeUsage(recs []usageRecord) sessionUsage {
	u := sessionUsage{daily: make(map[string]float64)}
	for _, r := range recs {
		u.tokens.In += r.in
		u.tokens.Out += r.out
		u.tokens.CacheRead += r.cacheRead
		u.tokens.CacheCreate += r.cacheCreate
		u.tokens.CostUSD += r.costUSD
		u.daily[r.date] += r.costUSD
	}
	u.tokens.Total = u.tokens.In + u.tokens.Out + u.tokens.CacheRead + u.tokens.CacheCreate
	return u
}

// agentSessionUsage reads the agent's transcripts with the parser for its
// harness. Claude Code transcripts live under ~/.claude/projects keyed by
// worktree path; Codex rollouts are matched to the worktree by their header.
func agentSessionUsage(a *agent.Agent) sessionUsage {
	switch harness.Parse(a.Harness) {
	case harness.Codex:
		return summarizeUsage(scanCodexSessions(a.WorktreePath, a.CreatedAt))
	default:
		return summarizeUsage(scanClaudeSession(a.WorktreePath))
	}
}

// getAgentSessionTokens is the Claude-transcript path of agentSessionUsage,
// kept as a narrow entry point for tests.
func getAgentSessionTokens(worktreePath string) tokenBreakdown {
	return summarizeUsage(scanClaudeSession(worktreePath)).tokens
}

// getAgentSessionDailyCosts is the Claude-transcript path of
// agentSessionUsage, kept as a narrow entry point for tests.
func getAgentSessionDailyCosts(worktreePath string) map[string]float64 {
	return summarizeUsage(scanClaudeSession(worktreePath)).daily
}

// GetAgentDailyCosts returns the agent's transcript-derived cost by day, for
// folding into the persistent daily-cost store at teardown.
func GetAgentDailyCosts(a *agent.Agent) map[string]float64 {
	return agentSessionUsage(a).daily
}

// claudeProjectDir returns the directory Claude Code keeps transcripts in
// for a worktree.
func claudeProjectDir(worktreePath string) (string, bool) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	projectKey := strings.ReplaceAll(worktreePath, "/", "-")
	return filepath.Join(homeDir, ".claude", "projects", projectKey), true
}

// claudeTranscriptFiles lists every transcript under the project dir, oldest
// first: the top-level session files plus each session's
// `<session-id>/subagents/agent-*.jsonl`, where the Agent tool's sub-agent
// turns are logged. Those sub-agent calls are billed like any other and were
// previously invisible to the estimate. Reading only the newest session file
// was also wrong for the same reason.
func claudeTranscriptFiles(projectDir string) []string {
	type entry struct {
		path string
		mod  time.Time
	}
	var files []entry
	filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Claude Code keeps auto-memory notes here, never transcripts.
			if path != projectDir && d.Name() == "memory" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		files = append(files, entry{path, info.ModTime()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out
}

// scanClaudeSession turns every transcript for a worktree into one priced
// record per API call. Content-block lines of one message are folded by
// message id (globally, so a `--resume`d session that re-logs history does
// not double-count); the line carrying the largest output_tokens is the
// final one and is the one priced. Lines without a message id (old
// transcripts) are only folded when consecutive, since their signature
// cannot distinguish two genuinely identical turns.
func scanClaudeSession(worktreePath string) []usageRecord {
	projectDir, ok := claudeProjectDir(worktreePath)
	if !ok {
		return nil
	}

	type call struct {
		msg claudeMessage
	}
	var calls []call
	byID := make(map[string]int)
	prevKey := ""

	for _, path := range claudeTranscriptFiles(projectDir) {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		prevKey = ""
		for scanner.Scan() {
			var msg claudeMessage
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			if msg.Type != "assistant" || msg.Message.Model == "<synthetic>" {
				continue
			}
			key := dedupKey(msg)
			idx := -1
			if key == prevKey && len(calls) > 0 {
				idx = len(calls) - 1
			} else if i, seen := byID[key]; seen && msg.Message.ID != "" {
				idx = i
			}
			if idx >= 0 {
				if msg.Message.Usage.OutputTokens > calls[idx].msg.Message.Usage.OutputTokens {
					calls[idx].msg.Message.Usage = msg.Message.Usage
				}
			} else {
				calls = append(calls, call{msg})
				if msg.Message.ID != "" {
					byID[key] = len(calls) - 1
				}
			}
			prevKey = key
		}
		f.Close()
	}

	recs := make([]usageRecord, 0, len(calls))
	for _, c := range calls {
		u := c.msg.Message.Usage
		recs = append(recs, usageRecord{
			model:       c.msg.Message.Model,
			date:        extractDate(c.msg.Timestamp),
			in:          u.InputTokens,
			out:         u.OutputTokens,
			cacheRead:   u.CacheReadInputTokens,
			cacheCreate: u.CacheCreationInputTokens,
			costUSD:     estimateCost(c.msg.Message.Model, u),
		})
	}
	return recs
}

func extractDate(timestamp string) string {
	if len(timestamp) >= 10 {
		return timestamp[:10]
	}
	return "unknown"
}

func formatTokens(tokens int64) string {
	if tokens <= 0 {
		return ""
	}
	if tokens >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	}
	if tokens >= 1_000 {
		return fmt.Sprintf("%.0fk", float64(tokens)/1_000)
	}
	return fmt.Sprintf("%d", tokens)
}

func formatCost(cost float64) string {
	if cost <= 0 {
		return ""
	}
	return fmt.Sprintf("$%.2f", cost)
}

func formatTokenDetail(r *AgentResources) string {
	if r == nil || (r.TokensIn == 0 && r.TokensOut == 0 && r.TokensCacheRead == 0) {
		return ""
	}
	return fmt.Sprintf("In: %s  Out: %s  Cache: %s",
		formatTokens(r.TokensIn),
		formatTokens(r.TokensOut),
		formatTokens(r.TokensCacheRead))
}

func formatResourceLine(r *AgentResources) string {
	if r == nil {
		return ""
	}
	diskStr := formatBytes(r.DiskBytes)
	if r.DiskReflinked {
		diskStr = "~" + diskStr
	}
	return fmt.Sprintf("CPU: %.0f%%  Mem: %s (%.0f%%)  Disk: %s",
		r.CPUPercent,
		formatBytes(r.MemBytes),
		r.MemPercent,
		diskStr,
	)
}

func formatBytes(bytes int64) string {
	const (
		gb = 1024 * 1024 * 1024
		mb = 1024 * 1024
	)
	if bytes >= gb {
		return fmt.Sprintf("%.1fGb", float64(bytes)/float64(gb))
	}
	if bytes >= mb {
		return fmt.Sprintf("%.0fMb", float64(bytes)/float64(mb))
	}
	if bytes > 0 {
		return fmt.Sprintf("%.0fKb", float64(bytes)/1024)
	}
	return "0Mb"
}
