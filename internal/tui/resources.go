package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	prevCPUTicks map[int]int64,
	fastWTProjects map[string]bool,
	collector *otelcollector.Collector,
	probe *diskProbe,
	liveWindows map[string]bool,
) (map[string]*AgentResources, map[int]int64, map[string]float64) {
	procs := listAllProcesses()
	procTicks := readAllProcTicks()
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
	type tokenResult struct {
		agentID string
		tokens  tokenBreakdown
	}
	tokenCh := make(chan tokenResult, len(agents))
	type dailyCostResult struct {
		agentID string
		costs   map[string]float64
	}
	dailyCostCh := make(chan dailyCostResult, len(agents))

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
			go func(id, path string) {
				defer wg.Done()
				tokenCh <- tokenResult{id, getAgentSessionTokens(path)}
			}(a.ID, a.WorktreePath)
			wg.Add(1)
			go func(id, path string) {
				defer wg.Done()
				dailyCostCh <- dailyCostResult{id, getAgentSessionDailyCosts(path)}
			}(a.ID, a.WorktreePath)
		}
	}

	go func() {
		wg.Wait()
		close(tokenCh)
		close(dailyCostCh)
	}()

	tokenMap := make(map[string]tokenBreakdown)
	for r := range tokenCh {
		tokenMap[r.agentID] = r.tokens
	}

	// Keep both the per-agent JSONL contribution and the rolled-up total so
	// that, if the OTel collector has accurate cost for an agent, we can
	// swap that agent's slice without re-parsing the JSONL.
	perAgentJSONLDaily := make(map[string]map[string]float64)
	liveDailyCosts := make(map[string]float64)
	for r := range dailyCostCh {
		perAgentJSONLDaily[r.agentID] = r.costs
		for date, cost := range r.costs {
			liveDailyCosts[date] += cost
		}
	}

	newCPUTicks := make(map[int]int64)

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
					if prev, ok := prevCPUTicks[pid]; ok {
						prevTotalTicks += prev
					}
				}

				if len(prevCPUTicks) > 0 && clkTck > 0 {
					res.CPUPercent = computeCPUPercent(prevTotalTicks, currentTicks, 2.0, clkTck, int(numCPU))
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

	return resources, newCPUTicks, liveDailyCosts
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

// cpuActiveThreshold is the amount of CPU time (in CPU-seconds) the persistent
// process tree must consume between two refresh samples (~2s apart) for an
// agent to be considered actively working rather than idle. An agent that is
// genuinely idle at its input prompt only burns a tiny amount of CPU on its
// long-lived processes (event loop, render, MCP keepalives), so the threshold
// is set well above that noise floor but far below the cost of real tool /
// subagent work.
const cpuActiveThreshold = 0.20

func isProcessTreeActive(
	windowID string,
	tmuxMgr *tmux.Manager,
	procs map[int]*procInfo,
	currentTicks map[int]int64,
	prevTicks map[int]int64,
	clkTck int64,
) bool {
	panePID, err := tmuxMgr.GetPanePID(windowID)
	if err != nil || panePID <= 0 {
		return false
	}
	return isProcessTreeActiveFromPID(panePID, procs, currentTicks, prevTicks, clkTck)
}

// isAgentBusy reports whether the agent's pane shows recent activity or its
// process tree has measurable CPU usage. It's the same idle test refreshCmd
// uses to flip StatusRunning to StatusReady, exposed so the CI-pass handler
// and the StatusWaitingReview revert path can gate "PR ready for review" on
// the underlying agent actually being idle.
//
// procs/currentTicks may be nil — refreshCmd has fresh snapshots from the top
// of its loop and passes them through to avoid the extra fork; one-off
// callers (the CI-pass message handler) pass nil and let us sample inline.
//
// Returns false (treats the agent as idle) when there's no tmux window to
// query or the tmux manager isn't wired up — this keeps tests, which
// construct agents without a real pane, on the existing transition path.
func isAgentBusy(
	a *agent.Agent,
	tmuxMgr *tmux.Manager,
	procs map[int]*procInfo,
	currentTicks map[int]int64,
	prevTicks map[int]int64,
	clkTck int64,
	idleThreshold time.Duration,
) bool {
	if a == nil || a.TmuxWindow == "" || tmuxMgr == nil {
		return false
	}
	if activity, err := tmuxMgr.GetWindowActivity(a.TmuxWindow); err == nil {
		if time.Since(activity) <= idleThreshold {
			return true
		}
	}
	if procs == nil {
		procs = listAllProcesses()
	}
	if currentTicks == nil {
		currentTicks = readAllProcTicks()
	}
	return isProcessTreeActive(a.TmuxWindow, tmuxMgr, procs, currentTicks, prevTicks, clkTck)
}

func isProcessTreeActiveFromPID(
	rootPID int,
	procs map[int]*procInfo,
	currentTicks map[int]int64,
	prevTicks map[int]int64,
	clkTck int64,
) bool {
	if len(prevTicks) == 0 || clkTck <= 0 {
		return false
	}
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
	return cpuSeconds > cpuActiveThreshold
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
// exporter (`claude_code.cost.usage` metric, requires `OTEL_METRICS_EXPORTER`
// and a running collector). Codex CLI is the same. Running a per-agent OTLP
// receiver purely to read cost would be heavier than the JSONL re-derivation
// below, so we re-derive cost from the usage blocks Claude already records.
// Pricing in estimateCost() is reconciled against Claude's own internal
// calculation — see TestEstimateCost_ShouldMatchClaudeInternal_*.
type claudeCacheCreation struct {
	Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
}

type claudeUsage struct {
	InputTokens              int64               `json:"input_tokens"`
	OutputTokens             int64               `json:"output_tokens"`
	CacheCreationInputTokens int64               `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64               `json:"cache_read_input_tokens"`
	CacheCreation            claudeCacheCreation `json:"cache_creation"`
}

// cacheCreate5m returns the portion of cache-creation tokens billed at the 5m
// ephemeral rate. When the JSONL carries the explicit split (modern Claude
// Code), prefer that; otherwise fall back to attributing the whole bucket to
// 5m, which was the historical default before 1h caching shipped.
func (u claudeUsage) cacheCreate5m() int64 {
	if u.CacheCreation.Ephemeral5mInputTokens != 0 || u.CacheCreation.Ephemeral1hInputTokens != 0 {
		return u.CacheCreation.Ephemeral5mInputTokens
	}
	return u.CacheCreationInputTokens
}

// cacheCreate1h returns the portion of cache-creation tokens billed at the 1h
// ephemeral rate.
func (u claudeUsage) cacheCreate1h() int64 {
	return u.CacheCreation.Ephemeral1hInputTokens
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
}

// dedupKey returns a stable per-API-call identity for an assistant entry.
// Claude Code logs each content block (thinking/text/tool_use) of one turn as
// a separate JSONL line, all sharing the same message.id and the same usage
// object — without dedup we'd double-count by 2-5x. Prefer the explicit
// message.id; fall back to an input-token signature for older formats that
// did not stamp an id.
func dedupKey(m claudeAPIMessage) string {
	if m.ID != "" {
		return m.ID
	}
	return fmt.Sprintf("sig:%d:%d:%d",
		m.Usage.InputTokens,
		m.Usage.CacheReadInputTokens,
		m.Usage.CacheCreationInputTokens,
	)
}

type tokenBreakdown struct {
	In          int64
	Out         int64
	CacheRead   int64
	CacheCreate int64
	Total       int64
	CostUSD     float64
}

func getAgentSessionTokens(worktreePath string) tokenBreakdown {
	var result tokenBreakdown

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return result
	}

	projectKey := strings.ReplaceAll(worktreePath, "/", "-")
	projectDir := filepath.Join(homeDir, ".claude", "projects", projectKey)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return result
	}

	var jsonlFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			jsonlFiles = append(jsonlFiles, e)
		}
	}
	if len(jsonlFiles) == 0 {
		return result
	}

	sort.Slice(jsonlFiles, func(i, j int) bool {
		infoI, errI := jsonlFiles[i].Info()
		infoJ, errJ := jsonlFiles[j].Info()
		if errI != nil || errJ != nil {
			return false
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	latestFile := filepath.Join(projectDir, jsonlFiles[0].Name())
	f, err := os.Open(latestFile)
	if err != nil {
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var prevKey string
	var prevUsage claudeUsage
	var prevModel string
	var maxOutputTokens int64
	firstGroup := true

	flushGroup := func() {
		if firstGroup {
			return
		}
		folded := prevUsage
		folded.OutputTokens = maxOutputTokens
		result.In += folded.InputTokens
		result.Out += folded.OutputTokens
		result.CacheRead += folded.CacheReadInputTokens
		result.CacheCreate += folded.CacheCreationInputTokens
		result.CostUSD += estimateCost(prevModel, folded)
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		var msg claudeMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Type != "assistant" {
			continue
		}
		if msg.Message.Model == "<synthetic>" {
			continue
		}

		key := dedupKey(msg.Message)
		u := msg.Message.Usage

		if key != prevKey || firstGroup {
			flushGroup()
			prevKey = key
			prevUsage = u
			prevModel = msg.Message.Model
			maxOutputTokens = u.OutputTokens
			firstGroup = false
		} else if u.OutputTokens > maxOutputTokens {
			maxOutputTokens = u.OutputTokens
		}
	}
	flushGroup()

	result.Total = result.In + result.Out + result.CacheRead + result.CacheCreate
	return result
}

// modelRates is the list-price per-1M-token rate for input and output, in USD.
// Cache rates derive from input: read = 0.10x, 5m write = 1.25x, 1h write =
// 2.00x (Anthropic prompt-caching pricing).
type modelRates struct {
	inputPer1M  float64
	outputPer1M float64
}

// modelRatesFor returns the published Anthropic rates for the given model
// string. Rates verified against `claude --print --output-format json`'s own
// `total_cost_usd` field — see TestEstimateCost_ShouldMatchClaudeInternal_*.
func modelRatesFor(model string) modelRates {
	switch {
	// Opus 4.5+ subscription / list rates (input $5, output $25). Verified
	// against Claude's internal cost computation for opus-4-7.
	case isNewOpus(model):
		return modelRates{inputPer1M: 5.0, outputPer1M: 25.0}
	// Legacy Opus 3 / 4 / 4.1 list rates.
	case strings.Contains(model, "opus"):
		return modelRates{inputPer1M: 15.0, outputPer1M: 75.0}
	// Haiku 4.5 list rates (input $1, output $5). Haiku 3.5 was $0.80 / $4
	// but is no longer in active service rotation; the 1/5 default is a
	// safe over-estimate of pennies on the rare retro lookup.
	case strings.Contains(model, "haiku"):
		return modelRates{inputPer1M: 1.0, outputPer1M: 5.0}
	// Sonnet (3.5, 4, 4.5, 4.6, ...) — the default branch. Same $3/$15
	// list rates across the line at the time of writing.
	default:
		return modelRates{inputPer1M: 3.0, outputPer1M: 15.0}
	}
}

func estimateCost(model string, u claudeUsage) float64 {
	r := modelRatesFor(model)

	// Anthropic prompt-caching multipliers (constant across the Claude line).
	const (
		cacheReadMult     = 0.10 // read is always 10% of input rate
		cacheCreate5mMult = 1.25 // 5-minute ephemeral write
		cacheCreate1hMult = 2.00 // 1-hour ephemeral write — DEFAULT for modern Claude Code
	)

	cost := float64(u.InputTokens) * r.inputPer1M / 1_000_000
	cost += float64(u.OutputTokens) * r.outputPer1M / 1_000_000
	cost += float64(u.CacheReadInputTokens) * (r.inputPer1M * cacheReadMult) / 1_000_000
	cost += float64(u.cacheCreate5m()) * (r.inputPer1M * cacheCreate5mMult) / 1_000_000
	cost += float64(u.cacheCreate1h()) * (r.inputPer1M * cacheCreate1hMult) / 1_000_000
	return cost
}

func isNewOpus(model string) bool {
	return strings.Contains(model, "opus-4-5") ||
		strings.Contains(model, "opus-4-6") ||
		strings.Contains(model, "opus-4-7") ||
		strings.Contains(model, "opus-4-8") ||
		strings.Contains(model, "opus-4-9") ||
		strings.Contains(model, "opus-5")
}

func GetAgentDailyCosts(worktreePath string) map[string]float64 {
	return getAgentSessionDailyCosts(worktreePath)
}

func getAgentSessionDailyCosts(worktreePath string) map[string]float64 {
	result := make(map[string]float64)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return result
	}

	projectKey := strings.ReplaceAll(worktreePath, "/", "-")
	projectDir := filepath.Join(homeDir, ".claude", "projects", projectKey)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return result
	}

	var jsonlFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			jsonlFiles = append(jsonlFiles, e)
		}
	}
	if len(jsonlFiles) == 0 {
		return result
	}

	sort.Slice(jsonlFiles, func(i, j int) bool {
		infoI, errI := jsonlFiles[i].Info()
		infoJ, errJ := jsonlFiles[j].Info()
		if errI != nil || errJ != nil {
			return false
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	latestFile := filepath.Join(projectDir, jsonlFiles[0].Name())
	f, err := os.Open(latestFile)
	if err != nil {
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	type groupEntry struct {
		key             string
		usage           claudeUsage
		model           string
		maxOutputTokens int64
		date            string
	}
	var prevGroup *groupEntry

	flushGroup := func() {
		if prevGroup == nil {
			return
		}
		folded := prevGroup.usage
		folded.OutputTokens = prevGroup.maxOutputTokens
		result[prevGroup.date] += estimateCost(prevGroup.model, folded)
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		var msg claudeMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Type != "assistant" {
			continue
		}
		if msg.Message.Model == "<synthetic>" {
			continue
		}

		date := extractDate(msg.Timestamp)
		key := dedupKey(msg.Message)
		u := msg.Message.Usage

		if prevGroup == nil || key != prevGroup.key {
			flushGroup()
			prevGroup = &groupEntry{
				key:             key,
				usage:           u,
				model:           msg.Message.Model,
				maxOutputTokens: u.OutputTokens,
				date:            date,
			}
		} else if u.OutputTokens > prevGroup.maxOutputTokens {
			prevGroup.maxOutputTokens = u.OutputTokens
		}
	}
	flushGroup()

	return result
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
