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
	CPUPercent float64
	MemBytes   int64
	MemPercent float64
	DiskBytes   int64
	TotalTokens int64
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
) (map[string]*AgentResources, map[int]int64) {
	procs := listAllProcesses()
	procTicks := readAllProcTicks()
	resources := make(map[string]*AgentResources)
	numCPU := float64(runtime.NumCPU())

	type diskResult struct {
		agentID string
		bytes   int64
	}
	var wg sync.WaitGroup
	diskCh := make(chan diskResult, len(agents))
	type tokenResult struct {
		agentID string
		tokens  int64
	}
	tokenCh := make(chan tokenResult, len(agents))

	for _, a := range agents {
		if a.WorktreePath != "" {
			wg.Add(1)
			go func(id, path string) {
				defer wg.Done()
				diskCh <- diskResult{id, getDiskUsage(path)}
			}(a.ID, a.WorktreePath)
			wg.Add(1)
			go func(id, path string) {
				defer wg.Done()
				tokenCh <- tokenResult{id, getAgentSessionTokens(path)}
			}(a.ID, a.WorktreePath)
		}
	}

	go func() {
		wg.Wait()
		close(diskCh)
		close(tokenCh)
	}()

	diskMap := make(map[string]int64)
	for r := range diskCh {
		diskMap[r.agentID] = r.bytes
	}

	tokenMap := make(map[string]int64)
	for r := range tokenCh {
		tokenMap[r.agentID] = r.tokens
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
		res.TotalTokens = tokenMap[a.ID]
		resources[a.ID] = res
	}

	return resources, newCPUTicks
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
	Usage claudeUsage `json:"usage"`
}

type claudeMessage struct {
	Type    string           `json:"type"`
	Message claudeAPIMessage `json:"message"`
}

func getAgentSessionTokens(worktreePath string) int64 {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return 0
	}

	projectKey := strings.ReplaceAll(worktreePath, "/", "-")
	if strings.HasPrefix(projectKey, "-") {
		projectKey = projectKey[1:]
	}
	projectDir := filepath.Join(homeDir, ".claude", "projects", projectKey)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return 0
	}

	var jsonlFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			jsonlFiles = append(jsonlFiles, e)
		}
	}
	if len(jsonlFiles) == 0 {
		return 0
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
		return 0
	}
	defer f.Close()

	var total int64
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg claudeMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Type != "assistant" {
			continue
		}
		total += msg.Message.Usage.InputTokens
		total += msg.Message.Usage.OutputTokens
		total += msg.Message.Usage.CacheCreationInputTokens
		total += msg.Message.Usage.CacheReadInputTokens
	}

	return total
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

func formatResourceLine(r *AgentResources) string {
	if r == nil {
		return ""
	}
	line := fmt.Sprintf("CPU: %.0f%%  Mem: %s (%.0f%%)  Disk: %s",
		r.CPUPercent,
		formatBytes(r.MemBytes),
		r.MemPercent,
		formatBytes(r.DiskBytes),
	)
	if r.TotalTokens > 0 {
		line += fmt.Sprintf("  Tokens: %s", formatTokens(r.TotalTokens))
	}
	return line
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
