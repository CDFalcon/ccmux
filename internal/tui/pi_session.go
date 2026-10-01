package tui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Pi usage. Pi writes one JSONL file per session under
// $PI_CODING_AGENT_DIR/sessions/--<encoded cwd>--/ (default ~/.pi/agent),
// where the encoding strips the leading slash and turns / \ : into dashes.
// The directory is the agent's worktree, so every file in it belongs to the
// agent: its sessions across restarts, reloads and any /new it ran.
// When PI_CODING_AGENT_SESSION_DIR points all sessions at one directory, the
// files are matched by the cwd in their header line instead.
//
// Usage lives in three places, all of which Pi counts toward a session's
// cost: assistant messages and tool results (message.usage), standalone
// `usage` entries (e.g. prompt-cache warming) and compaction summaries.
// Each carries the token counts and Pi's own cost breakdown.

func piAgentDir() string {
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		if strings.HasPrefix(d, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				return filepath.Join(home, d[2:])
			}
		}
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

var piCwdUnsafe = regexp.MustCompile(`[/\\:]`)

// piEncodeCwd mirrors Pi's getDefaultSessionDirPath.
func piEncodeCwd(cwd string) string {
	trimmed := cwd
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		trimmed = trimmed[1:] // exactly one leading separator, as Pi does
	}
	return "--" + piCwdUnsafe.ReplaceAllString(trimmed, "-") + "--"
}

// piSessionFiles lists the session files for a worktree.
func piSessionFiles(worktreePath string) []string {
	if worktreePath == "" {
		return nil
	}
	if shared := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); shared != "" {
		matches, _ := filepath.Glob(filepath.Join(shared, "*.jsonl"))
		var out []string
		for _, m := range matches {
			if readPiSessionCwd(m) == worktreePath {
				out = append(out, m)
			}
		}
		return out
	}
	agentDir := piAgentDir()
	if agentDir == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(agentDir, "sessions", piEncodeCwd(worktreePath), "*.jsonl"))
	return matches
}

func readPiSessionCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	if !scanner.Scan() {
		return ""
	}
	var header struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
	}
	if json.Unmarshal(scanner.Bytes(), &header) != nil || header.Type != "session" {
		return ""
	}
	return header.Cwd
}

type piUsage struct {
	Input      int64 `json:"input"` // excludes cache reads and writes
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Cost       struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

type piEntry struct {
	Type      string   `json:"type"`
	ID        string   `json:"id"`
	Timestamp string   `json:"timestamp"`
	Provider  string   `json:"provider"` // usage entries
	Model     string   `json:"model"`    // usage entries
	Usage     *piUsage `json:"usage"`    // usage and compaction entries
	Message   *struct {
		Role       string   `json:"role"`
		Provider   string   `json:"provider"`
		Model      string   `json:"model"`
		ResponseID string   `json:"responseId"`
		Timestamp  int64    `json:"timestamp"`
		Usage      *piUsage `json:"usage"`
	} `json:"message"`
}

// piRecord prices one usage block. Pi's own cost is used when it has one;
// a zero on an OpenAI provider (a ChatGPT-subscription login through
// "openai-codex") is priced at OpenAI list rates, like Codex agents.
func piRecord(provider, model, timestamp string, u *piUsage) (usageRecord, bool) {
	if u == nil || (u.Input == 0 && u.Output == 0 && u.CacheRead == 0 && u.CacheWrite == 0) {
		return usageRecord{}, false
	}
	cost := u.Cost.Total
	if cost <= 0 && strings.HasPrefix(provider, "openai") {
		cost = priceCodexCall(model, u.Input+u.CacheRead, u.CacheRead, u.Output)
	}
	return usageRecord{
		model:       model,
		date:        extractDate(timestamp),
		in:          u.Input,
		out:         u.Output,
		cacheRead:   u.CacheRead,
		cacheCreate: u.CacheWrite,
		costUSD:     cost,
	}, true
}

// scanPiSessions returns every priced request Pi recorded for the worktree.
// Entries are deduplicated across files, because /fork and /clone copy the
// source session's history — usage included — into the new file.
func scanPiSessions(worktreePath string) []usageRecord {
	var recs []usageRecord
	seen := make(map[string]bool)
	for _, path := range piSessionFiles(worktreePath) {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			// Cheap pre-filter: most lines (tool output, user text) carry
			// no usage at all.
			if !strings.Contains(string(line), `"usage"`) {
				continue
			}
			var e piEntry
			if json.Unmarshal(line, &e) != nil {
				continue
			}
			key := e.ID + "|" + e.Timestamp
			if e.Message != nil && e.Message.ResponseID != "" {
				key = "resp|" + e.Message.ResponseID
			}
			if e.ID == "" || seen[key] {
				continue
			}
			var (
				rec usageRecord
				ok  bool
			)
			switch {
			case e.Type == "message" && e.Message != nil:
				rec, ok = piRecord(e.Message.Provider, e.Message.Model, e.Timestamp, e.Message.Usage)
			case e.Type == "usage" || e.Type == "compaction":
				rec, ok = piRecord(e.Provider, e.Model, e.Timestamp, e.Usage)
			}
			if ok {
				seen[key] = true
				recs = append(recs, rec)
			}
		}
		f.Close()
	}
	return recs
}
