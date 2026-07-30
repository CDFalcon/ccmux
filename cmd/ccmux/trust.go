package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CDFalcon/ccmux/internal/lockfile"
	"github.com/spf13/cobra"
)

// trustClaudeProject marks a worktree as trusted in ~/.claude.json so the agent
// does not stop at Claude Code's trust dialog on first launch.
//
// This replaces the inline shell that used to live in the launcher script:
//
//	jq --arg path "$WORKTREE_PATH" '.projects[$path].hasTrustDialogAccepted = true' \
//	  "$CLAUDE_JSON" > "${CLAUDE_JSON}.tmp" && mv "${CLAUDE_JSON}.tmp" "$CLAUDE_JSON"
//
// Launching several agents in one command ran several copies of that
// concurrently, all using the same `$HOME/.claude.json.tmp` scratch path. One
// spawn's `mv` renamed the file out from under the others, so a sibling failed:
//
//	mv: rename /Users/x/.claude.json.tmp to /Users/x/.claude.json: No such file or directory
//
// Under the launcher's `set -e` that killed the pane mid-setup — before
// `ccmux register-agent` — leaving the agent in `spawning` with an empty
// branch_name and no way to ever leave it. Even the non-fatal outcome was wrong:
// both jq processes read the same pre-image, so the last rename silently dropped
// the other worktree's trust entry and that agent hit the trust dialog.
//
// Doing it in Go lets us hold a real cross-process lock for the whole
// read-modify-write and replace the file atomically via a unique temp name.
//
// The mutation is deliberately minimal. ~/.claude.json is Claude Code's own
// state file, not ours: we decode into a generic map, set exactly one nested
// key, and re-encode, so no unknown field is dropped on the way through.
func trustClaudeProject(worktreePath string) error {
	if strings.TrimSpace(worktreePath) == "" {
		return fmt.Errorf("worktree path is required")
	}
	if !filepath.IsAbs(worktreePath) {
		abs, err := filepath.Abs(worktreePath)
		if err != nil {
			return fmt.Errorf("failed to resolve worktree path: %w", err)
		}
		worktreePath = abs
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}
	configPath := filepath.Join(homeDir, ".claude.json")

	return lockfile.WithLock(configPath, lockfile.DefaultTimeout, func() error {
		return upsertClaudeTrust(configPath, worktreePath)
	})
}

// upsertClaudeTrust performs the read-modify-write. Split out from the locking so
// tests can drive it directly.
func upsertClaudeTrust(configPath, worktreePath string) error {
	mode := os.FileMode(0o644)
	raw, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if info, statErr := os.Stat(configPath); statErr == nil {
			mode = info.Mode().Perm()
		}
	case os.IsNotExist(err):
		raw = []byte("{}")
	default:
		return fmt.Errorf("failed to read %s: %w", configPath, err)
	}

	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = []byte("{}")
	}

	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		// Refuse rather than overwrite: this file is Claude Code's state, and
		// clobbering it would lose the user's history, MCP config and auth.
		return fmt.Errorf("failed to parse %s (refusing to overwrite): %w", configPath, err)
	}
	if config == nil {
		config = map[string]any{}
	}

	projects, _ := config["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	entry, _ := projects[worktreePath].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}

	if trusted, ok := entry["hasTrustDialogAccepted"].(bool); ok && trusted {
		return nil // already trusted; leave the file untouched
	}
	entry["hasTrustDialogAccepted"] = true
	projects[worktreePath] = entry
	config["projects"] = projects

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode %s: %w", configPath, err)
	}
	out = append(out, '\n')

	return lockfile.WriteFileAtomic(configPath, out, mode)
}

func trustClaudeProjectCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "trust-claude-project <path>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return trustClaudeProject(args[0])
		},
	}
}
