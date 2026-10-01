package tui

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OpenCode usage. OpenCode keeps every session in one SQLite database
// ($XDG_DATA_HOME/opencode/opencode.db, i.e. ~/.local/share/opencode on
// macOS too; OPENCODE_DB overrides it). Each `session` row records the
// directory OpenCode ran in — the agent's worktree — and each assistant
// `message` row carries a JSON blob with that request's tokens and the cost
// OpenCode computed from models.dev prices. Every session started in the
// worktree is summed, which covers sub-agent (task tool) sessions and any
// fresh session a `ccmux reload --harness opencode` started.
//
// The database is read with the system sqlite3 CLI, read-only, rather than a
// linked SQLite driver: ccmux is a single static binary and only needs one
// query. Without sqlite3 on PATH OpenCode agents simply show no cost.

// openCodeDBPath returns OpenCode's database path, honouring the same
// OPENCODE_DB and XDG_DATA_HOME overrides the CLI does.
func openCodeDBPath() string {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dataDir = filepath.Join(home, ".local", "share")
	}
	dataDir = filepath.Join(dataDir, "opencode")
	if db := os.Getenv("OPENCODE_DB"); db != "" {
		if db == ":memory:" {
			return ""
		}
		if filepath.IsAbs(db) {
			return db
		}
		return filepath.Join(dataDir, db)
	}
	return filepath.Join(dataDir, "opencode.db")
}

// openCodeMessageRow is one assistant message as selected by
// openCodeUsageQuery.
type openCodeMessageRow struct {
	Created    int64   `json:"created"` // unix ms
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	Cost       float64 `json:"cost"`
	Input      int64   `json:"input"` // excludes cache reads/writes
	Output     int64   `json:"output"`
	Reasoning  int64   `json:"reasoning"` // separate from output
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
}

func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func openCodeUsageQuery(worktreePath string) string {
	return `SELECT m.time_created AS created,
  coalesce(json_extract(m.data, '$.providerID'), '') AS provider,
  coalesce(json_extract(m.data, '$.modelID'), '') AS model,
  coalesce(json_extract(m.data, '$.cost'), 0) AS cost,
  coalesce(json_extract(m.data, '$.tokens.input'), 0) AS input,
  coalesce(json_extract(m.data, '$.tokens.output'), 0) AS output,
  coalesce(json_extract(m.data, '$.tokens.reasoning'), 0) AS reasoning,
  coalesce(json_extract(m.data, '$.tokens.cache.read'), 0) AS cache_read,
  coalesce(json_extract(m.data, '$.tokens.cache.write'), 0) AS cache_write
FROM message m JOIN session s ON s.id = m.session_id
WHERE s.directory = ` + sqlString(worktreePath) + ` AND json_extract(m.data, '$.role') = 'assistant'
ORDER BY m.time_created;`
}

// openCodeRecords turns message rows into priced usage records. OpenCode's
// own cost is used when it has one; requests it priced at zero on the OpenAI
// provider (a ChatGPT-subscription login) are priced at OpenAI list rates,
// matching how Codex agents are shown. Other zero-cost rows (free models)
// stay free.
func openCodeRecords(rows []openCodeMessageRow) []usageRecord {
	recs := make([]usageRecord, 0, len(rows))
	for _, r := range rows {
		out := r.Output + r.Reasoning
		if r.Input == 0 && out == 0 && r.CacheRead == 0 && r.CacheWrite == 0 {
			continue
		}
		cost := r.Cost
		if cost <= 0 && r.Provider == "openai" {
			cost = priceCodexCall(r.Model, r.Input+r.CacheRead, r.CacheRead, out)
		}
		recs = append(recs, usageRecord{
			model:       r.Model,
			date:        time.UnixMilli(r.Created).UTC().Format("2006-01-02"),
			in:          r.Input,
			out:         out,
			cacheRead:   r.CacheRead,
			cacheCreate: r.CacheWrite,
			costUSD:     cost,
		})
	}
	return recs
}

// scanOpenCodeSessions returns every priced request OpenCode recorded for
// the agent's worktree.
func scanOpenCodeSessions(worktreePath string) []usageRecord {
	dbPath := openCodeDBPath()
	if dbPath == "" || worktreePath == "" {
		return nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// -readonly plus a busy timeout: OpenCode holds the database open in
	// WAL mode while the agent runs, and a read must never block its writes.
	cmd := exec.CommandContext(ctx, sqlite, "-readonly", "-json", "-cmd", ".timeout 2000", dbPath, openCodeUsageQuery(worktreePath))
	out, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return nil
	}
	var rows []openCodeMessageRow
	if json.Unmarshal(out, &rows) != nil {
		return nil
	}
	return openCodeRecords(rows)
}
