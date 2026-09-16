package tui

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Codex CLI transcript ("rollout") parsing. Codex writes one
// $CODEX_HOME/sessions/YYYY/MM/DD/rollout-<timestamp>-<id>.jsonl per thread.
// Nothing in the file name ties a rollout to a ccmux agent; the link is the
// `session_meta` line at the top of the file, whose payload.cwd is the
// directory Codex was started in — the agent's worktree. A single agent can
// own several rollouts (ccmux starts a fresh Codex thread on `ccmux reload`
// and for follow-up prompts, and Codex sub-agents fork their own), so every
// rollout whose cwd matches is summed.

// codexHome returns the directory Codex keeps its state in, honouring the
// same CODEX_HOME override the CLI does.
func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// codexRolloutCwd caches rollout path → session cwd. The header of a
// rollout never changes, and the sessions tree holds every thread the user
// ever ran, so re-reading each file's first line on every poll would be the
// dominant cost of this scan.
var codexRolloutCwd sync.Map

// codexRolloutLine is the common envelope of every rollout line; payload is
// decoded per type.
type codexRolloutLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type codexTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

// sub returns u - o, or ok=false when any field went backwards — which
// means the cumulative counter was reset rather than advanced.
func (u codexTokenUsage) sub(o codexTokenUsage) (codexTokenUsage, bool) {
	d := codexTokenUsage{
		InputTokens:           u.InputTokens - o.InputTokens,
		CachedInputTokens:     u.CachedInputTokens - o.CachedInputTokens,
		OutputTokens:          u.OutputTokens - o.OutputTokens,
		ReasoningOutputTokens: u.ReasoningOutputTokens - o.ReasoningOutputTokens,
		TotalTokens:           u.TotalTokens - o.TotalTokens,
	}
	if d.InputTokens < 0 || d.CachedInputTokens < 0 || d.OutputTokens < 0 {
		return codexTokenUsage{}, false
	}
	return d, true
}

func (u codexTokenUsage) isZero() bool {
	return u.InputTokens == 0 && u.CachedInputTokens == 0 && u.OutputTokens == 0
}

// readCodexRolloutCwd returns the cwd recorded in a rollout's session_meta,
// or "" when the header is missing or unreadable. Only the first few lines
// are examined; session_meta is always first in practice.
func readCodexRolloutCwd(path string) string {
	if v, ok := codexRolloutCwd.Load(path); ok {
		return v.(string)
	}
	cwd := ""
	if f, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for i := 0; i < 5 && scanner.Scan(); i++ {
			var line codexRolloutLine
			if json.Unmarshal(scanner.Bytes(), &line) != nil || line.Type != "session_meta" {
				continue
			}
			var meta struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(line.Payload, &meta) == nil {
				cwd = meta.Cwd
			}
			break
		}
		f.Close()
		// Only cache a header we actually found: a rollout whose first
		// write has not landed yet will grow a header shortly.
		if cwd != "" {
			codexRolloutCwd.Store(path, cwd)
		}
	}
	return cwd
}

// codexRolloutsFor lists the rollout files started in the agent's worktree.
// Rollouts live under date directories named for the local day they
// started, so a non-zero createdAt prunes the walk to that day onward (with
// a day of slack for clock and timezone skew).
func codexRolloutsFor(worktreePath string, createdAt time.Time) []string {
	root := filepath.Join(codexHome(), "sessions")
	var minDay string
	if !createdAt.IsZero() {
		minDay = createdAt.Local().AddDate(0, 0, -1).Format("2006/01/02")
	}
	var out []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		if d.IsDir() {
			// Prune date directories older than the agent. Compare only
			// once we are at YYYY/MM/DD depth; shallower dirs are prefixes.
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if minDay != "" && rel != "." && len(parts) <= 3 {
				prefix := strings.Join(strings.Split(minDay, "/")[:len(parts)], "/")
				if filepath.ToSlash(rel) < prefix {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if readCodexRolloutCwd(path) == worktreePath {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// scanCodexRollout turns one rollout into per-request usage records.
func scanCodexRollout(path string) []usageRecord {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		recs      []usageRecord
		model     string
		prevTotal *codexTokenUsage
	)
	for scanner.Scan() {
		var line codexRolloutLine
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		switch line.Type {
		case "turn_context":
			var tc struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(line.Payload, &tc) == nil && tc.Model != "" {
				model = tc.Model
			}
		case "event_msg":
			var ev struct {
				Type string `json:"type"`
				Info *struct {
					TotalTokenUsage codexTokenUsage  `json:"total_token_usage"`
					LastTokenUsage  *codexTokenUsage `json:"last_token_usage"`
				} `json:"info"`
			}
			if json.Unmarshal(line.Payload, &ev) != nil || ev.Type != "token_count" || ev.Info == nil {
				// token_count events with a null info are rate-limit
				// refreshes, not usage.
				continue
			}
			total := ev.Info.TotalTokenUsage
			var delta codexTokenUsage
			switch {
			case prevTotal == nil:
				// First snapshot in this rollout. A thread forked from a
				// parent inherits the parent's cumulative total here, so
				// count only the request that produced this snapshot.
				if ev.Info.LastTokenUsage != nil {
					delta = *ev.Info.LastTokenUsage
				} else {
					delta = total
				}
			default:
				if d, ok := total.sub(*prevTotal); ok {
					delta = d
				} else if ev.Info.LastTokenUsage != nil {
					// Counter went backwards (new thread baseline);
					// fall back to the per-request figure.
					delta = *ev.Info.LastTokenUsage
				} else {
					delta = total
				}
			}
			t := total
			prevTotal = &t
			if delta.isZero() {
				continue
			}
			cached := delta.CachedInputTokens
			if cached > delta.InputTokens {
				cached = delta.InputTokens
			}
			recs = append(recs, usageRecord{
				model:     model,
				date:      extractDate(line.Timestamp),
				in:        delta.InputTokens - cached,
				out:       delta.OutputTokens,
				cacheRead: cached,
				costUSD:   priceCodexCall(model, delta.InputTokens, cached, delta.OutputTokens),
			})
		}
	}
	return recs
}

// scanCodexSessions returns every priced request across all rollouts an
// agent's worktree owns.
func scanCodexSessions(worktreePath string, createdAt time.Time) []usageRecord {
	var recs []usageRecord
	for _, p := range codexRolloutsFor(worktreePath, createdAt) {
		recs = append(recs, scanCodexRollout(p)...)
	}
	return recs
}
