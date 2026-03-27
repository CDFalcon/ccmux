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

	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/tmux"
)

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

func queryAllAgentResources(
	agents []*agent.Agent,
	tmuxMgr *tmux.Manager,
	totalMemKB int64,
	clkTck int64,
	prevCPUTicks map[int]int64,
	fastWTProjects map[string]bool,
) (map[string]*AgentResources, map[int]int64, map[string]float64) {
	procs := listAllProcesses()
	procTicks := readAllProcTicks()
	resources := make(map[string]*AgentResources)
	numCPU := float64(runtime.NumCPU())

	type diskResult struct {
		agentID   string
		bytes     int64
		reflinked bool
	}
	var wg sync.WaitGroup
	diskCh := make(chan diskResult, len(agents))
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

	for _, a := range agents {
		if a.WorktreePath != "" {
			wg.Add(1)
			isFastWT := fastWTProjects[a.ProjectName]
			go func(id, path string, fastWT bool) {
				defer wg.Done()
				if fastWT {
					diskCh <- diskResult{id, getDiskUsageIncremental(path), true}
				} else {
					diskCh <- diskResult{id, getDiskUsage(path), false}
				}
			}(a.ID, a.WorktreePath, isFastWT)
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
		close(diskCh)
		close(tokenCh)
		close(dailyCostCh)
	}()

	diskMap := make(map[string]int64)
	diskReflinked := make(map[string]bool)
	for r := range diskCh {
		diskMap[r.agentID] = r.bytes
		diskReflinked[r.agentID] = r.reflinked
	}

	tokenMap := make(map[string]tokenBreakdown)
	for r := range tokenCh {
		tokenMap[r.agentID] = r.tokens
	}

	liveDailyCosts := make(map[string]float64)
	for r := range dailyCostCh {
		for date, cost := range r.costs {
			liveDailyCosts[date] += cost
		}
	}

	newCPUTicks := make(map[int]int64)

	for _, a := range agents {
		res := &AgentResources{}

		if a.TmuxWindow != "" {
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
		res.CostUSD = tb.CostUSD
		resources[a.ID] = res
	}

	return resources, newCPUTicks, liveDailyCosts
}

func listAllProcesses() map[int]*procInfo {
	cmd := exec.Command("ps", "-e", "--no-headers", "-o", "pid:1,ppid:1,rss:1")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	procs := make(map[int]*procInfo)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		ppid, _ := strconv.Atoi(fields[1])
		rss, _ := strconv.ParseInt(fields[2], 10, 64)
		procs[pid] = &procInfo{pid: pid, ppid: ppid, rss: rss}
	}
	return procs
}

func readAllProcTicks() map[int]int64 {
	ticks := make(map[int]int64)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return ticks
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		t := readProcTicks(pid)
		if t > 0 {
			ticks[pid] = t
		}
	}
	return ticks
}

func readProcTicks(pid int) int64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	closeIdx := strings.LastIndex(string(data), ")")
	if closeIdx < 0 || closeIdx+2 >= len(data) {
		return 0
	}
	fields := strings.Fields(string(data)[closeIdx+2:])
	if len(fields) < 13 {
		return 0
	}
	utime, _ := strconv.ParseInt(fields[11], 10, 64)
	stime, _ := strconv.ParseInt(fields[12], 10, 64)
	return utime + stime
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

const cpuActiveThreshold = 0.05

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

	var curr, prev int64
	for _, pid := range descendants {
		if t, ok := currentTicks[pid]; ok {
			curr += t
		}
		if t, ok := prevTicks[pid]; ok {
			prev += t
		}
	}

	deltaTicks := curr - prev
	if deltaTicks <= 0 {
		return false
	}
	cpuSeconds := float64(deltaTicks) / float64(clkTck)
	return cpuSeconds > cpuActiveThreshold
}

func getDiskUsage(path string) int64 {
	cmd := exec.Command("du", "-sb", path)
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) < 1 {
		return 0
	}
	size, _ := strconv.ParseInt(fields[0], 10, 64)
	return size
}

func getDiskUsageIncremental(path string) int64 {
	cmd := exec.Command("git", "-C", path, "diff", "--name-only", "HEAD")
	modifiedOut, err := cmd.Output()
	if err != nil {
		return getDiskUsage(path)
	}

	cmd2 := exec.Command("git", "-C", path, "ls-files", "--others", "--exclude-standard")
	untrackedOut, err := cmd2.Output()
	if err != nil {
		return getDiskUsage(path)
	}

	var totalBytes int64
	seen := make(map[string]bool)
	for _, output := range [][]byte{modifiedOut, untrackedOut} {
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			fullPath := filepath.Join(path, line)
			info, err := os.Stat(fullPath)
			if err == nil {
				totalBytes += info.Size()
			}
		}
	}
	return totalBytes
}

func getTotalMemoryKB() int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseInt(fields[1], 10, 64)
				return kb
			}
		}
	}
	return 0
}

func getSystemMemPercent() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var totalKB, availKB int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				totalKB, _ = strconv.ParseInt(fields[1], 10, 64)
			}
		} else if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				availKB, _ = strconv.ParseInt(fields[1], 10, 64)
			}
		}
		if totalKB > 0 && availKB > 0 {
			break
		}
	}
	if totalKB == 0 {
		return 0
	}
	return float64(totalKB-availKB) / float64(totalKB) * 100
}

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

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type claudeAPIMessage struct {
	Model      string      `json:"model"`
	Usage      claudeUsage `json:"usage"`
	StopReason *string     `json:"stop_reason"`
}

type claudeMessage struct {
	Type      string           `json:"type"`
	Message   claudeAPIMessage `json:"message"`
	Timestamp string           `json:"timestamp"`
}

type inputSignature struct {
	InputTokens              int64
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
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

	var prevSig inputSignature
	var prevModel string
	var maxOutputTokens int64
	firstGroup := true

	flushGroup := func() {
		if firstGroup {
			return
		}
		result.In += prevSig.InputTokens
		result.Out += maxOutputTokens
		result.CacheRead += prevSig.CacheReadInputTokens
		result.CacheCreate += prevSig.CacheCreationInputTokens
		result.CostUSD += estimateCost(prevModel, claudeUsage{
			InputTokens:              prevSig.InputTokens,
			OutputTokens:             maxOutputTokens,
			CacheCreationInputTokens: prevSig.CacheCreationInputTokens,
			CacheReadInputTokens:     prevSig.CacheReadInputTokens,
		})
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

		u := msg.Message.Usage
		sig := inputSignature{
			InputTokens:              u.InputTokens,
			CacheReadInputTokens:     u.CacheReadInputTokens,
			CacheCreationInputTokens: u.CacheCreationInputTokens,
		}

		if sig != prevSig || firstGroup {
			flushGroup()
			prevSig = sig
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

func estimateCost(model string, u claudeUsage) float64 {
	var inputPer1M, outputPer1M float64

	switch {
	case isNewOpus(model):
		inputPer1M = 5.0
		outputPer1M = 25.0
	case strings.Contains(model, "opus"):
		inputPer1M = 15.0
		outputPer1M = 75.0
	case strings.Contains(model, "haiku"):
		inputPer1M = 1.0
		outputPer1M = 5.0
	default:
		inputPer1M = 3.0
		outputPer1M = 15.0
	}

	cacheReadPer1M := inputPer1M * 0.10
	cacheCreatePer1M := inputPer1M * 1.25

	cost := float64(u.InputTokens) * inputPer1M / 1_000_000
	cost += float64(u.OutputTokens) * outputPer1M / 1_000_000
	cost += float64(u.CacheReadInputTokens) * cacheReadPer1M / 1_000_000
	cost += float64(u.CacheCreationInputTokens) * cacheCreatePer1M / 1_000_000
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
		sig             inputSignature
		model           string
		maxOutputTokens int64
		date            string
	}
	var prevGroup *groupEntry

	flushGroup := func() {
		if prevGroup == nil {
			return
		}
		cost := estimateCost(prevGroup.model, claudeUsage{
			InputTokens:              prevGroup.sig.InputTokens,
			OutputTokens:             prevGroup.maxOutputTokens,
			CacheCreationInputTokens: prevGroup.sig.CacheCreationInputTokens,
			CacheReadInputTokens:     prevGroup.sig.CacheReadInputTokens,
		})
		result[prevGroup.date] += cost
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

		u := msg.Message.Usage
		sig := inputSignature{
			InputTokens:              u.InputTokens,
			CacheReadInputTokens:     u.CacheReadInputTokens,
			CacheCreationInputTokens: u.CacheCreationInputTokens,
		}

		if prevGroup == nil || sig != prevGroup.sig {
			flushGroup()
			prevGroup = &groupEntry{
				sig:             sig,
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
