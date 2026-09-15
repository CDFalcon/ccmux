package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/dailycost"
	"github.com/CDFalcon/ccmux/internal/harness"
	"github.com/CDFalcon/ccmux/internal/lockfile"
	"github.com/CDFalcon/ccmux/internal/logging"
	"github.com/CDFalcon/ccmux/internal/otelcollector"
	"github.com/CDFalcon/ccmux/internal/project"
	"github.com/CDFalcon/ccmux/internal/prompt"
	"github.com/CDFalcon/ccmux/internal/queue"
	"github.com/CDFalcon/ccmux/internal/settings"
	"github.com/CDFalcon/ccmux/internal/shellutil"
	"github.com/CDFalcon/ccmux/internal/sysprompt"
	"github.com/CDFalcon/ccmux/internal/tmux"
	"github.com/CDFalcon/ccmux/internal/tui"
	"github.com/CDFalcon/ccmux/internal/updater"
	"github.com/CDFalcon/ccmux/internal/version"
	"github.com/CDFalcon/ccmux/internal/worktree"
	"github.com/spf13/cobra"
)

const defaultSessionID = "default"

func main() {
	fmt.Println("ccmux starting up")
	logging.Init()
	defer logging.Close()

	rootCmd := &cobra.Command{
		Use:   "ccmux [session-id]",
		Short: "Colby's Claude Multiplexer - manage multiple Claude agents in parallel",
		Long: `ccmux starts or attaches to a Claude agent orchestrator session.

Without arguments, uses the "default" session.
With a session-id argument, uses that specific session.

Examples:
  ccmux              # Start or attach to "default" session
  ccmux my-project   # Start or attach to "my-project" session`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := defaultSessionID
			if len(args) > 0 {
				sessionID = args[0]
			}

			return runSession(sessionID)
		},
	}

	rootCmd.AddCommand(
		versionCmd(),
		updateCmd(),
		spawnCmd(),
		taskCmd(),
		trustCodexProjectCmd(),
		trustClaudeProjectCmd(),
		registerAgentCmd(),
		agentFailedCmd(),
		queueAddCmd(),
		prReadyCmd(),
		ciWaitCmd(),
		agentStoppedCmd(),
		paneCmd(),
		agentsCmd(),
		reloadCmd(),
		focusCmd(),
		cleanupCmd(),
		killCmd(),
		killSessionCmd(),
		pruneCmd(),
		agentRmCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("ccmux %s (%s) built %s\n", version.Version, version.GitCommit, version.BuildDate)
		},
	}
}

func updateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update ccmux to the latest version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Current version: %s\n", version.Version)
			fmt.Println("Checking for updates...")

			beta := false
			if store, err := settings.NewStore(); err == nil {
				if s, err := store.Get(); err == nil {
					beta = s.BetaChannel
				}
			}

			latest, available, err := updater.CheckForUpdate(beta)
			if err != nil {
				return fmt.Errorf("failed to check for updates: %w", err)
			}

			if !available {
				fmt.Println("Already on the latest version.")
				return nil
			}

			fmt.Printf("Downloading %s...\n", latest)
			if err := updater.DownloadUpdate(latest); err != nil {
				return fmt.Errorf("update failed: %w", err)
			}

			fmt.Printf("Updated to %s. Restart ccmux to use the new version.\n", latest)
			return nil
		},
	}
}

func runSession(sessionID string) error {
	tmuxSessionName := fmt.Sprintf("ccmux-%s", sessionID)
	tmuxManager := tmux.NewManager(tmuxSessionName)

	if !tmux.InsideTmux() {
		if !tmuxManager.SessionExists() {
			homeDir, _ := os.UserHomeDir()

			recovered := false
			if homeDir != "" {
				var err error
				recovered, err = recoverOrphanedAgents(sessionID, tmuxManager, homeDir)
				if err != nil {
					logging.Log("recovery: error during agent recovery: %v", err)
				}
			}

			if !recovered {
				if homeDir != "" {
					sessionDir := filepath.Join(homeDir, ".ccmux", "sessions", sessionID)
					os.RemoveAll(sessionDir)
				}

				exePath, err := os.Executable()
				if err != nil {
					return fmt.Errorf("failed to get executable path: %w", err)
				}
				cmd := fmt.Sprintf("%s %s", exePath, sessionID)
				if err := tmuxManager.CreateSessionWithCommand(homeDir, cmd); err != nil {
					return err
				}
			}
		} else {
			tmuxManager.ForwardEnv()
			tmuxManager.SourceUserConfig()
			tmuxManager.EnsureRemainOnExit()
			tmuxManager.SetupAgentNavigation()

			exePath, err := os.Executable()
			if err == nil {
				cmd := fmt.Sprintf("%s %s", exePath, sessionID)
				tmuxManager.RespawnDeadPane(tmuxManager.FirstWindowTarget(), cmd)
			}

			tmuxManager.SelectFirstWindow()
		}
		return tmuxManager.AttachSession()
	}

	agentStore, err := agent.NewStore(sessionID)
	if err != nil {
		return err
	}

	queueManager, err := queue.NewQueue(sessionID)
	if err != nil {
		return err
	}

	projectStore, err := project.NewStore()
	if err != nil {
		return err
	}

	promptStore, err := prompt.NewStore()
	if err != nil {
		return err
	}

	settingsStore, err := settings.NewStore()
	if err != nil {
		return err
	}

	dailyCostStore, err := dailycost.NewStore()
	if err != nil {
		return err
	}

	// In-process OTel collector: receives `claude_code.cost.usage` from
	// every spawned Claude agent (see the OTEL_* block in the launcher
	// script) so the TUI can show Anthropic's own cost figure instead of
	// re-deriving it. Start failure is non-fatal — the TUI silently
	// falls back to the JSONL-derived estimate. Shutdown is tied to the
	// TUI's lifetime so the loopback port and endpoint advertisement file
	// are released before the next restart of ccmux runs.
	collectorCtx, cancelCollector := context.WithCancel(context.Background())
	defer cancelCollector()
	otelCollector, err := otelcollector.Start(collectorCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: OpenTelemetry collector failed to start (%v); falling back to JSONL cost estimation\n", err)
		otelCollector = nil
	}

	restart, err := tui.Run(agentStore, queueManager, projectStore, promptStore, settingsStore, dailyCostStore, otelCollector, tmuxManager, sessionID)
	if err != nil {
		return err
	}
	if restart {
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable for restart: %w", err)
		}
		return syscall.Exec(exePath, []string{exePath, sessionID}, os.Environ())
	}
	return nil
}

// spawnOptions carries everything runSpawn needs to launch a new agent. Only
// ProjectName and Task are required; the rest fall back to sensible defaults
// (the project's configured harness and base branch, an autogenerated
// worktree name, and no extra prompt content).
type spawnOptions struct {
	ProjectName   string
	Task          string
	BaseBranch    string
	WorktreeName  string
	PromptContent string
	HarnessName   string
}

// verifyBaseBranch confirms baseBranch exists on the project repo's "origin"
// remote before a task is spawned. The launcher script runs
// `git fetch origin <ref>`, which aborts the whole task with exit status 128
// when the ref is missing (e.g. passing origin/main to a repo whose default
// branch is master). Catching it here lets `ccmux task` / `ccmux spawn` fail
// fast with an actionable error instead of leaving a dead tmux window and an
// agent record orphaned in "spawning".
//
// baseBranch may be either "origin/<ref>" or a bare "<ref>"; the launcher
// always fetches from origin, so the leading "origin/" is stripped before the
// lookup.
func verifyBaseBranch(projName, repoPath, baseBranch string) error {
	ref := strings.TrimPrefix(baseBranch, "origin/")

	out, err := exec.Command("git", "-C", repoPath, "ls-remote", "--heads", "origin").CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not list branches on origin for project %q (%s): %v: %s",
			projName, repoPath, err, strings.TrimSpace(string(out)))
	}

	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// Each ls-remote line is "<sha>\trefs/heads/<branch>".
		if i := strings.Index(line, "refs/heads/"); i >= 0 {
			branch := line[i+len("refs/heads/"):]
			if branch == ref {
				return nil
			}
			branches = append(branches, branch)
		}
	}

	return fmt.Errorf("base branch %q not found on origin for project %q; available branches: %s",
		baseBranch, projName, strings.Join(branches, ", "))
}

// runSpawn creates a worktree, launcher script, tmux window, and agent record
// for a new task, returning the generated agent ID. It is the shared
// implementation behind both the hidden `spawn` command (driven by the TUI)
// and the user-facing `task` command (callable by a running agent).
func runSpawn(opts spawnOptions) (string, error) {
	logging.Log("spawn: starting for task=%q project=%q branch=%q worktreeName=%q harness=%q",
		opts.Task, opts.ProjectName, opts.BaseBranch, opts.WorktreeName, opts.HarnessName)

	if opts.ProjectName == "" {
		return "", fmt.Errorf("project is required")
	}
	if opts.Task == "" {
		return "", fmt.Errorf("task description is required")
	}
	if !tmux.InsideTmux() {
		return "", fmt.Errorf("must be run from inside a ccmux session")
	}

	if opts.HarnessName != "" && !harness.Valid(opts.HarnessName) {
		return "", fmt.Errorf("unknown harness: %s (expected claude or codex)", opts.HarnessName)
	}

	sessionID := getCurrentSessionID()
	agentID := generateID()
	logging.Log("spawn: generated agentID=%s sessionID=%s", agentID, sessionID)

	projectStore, err := project.NewStore()
	if err != nil {
		return "", err
	}
	proj, err := projectStore.Get(opts.ProjectName)
	if err != nil {
		return "", fmt.Errorf("project not found: %s", opts.ProjectName)
	}

	// Resolve the base branch: an explicit value wins, otherwise fall back to
	// the project's configured default (origin/master when the project has
	// none set).
	baseBranch := opts.BaseBranch
	if baseBranch == "" {
		baseBranch = proj.EffectiveBaseBranch()
	}

	// Fail fast when the base branch is missing from the repo's origin remote.
	// The launcher script's `git fetch origin <ref>` would otherwise abort the
	// whole task (exit 128), leaving a dead tmux window and an agent record
	// orphaned in "spawning" — a silent failure, since CreateWindow returns
	// before the script runs and the caller only sees "Spawned agent ...".
	if err := verifyBaseBranch(opts.ProjectName, proj.EffectivePath(), baseBranch); err != nil {
		return "", err
	}

	// An explicit harness wins; otherwise fall back to the project's
	// configured default.
	selectedHarness := proj.EffectiveHarness()
	if opts.HarnessName != "" {
		selectedHarness = harness.Parse(opts.HarnessName)
	}

	tmuxSessionName := fmt.Sprintf("ccmux-%s", sessionID)
	tmuxManager := tmux.NewManager(tmuxSessionName)

	worktreeName := sanitizeWorktreeName(opts.WorktreeName)

	launcherScript, err := writeLauncherScript(agentID, opts.Task, proj.EffectivePath(), baseBranch, sessionID, proj.UseFastWorktrees, worktreeName, opts.PromptContent, proj.StartupScript, selectedHarness, proj.EffectiveDraftPRs())
	if err != nil {
		return "", fmt.Errorf("failed to create launcher script: %w", err)
	}

	windowName := agentID[:8]
	if worktreeName != "" {
		windowName = worktreeName
	}
	windowID, paneID, err := tmuxManager.CreateWindow(proj.EffectivePath(), "bash "+launcherScript, windowName)
	if err != nil {
		os.Remove(launcherScript)
		return "", fmt.Errorf("failed to create tmux window: %w", err)
	}
	logging.Log("spawn: created window=%s pane=%s with launcher script", windowID, paneID)

	agentStore, err := agent.NewStore(sessionID)
	if err != nil {
		return "", err
	}
	a := &agent.Agent{
		ID:           agentID,
		Task:         opts.Task,
		ProjectName:  opts.ProjectName,
		Harness:      string(selectedHarness),
		WorktreeName: worktreeName,
		TmuxWindow:   windowID,
		TmuxPane:     paneID,
		BaseBranch:   baseBranch,
		Status:       agent.StatusSpawning,
	}
	if err := agentStore.Create(a); err != nil {
		return "", err
	}

	return agentID, nil
}

func spawnCmd() *cobra.Command {
	var projectName string
	var baseBranch string
	var worktreeName string
	var promptContent string
	var harnessName string

	cmd := &cobra.Command{
		Use:    "spawn <task>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			task := args[0]
			agentID, err := runSpawn(spawnOptions{
				ProjectName:   projectName,
				Task:          task,
				BaseBranch:    baseBranch,
				WorktreeName:  worktreeName,
				PromptContent: promptContent,
				HarnessName:   harnessName,
			})
			if err != nil {
				return err
			}

			fmt.Printf("Spawned agent %s for task: %s\n", agentID, task)
			return nil
		},
	}

	cmd.Flags().StringVar(&projectName, "project", "", "Project to use")
	cmd.Flags().StringVar(&baseBranch, "branch", "", "Base branch to create worktree from (default: project's configured base branch)")
	cmd.Flags().StringVar(&worktreeName, "worktree-name", "", "Optional human-readable name for the worktree and branch")
	cmd.Flags().StringVar(&promptContent, "prompts", "", "Custom prompt content to inject into the agent's system prompt")
	cmd.Flags().StringVar(&harnessName, "harness", "", "Coding agent CLI to launch: claude or codex (default: project default)")
	cmd.MarkFlagRequired("project")

	return cmd
}

// optionalArg normalises a positional argument that may be intentionally
// skipped. A literal "-" means "use the default", which lets a caller supply
// a later positional argument without committing to an earlier one.
func optionalArg(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

func taskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "task <project> <description> [harness] [base-branch] [branch-name]",
		Short: "Spawn a new ccmux agent task",
		Long: `task spawns a new ccmux agent in the current session.

It is intended to be called by a running agent to delegate or parallelise
work. Like spawning a task from the TUI, it creates a git worktree, branch,
and tmux window, then launches a coding agent on the described task.

Arguments:
  project      Name of a registered ccmux project (required)
  description  Task description handed to the new agent (required)
  harness      Coding agent CLI: claude or codex (optional, default: project default)
  base-branch  Base branch to create the worktree from (optional, default: project's configured base branch)
  branch-name  Human-readable name for the worktree and branch (optional)

The optional arguments are positional. To skip one while still supplying a
later one, pass "-" in its place.

Examples:
  ccmux task myproject "Fix the login bug"
  ccmux task myproject "Add dark mode" codex
  ccmux task myproject "Refactor the API" claude origin/develop
  ccmux task myproject "Write docs" - - docs-update`,
		Args: cobra.RangeArgs(2, 5),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := spawnOptions{
				ProjectName: args[0],
				Task:        args[1],
			}
			if len(args) > 2 {
				opts.HarnessName = optionalArg(args[2])
			}
			if len(args) > 3 {
				opts.BaseBranch = optionalArg(args[3])
			}
			if len(args) > 4 {
				opts.WorktreeName = optionalArg(args[4])
			}

			agentID, err := runSpawn(opts)
			if err != nil {
				return err
			}

			fmt.Printf("Spawned agent %s for task: %s\n", agentID, opts.Task)
			return nil
		},
	}
}

func codexConfigPath() (string, error) {
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		codexHome = filepath.Join(homeDir, ".codex")
	}
	return filepath.Join(codexHome, "config.toml"), nil
}

func tomlBasicString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				b.WriteString(fmt.Sprintf(`\u%04X`, r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func codexProjectTableHeader(projectPath string) string {
	return "[projects." + tomlBasicString(projectPath) + "]"
}

func isTomlKey(line, key string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, key) {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, key))
	return strings.HasPrefix(rest, "=")
}

func upsertCodexProjectTrust(config, projectPath string) string {
	header := codexProjectTableHeader(projectPath)
	trustLine := `trust_level = "trusted"`
	if config == "" {
		return header + "\n" + trustLine + "\n"
	}

	lines := strings.Split(config, "\n")
	sectionStart := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			sectionStart = i
			break
		}
	}
	if sectionStart == -1 {
		separator := ""
		if !strings.HasSuffix(config, "\n") {
			separator = "\n"
		}
		if strings.HasSuffix(config, "\n\n") {
			return config + separator + header + "\n" + trustLine + "\n"
		}
		return config + separator + "\n" + header + "\n" + trustLine + "\n"
	}

	sectionEnd := len(lines)
	for i := sectionStart + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			sectionEnd = i
			break
		}
	}
	for i := sectionStart + 1; i < sectionEnd; i++ {
		if isTomlKey(lines[i], "trust_level") {
			lines[i] = trustLine
			return strings.Join(lines, "\n")
		}
	}

	updated := append([]string{}, lines[:sectionStart+1]...)
	updated = append(updated, trustLine)
	updated = append(updated, lines[sectionStart+1:]...)
	return strings.Join(updated, "\n")
}

func trustCodexProject(projectPath string) error {
	if strings.TrimSpace(projectPath) == "" {
		return fmt.Errorf("project path is required")
	}
	if !filepath.IsAbs(projectPath) {
		absPath, err := filepath.Abs(projectPath)
		if err != nil {
			return fmt.Errorf("failed to resolve project path: %w", err)
		}
		projectPath = absPath
	}

	configPath, err := codexConfigPath()
	if err != nil {
		return err
	}

	// Same hazard as the Claude pretrust: concurrent spawns each did an unlocked
	// read-modify-write of one shared config, so the last writer dropped the
	// others' trust entries — and os.WriteFile truncates before writing, so a
	// crash mid-write could leave the file empty. Hold the lock across the whole
	// sequence and replace atomically.
	return lockfile.WithLock(configPath, lockfile.DefaultTimeout, func() error {
		data, err := os.ReadFile(configPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to read Codex config: %w", err)
		}

		updated := upsertCodexProjectTrust(string(data), projectPath)
		if string(data) == updated {
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
			return fmt.Errorf("failed to create Codex config directory: %w", err)
		}

		mode := os.FileMode(0600)
		if info, statErr := os.Stat(configPath); statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := lockfile.WriteFileAtomic(configPath, []byte(updated), mode); err != nil {
			return fmt.Errorf("failed to write Codex config: %w", err)
		}
		return nil
	})
}

func trustCodexProjectCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "trust-codex-project <path>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return trustCodexProject(args[0])
		},
	}
}

// postToolUseHookScript is the body of .claude/hooks/post_tool_use.sh that the
// launcher and recovery scripts install into every Claude worktree. It is one
// string so the two templates cannot drift, and so tests can run the hook
// directly against synthetic PostToolUse payloads.
const postToolUseHookScript = `#!/bin/bash
# ccmux PostToolUse hook: after a successful 'gh pr create' or 'git push',
# automatically kick off 'ccmux ci-wait' so the orchestrator can monitor CI
# without the agent having to invoke it manually. Silently no-ops for any
# other tool call.
#
# 'gh pr create' is the initial PR (URL extracted from output).
# 'git push' covers follow-up pushes addressing review comments or fixing
# CI failures — without this branch, the agent's status never flips back
# to waiting_review after the new CI passes.
set -u

INPUT=$(cat)

# Subagents spawned with worktree isolation (Agent isolation:"worktree",
# EnterWorktree) live under <worktree>/.claude/worktrees/<name> and share the
# parent's hooks. A 'gh pr create' or 'git push' from one of those is the
# subagent's PR on the subagent's branch — recording it as the parent's would
# flip the parent to waiting_ci/waiting_review while its real work sits
# uncommitted. Skip anything running from (or cd'ing into) such a worktree.
# 'ccmux ci-wait' independently verifies the PR's head branch, so this is
# the cheap first line, not the only one.
CWD=$(jq -r '.cwd // empty' <<<"$INPUT" 2>/dev/null || echo "")
if [[ "$CWD" == */.claude/worktrees/* ]]; then
  exit 0
fi

TOOL_NAME=$(jq -r '.tool_name // empty' <<<"$INPUT" 2>/dev/null || echo "")
if [ "$TOOL_NAME" != "Bash" ]; then
  exit 0
fi

if [ -z "${CCMUX_AGENT_ID:-}" ]; then
  exit 0
fi

COMMAND=$(jq -r '.tool_input.command // empty' <<<"$INPUT" 2>/dev/null || echo "")
if [[ "$COMMAND" == *".claude/worktrees/"* ]]; then
  exit 0
fi

if grep -qE '(^|[[:space:]&;|(])gh[[:space:]]+pr[[:space:]]+create([[:space:]]|$)' <<<"$COMMAND"; then
  STDOUT=$(jq -r '.tool_response.stdout // .tool_response.output // empty' <<<"$INPUT" 2>/dev/null || echo "")
  PR_URL=$(grep -oE 'https://github\.com/[^[:space:]]+/pull/[0-9]+' <<<"$STDOUT" | tail -n1)
  if [ -n "$PR_URL" ]; then
    nohup ccmux ci-wait "$PR_URL" >/dev/null 2>&1 </dev/null &
    disown 2>/dev/null || true
  fi
  exit 0
fi

if grep -qE '(^|[[:space:]&;|(])git[[:space:]]+push([[:space:]]|$)' <<<"$COMMAND"; then
  # No URL arg — ci-wait will use the agent's stored PR URL, or no-op if
  # the agent doesn't have one yet (e.g. pushing the branch before
  # 'gh pr create').
  nohup ccmux ci-wait >/dev/null 2>&1 </dev/null &
  disown 2>/dev/null || true
  exit 0
fi

exit 0
`

func writeLauncherScript(agentID, task, repoPath, baseBranch, sessionID string, useFastWorktrees bool, worktreeName string, promptContent string, startupScript string, h harness.Type, draftPRs bool) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+".sh")

	useFastWT := "0"
	if useFastWorktrees {
		useFastWT = "1"
	}

	draftPRsFlag := "0"
	if draftPRs {
		draftPRsFlag = "1"
	}

	wtSuffix := agentID
	if worktreeName != "" {
		wtSuffix = worktreeName + "-" + agentID
	}

	sq := shellutil.Quote
	script := fmt.Sprintf(`#!/bin/bash
set -e

AGENT_ID=%s
TASK=%s
REPO_PATH=%s
BASE_BRANCH=%s
SESSION_ID=%s
USE_FAST_WT=%s
WT_SUFFIX=%s
HARNESS=%s
DRAFT_PRS=%s

# Setup runs under "set -e", so any failure below aborts this script and the
# pane dies mid-setup. Without this trap the agent record was simply left in
# status spawning with an empty branch_name forever - indistinguishable from a
# slow spawn, so nothing watching could tell it had died. Mark it failed instead.
#
# Cleared once "ccmux register-agent" succeeds; after that the agent's own
# lifecycle owns its status.
CCMUX_REGISTERED=0
ccmux_on_exit() {
  ccmux_exit=$?
  if [ "$ccmux_exit" -ne 0 ] && [ "$CCMUX_REGISTERED" -eq 0 ]; then
    ccmux agent-failed "$AGENT_ID" \
      --reason="spawn failed during setup (exit $ccmux_exit)" || true
  fi
}
trap ccmux_on_exit EXIT

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
RESET="\033[0m"
echo -e "${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Task:${RESET} $TASK"
echo -e "${DIM}Harness:${RESET} $HARNESS"
echo ""

if [ "$USE_FAST_WT" = "1" ]; then
  # Fast worktree mode using rift (copy-on-write snapshot of the repo)
  WORKTREE_PARENT="$(dirname "$REPO_PATH")"
  WORKTREE_PATH="$WORKTREE_PARENT/ccmux-$WT_SUFFIX"
  BRANCH_NAME="ccmux/$WT_SUFFIX"

  # Fetch in the SOURCE repo first so the snapshot picks up fresh remote
  # refs immediately. rift create then clones a repo that already knows
  # about $BASE_BRANCH, avoiding a redundant fetch in the new workspace.
  FETCH_REF="${BASE_BRANCH#origin/}"
  echo "→ Fetching latest $FETCH_REF from origin..."
  cd "$REPO_PATH"
  git fetch origin "$FETCH_REF"

  echo "→ Creating fast worktree at $WORKTREE_PATH..."
  rift create --name "ccmux-$WT_SUFFIX" --into "$WORKTREE_PARENT" > /dev/null
  cd "$WORKTREE_PATH"
  echo "✓ Fast worktree created (rift)"
  echo ""

  # Each rift snapshot has its own .git, so the source repo's branches
  # exist locally in the snapshot too — just check one out at $BASE_BRANCH.
  echo "→ Creating branch $BRANCH_NAME..."
  git checkout -B "$BRANCH_NAME" "$BASE_BRANCH"
  echo "✓ Branch created"
  echo ""

  echo "→ Updating remote branch refs..."
  git ls-remote --heads origin | while read sha ref; do
    git update-ref "refs/remotes/origin/${ref#refs/heads/}" "$sha" 2>/dev/null || true
  done
  echo "✓ Remote branch refs updated"
  echo ""
else
  # Standard git worktree mode
  WORKTREE_PATH="$(dirname "$REPO_PATH")/ccmux-$WT_SUFFIX"
  BRANCH_NAME="ccmux/$WT_SUFFIX"

  echo "→ Creating worktree at $WORKTREE_PATH..."
  cd "$REPO_PATH"
  FETCH_REF="${BASE_BRANCH#origin/}"
  echo "→ Fetching latest $FETCH_REF from origin..."
  git fetch origin "$FETCH_REF"
  git worktree add -b "$BRANCH_NAME" "$WORKTREE_PATH" "$BASE_BRANCH"
  cd "$WORKTREE_PATH"
  echo "✓ Worktree created"
  echo ""

  echo "→ Updating remote branch refs..."
  git ls-remote --heads origin | while read sha ref; do
    git update-ref "refs/remotes/origin/${ref#refs/heads/}" "$sha" 2>/dev/null || true
  done
  echo "✓ Remote branch refs updated"
  echo ""
fi

# Claude Code hook installation + directory trust. These are specific to the
# Claude harness; other harnesses (e.g. Codex) ignore .claude/ entirely.
if [ "$HARNESS" = "claude" ]; then
echo "→ Installing Claude Code hooks..."
mkdir -p .claude/hooks

cat > .claude/hooks/stop.sh << 'HOOKEOF'
#!/bin/bash
ccmux agent-stopped "$CCMUX_AGENT_ID"
HOOKEOF
chmod +x .claude/hooks/stop.sh

cat > .claude/hooks/post_tool_use.sh << 'HOOKEOF'
`+postToolUseHookScript+`HOOKEOF
chmod +x .claude/hooks/post_tool_use.sh

CCMUX_STOP_CMD="CCMUX_AGENT_ID=$AGENT_ID $WORKTREE_PATH/.claude/hooks/stop.sh"
CCMUX_PTU_CMD="CCMUX_AGENT_ID=$AGENT_ID $WORKTREE_PATH/.claude/hooks/post_tool_use.sh"

if [ -f .claude/settings.json ]; then
  EXISTING=$(cat .claude/settings.json)
  CLEANED=$(echo "$EXISTING" | jq '
    .hooks.Stop = [((.hooks.Stop // [])[]) | select((.hooks // []) | any(.command | contains("ccmux agent-stopped") or contains("/.claude/hooks/stop.sh")) | not)]
    | .hooks.PostToolUse = [((.hooks.PostToolUse // [])[]) | select((.hooks // []) | any(.command | contains("/.claude/hooks/post_tool_use.sh")) | not)]
  ')
  echo "$CLEANED" | jq \
    --arg stop_cmd "$CCMUX_STOP_CMD" \
    --arg ptu_cmd "$CCMUX_PTU_CMD" \
    '.hooks.Stop = ((.hooks.Stop // []) + [{hooks: [{type: "command", command: $stop_cmd}]}])
     | .hooks.PostToolUse = ((.hooks.PostToolUse // []) + [{matcher: "Bash", hooks: [{type: "command", command: $ptu_cmd}]}])' \
    > .claude/settings.json
else
  jq -n \
    --arg stop_cmd "$CCMUX_STOP_CMD" \
    --arg ptu_cmd "$CCMUX_PTU_CMD" \
    '{hooks: {Stop: [{hooks: [{type: "command", command: $stop_cmd}]}], PostToolUse: [{matcher: "Bash", hooks: [{type: "command", command: $ptu_cmd}]}]}}' \
    > .claude/settings.json
fi

# Prevent ccmux hook files from being committed
GIT_COMMON_DIR=$(git rev-parse --git-common-dir)
EXCLUDE_FILE="$GIT_COMMON_DIR/info/exclude"
mkdir -p "$GIT_COMMON_DIR/info"
STOP_SH_TRACKED=$(git ls-files .claude/hooks/stop.sh)
PTU_SH_TRACKED=$(git ls-files .claude/hooks/post_tool_use.sh)
SETTINGS_TRACKED=$(git ls-files .claude/settings.json)

if [ -z "$STOP_SH_TRACKED" ]; then
  grep -qxF '.claude/hooks/stop.sh' "$EXCLUDE_FILE" 2>/dev/null || echo '.claude/hooks/stop.sh' >> "$EXCLUDE_FILE"
else
  git update-index --assume-unchanged .claude/hooks/stop.sh
fi

if [ -z "$PTU_SH_TRACKED" ]; then
  grep -qxF '.claude/hooks/post_tool_use.sh' "$EXCLUDE_FILE" 2>/dev/null || echo '.claude/hooks/post_tool_use.sh' >> "$EXCLUDE_FILE"
else
  git update-index --assume-unchanged .claude/hooks/post_tool_use.sh
fi

if [ -z "$SETTINGS_TRACKED" ]; then
  grep -qxF '.claude/settings.json' "$EXCLUDE_FILE" 2>/dev/null || echo '.claude/settings.json' >> "$EXCLUDE_FILE"
else
  git update-index --assume-unchanged .claude/settings.json
fi

echo "✓ Hooks installed"
echo ""

# Pre-trust worktree directory in Claude Code.
#
# This is a locked, atomic read-modify-write in Go rather than the jq/mv
# one-liner it replaces: concurrent spawns all used the same
# $HOME/.claude.json.tmp scratch path, so one spawn's mv renamed the file away
# and a sibling died with "mv: rename ... No such file or directory" — under
# set -e, before register-agent ever ran.
echo "→ Pre-trusting worktree directory..."
ccmux trust-claude-project "$WORKTREE_PATH"
echo "✓ Directory trusted"
echo ""
fi

if [ "$HARNESS" = "codex" ]; then
  echo "→ Pre-trusting worktree directory in Codex..."
  ccmux trust-codex-project "$WORKTREE_PATH"
  echo "✓ Directory trusted"
  echo ""
fi

# Register agent
echo "→ Registering agent..."
WINDOW_ID=$(tmux display-message -p '#{window_id}')
ccmux register-agent --id="$AGENT_ID" --task="$TASK" --worktree="$WORKTREE_PATH" --branch="$BRANCH_NAME" --base="$BASE_BRANCH" --window="$WINDOW_ID"
CCMUX_REGISTERED=1
echo "✓ Agent registered"
echo ""

# Store the worktree path in a tmux window option so that any new pane opened
# in this window (e.g. via prefix-%% or prefix-") automatically cds there.
tmux set-option -w @ccmux_worktree "$WORKTREE_PATH"

STARTUP_SCRIPT=%s
if [ -n "$STARTUP_SCRIPT" ] && [ -f "$STARTUP_SCRIPT" ]; then
  echo "→ Running startup script: $STARTUP_SCRIPT"
  bash "$STARTUP_SCRIPT"
  echo "✓ Startup script completed"
  echo ""
fi

echo -e "${DIM}Starting $HARNESS...${RESET}"
echo ""

cd "$WORKTREE_PATH"

export CCMUX_AGENT_ID="$AGENT_ID"
unset CLAUDECODE

`+harness.TelemetryEnvBlock+`
PR_BASE_BRANCH="${BASE_BRANCH#origin/}"

PR_DRAFT_FLAG=""
PR_DRAFT_NOTE="IMPORTANT: this project opens pull requests ready for review — do NOT add a --draft flag."
if [ "$DRAFT_PRS" = "1" ]; then
  PR_DRAFT_FLAG="--draft "
  PR_DRAFT_NOTE="This project opens pull requests as drafts — keep the --draft flag."
fi

SYSTEM_PROMPT="You are working on a task as part of the ccmux agent system. Environment variable CCMUX_AGENT_ID=$AGENT_ID is set for hook integration.

When done with your task, commit your work and create a PR with:
    gh pr create ${PR_DRAFT_FLAG}--base $PR_BASE_BRANCH --title \"...\" --body \"...\"
${PR_DRAFT_NOTE}

`+sysprompt.SharePaneDoc+`

`+sysprompt.PeerAgentsDoc+`

`+sysprompt.ReloadDoc+`"

CLAUDE_MD_PATH="$HOME/.claude/CLAUDE.md"
if [ -f "$CLAUDE_MD_PATH" ]; then
  CLAUDE_MD_CONTENT=$(cat "$CLAUDE_MD_PATH")
  SYSTEM_PROMPT="${SYSTEM_PROMPT}

${CLAUDE_MD_CONTENT}"
fi

PROMPTS_FILE=%s
if [ -f "$PROMPTS_FILE" ]; then
  PROMPTS_CONTENT=$(cat "$PROMPTS_FILE")
  SYSTEM_PROMPT="${SYSTEM_PROMPT}

${PROMPTS_CONTENT}"
fi

`+harness.SystemPromptFileBlock+`
`+harness.ExitCapturePrologue+`%s
`+harness.ExitCaptureCapture+harness.ExitCaptureReport, sq(agentID), sq(task), sq(repoPath), sq(baseBranch), sq(sessionID), sq(useFastWT), sq(wtSuffix), sq(string(h)), sq(draftPRsFlag), sq(startupScript), sq(promptsFilePath(agentID)), h.StartCommand())

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	if promptContent != "" {
		if err := os.WriteFile(promptsFilePath(agentID), []byte(promptContent), 0644); err != nil {
			return "", fmt.Errorf("failed to write prompts file: %w", err)
		}
	}

	return scriptPath, nil
}

// launcherFileSuffixes lists every file ccmux may write under
// ~/.ccmux/launchers for an agent: <id><suffix>. Keep it in sync with the
// script writers; removeLauncherFiles and `ccmux prune` clean up by it.
var launcherFileSuffixes = []string{
	".sh", "-review.sh", "-recovery.sh", "-placeholder.sh",
	"-ci-fix.sh", "-merge-conflict.sh", "-restart.sh", "-reload.sh", "-prompts.txt",
	"-system-prompt.txt",
}

// removeLauncherFiles deletes an agent's launcher scripts and prompts file.
func removeLauncherFiles(launcherDir, agentID string) {
	for _, suffix := range launcherFileSuffixes {
		os.Remove(filepath.Join(launcherDir, agentID+suffix))
	}
}

func promptsFilePath(agentID string) string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".ccmux", "launchers", agentID+"-prompts.txt")
}

func registerAgentCmd() *cobra.Command {
	var id, task, worktreePath, branch, baseBranch, window string

	cmd := &cobra.Command{
		Use:    "register-agent",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := getCurrentSessionID()

			agentStore, err := agent.NewStore(sessionID)
			if err != nil {
				return err
			}

			return agentStore.Update(id, func(a *agent.Agent) {
				a.WorktreePath = worktreePath
				a.BranchName = branch
				a.Status = agent.StatusRunning
			})
		},
	}

	cmd.Flags().StringVar(&id, "id", "", "Agent ID")
	cmd.Flags().StringVar(&task, "task", "", "Task description")
	cmd.Flags().StringVar(&worktreePath, "worktree", "", "Worktree path")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch name")
	cmd.Flags().StringVar(&baseBranch, "base", "", "Base branch")
	cmd.Flags().StringVar(&window, "window", "", "Tmux window ID")
	cmd.MarkFlagRequired("id")
	cmd.MarkFlagRequired("task")
	cmd.MarkFlagRequired("worktree")
	cmd.MarkFlagRequired("branch")
	cmd.MarkFlagRequired("base")
	cmd.MarkFlagRequired("window")

	return cmd
}

func queueAddCmd() *cobra.Command {
	var itemType, agentID, summary, details string

	cmd := &cobra.Command{
		Use:    "queue-add",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := getCurrentSessionID()
			queueManager, err := queue.NewQueue(sessionID)
			if err != nil {
				return err
			}

			qType := queue.ItemType(itemType)
			_, err = queueManager.Add(qType, agentID, summary, details)
			return err
		},
	}

	cmd.Flags().StringVar(&itemType, "type", "", "Item type")
	cmd.Flags().StringVar(&agentID, "agent", "", "Agent ID")
	cmd.Flags().StringVar(&summary, "summary", "", "Brief summary")
	cmd.Flags().StringVar(&details, "details", "", "Full details")
	cmd.MarkFlagRequired("type")
	cmd.MarkFlagRequired("agent")

	return cmd
}

func prReadyCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "pr-ready <pr-url>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prURL := args[0]

			agentID := os.Getenv("CCMUX_AGENT_ID")
			if agentID == "" {
				return fmt.Errorf("CCMUX_AGENT_ID environment variable not set")
			}

			sessionID := getCurrentSessionID()

			summary := getPRTitle(prURL)
			if summary == "" {
				summary = fmt.Sprintf("PR ready: %s", prURL)
			}

			queueManager, err := queue.NewQueue(sessionID)
			if err != nil {
				return err
			}

			_, err = queueManager.Add(queue.ItemTypePRReady, agentID, summary, prURL)
			if err != nil {
				return err
			}

			agentStore, err := agent.NewStore(sessionID)
			if err != nil {
				return err
			}

			// Record the URL alongside the status. Without it the agent is
			// marked "waiting for review" while nothing in ccmux knows which PR
			// to show, poll or merge — the exact PR-that-does-not-exist state
			// recovery now has to defend against.
			return agentStore.Update(agentID, func(a *agent.Agent) {
				a.Status = agent.StatusWaitingReview
				a.PRURL = prURL
			})
		},
	}
}

func ciWaitCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "ci-wait [pr-url]",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID := os.Getenv("CCMUX_AGENT_ID")
			if agentID == "" {
				return fmt.Errorf("CCMUX_AGENT_ID environment variable not set")
			}

			sessionID := getCurrentSessionID()

			agentStore, err := agent.NewStore(sessionID)
			if err != nil {
				return err
			}

			var prURL string
			if len(args) > 0 {
				prURL = args[0]
			}

			queueManager, err := queue.NewQueue(sessionID)
			if err != nil {
				return err
			}

			return recordPRForCIWait(agentStore, queueManager, agentID, prURL, lookupPRHeadBranch)
		},
	}
}

// lookupPRHeadBranch asks gh which branch a PR was opened from.
func lookupPRHeadBranch(prURL string) (string, error) {
	out, err := exec.Command("gh", "pr", "view", prURL, "--json", "headRefName", "-q", ".headRefName").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// recordPRForCIWait is the body of `ccmux ci-wait`: it flips the agent to
// StatusWaitingCI for prURL so the orchestrator's poller picks it up.
//
// With no prURL it falls back to the agent's stored URL so the PostToolUse
// hook can fire on a plain `git push` (resume scenarios) without extracting a
// URL from the tool output; if the agent has no PR yet it is a no-op.
//
// With an explicit prURL the PR's head branch must match the agent's own
// branch_name. Claude subagents spawned with worktree isolation share the
// parent's hooks, so a `gh pr create` from <worktree>/.claude/worktrees/<x>
// reaches this command with the parent's CCMUX_AGENT_ID. Recording that PR
// flipped the parent to waiting_ci/waiting_review for a PR on the subagent's
// branch while the parent's actual work sat uncommitted — a "finished-turn-
// without-PR" death that looked healthy. A mismatch is dropped silently: the
// hook has nowhere useful to report it, and the parent's own PR will arrive
// through the same path later. A failed lookup (offline, gh not authed)
// falls open to the pre-existing behaviour so ci-wait never loses a real PR
// to a transient network error.
func recordPRForCIWait(agentStore *agent.Store, queueManager *queue.Queue, agentID, prURL string, lookupHead func(string) (string, error)) error {
	a, err := agentStore.Get(agentID)
	if err != nil {
		return err
	}

	if prURL == "" {
		if a.PRURL == "" {
			return nil
		}
		prURL = a.PRURL
	} else if a.BranchName != "" && prURL != a.PRURL {
		head, err := lookupHead(prURL)
		if err == nil && head != "" && head != a.BranchName {
			return nil
		}
	}

	if err := queueManager.RemoveByAgentAndType(agentID, queue.ItemTypePRReady); err != nil {
		return err
	}
	// Defensive: an older Stop hook path could flip a resumed agent to
	// StatusReady + "Agent finished (no PR)" before this fires. Clear
	// it so the queue accurately reflects "waiting on CI" for the new
	// push. (handleAgentStopped no longer takes that path when PRURL
	// is set, but old records in the store may still carry one.)
	if err := queueManager.RemoveByAgentAndType(agentID, queue.ItemTypeIdle); err != nil {
		return err
	}

	return agentStore.Update(agentID, func(a *agent.Agent) {
		a.Status = agent.StatusWaitingCI
		a.PRURL = prURL
		a.CIWaitAt = time.Now()
	})
}

func getPRTitle(prURL string) string {
	// Extract PR number from URL
	parts := strings.Split(prURL, "/")
	if len(parts) < 2 {
		return ""
	}
	prNumber := parts[len(parts)-1]

	cmd := exec.Command("gh", "pr", "view", prNumber, "--json", "title", "-q", ".title")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// agentFailedCmd records that an agent could not start or died abnormally.
//
// Before this existed, agent.StatusFailed was declared, given a display name and
// given a colour — and never assigned anywhere in the codebase. A spawn that
// died during setup (the ~/.claude.json race being the reported case) left its
// record in StatusSpawning with an empty branch_name permanently, animating a
// spinner with no way out; a harness that exited non-zero immediately
// ("Resuming agent... No conversation found to continue", exit 1) left the record
// in StatusRunning behind a dead pane. Neither was distinguishable from healthy
// progress by anything watching, which is exactly how a dead agent went unnoticed
// for seven hours.
//
// The launcher and recovery scripts call this from an EXIT trap, so any failure
// before the agent is registered becomes a visible, reasoned StatusFailed.
func agentFailedCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:    "agent-failed <agent-id>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID := args[0]
			agentStore, err := agent.NewStore(getCurrentSessionID())
			if err != nil {
				return err
			}

			a, err := agentStore.Get(agentID)
			if err != nil {
				// Nothing to mark — the record was already cleaned up. Not an
				// error: this runs from a trap and must never fail the teardown
				// it is reporting on.
				return nil
			}
			// Never overwrite a teardown already in progress; those states are
			// their own truth and are about to remove the record anyway.
			switch a.Status {
			case agent.StatusMerged, agent.StatusCleaningUp, agent.StatusKilling:
				return nil
			}

			if reason == "" {
				reason = "agent failed"
			}
			if err := agentStore.Update(agentID, func(ag *agent.Agent) {
				ag.Status = agent.StatusFailed
				ag.FailureReason = reason
			}); err != nil {
				return err
			}

			// Surface it in the queue too, so the TUI's review list shows it
			// rather than requiring someone to notice a status column.
			if queueManager, err := queue.NewQueue(getCurrentSessionID()); err == nil {
				queueManager.RemoveByAgentAndType(agentID, queue.ItemTypeIdle)
				queueManager.Add(queue.ItemTypeDead, agentID, "Agent failed - kill or restart", reason)
			}

			fmt.Printf("Marked agent %s failed: %s\n", agentID, reason)
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "why the agent failed")
	return cmd
}

func agentStoppedCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "agent-stopped <agent-id>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID := args[0]
			sessionID := getCurrentSessionID()

			agentStore, err := agent.NewStore(sessionID)
			if err != nil {
				return err
			}

			a, err := agentStore.Get(agentID)
			if err != nil {
				return err
			}

			queueManager, err := queue.NewQueue(sessionID)
			if err != nil {
				return err
			}

			return handleAgentStopped(agentStore, queueManager, a)
		},
	}
}

// handleAgentStopped applies the Stop-hook status transition for a single
// agent. Pulled out of agentStoppedCmd for testability.
//
// Claude Code's Stop hook fires at the end of every assistant turn, not on
// process exit. For interactive resumes (CI fix, review reply, merge-conflict
// fix) that means this runs while the launcher script is still blocked on
// `claude --continue` — the script's trailing `ccmux ci-wait` never executes,
// so we have to handle the post-turn transition entirely from here.
//
// The PRURL distinguishes the two real cases:
//   - StatusRunning + no PRURL: a brand-new agent finished its first task
//     without opening a PR. Genuinely "agent finished (no PR)" — mark idle.
//   - StatusRunning + PRURL set: a resumed agent finished a turn. Whether it
//     pushed, retriggered CI (e.g. `gh run rerun`), or did nothing, the right
//     next state is WaitingCI — hand it back to the CI poller so a follow-up
//     pass/fail/timeout is detected. If the "fix" didn't fix anything, the
//     poller's duplicate-failure throttle (see shouldThrottleResume in tui)
//     flips the agent to "needs manual intervention" after ~3 polls.
func handleAgentStopped(agentStore *agent.Store, queueManager *queue.Queue, a *agent.Agent) error {
	switch a.Status {
	case agent.StatusReady:
		// Agent stopped without a PR but was already marked idle
		return nil
	case agent.StatusWaitingReview:
		// Agent made a PR - it's already in queue, nothing to do
		return nil
	case agent.StatusWaitingCI:
		// Agent is waiting for CI - timer will handle resume, nothing to do
		return nil
	case agent.StatusWaitingMergeQueue:
		// Agent's PR is in the merge queue - polling will detect the
		// merge and trigger cleanup, nothing to do here
		return nil
	case agent.StatusRunning:
		if a.PRURL != "" {
			return agentStore.Update(a.ID, func(ag *agent.Agent) {
				ag.Status = agent.StatusWaitingCI
				// Preserve CIWaitAt: it doubles as the "new review feedback
				// since" cutoff for the poller's checkForNewReviews, and the
				// Stop hook fires at EVERY end-of-turn. Resetting it here
				// swallowed review comments that arrived mid-turn — the
				// poller's busy gate defers the new-review resume until the
				// agent goes idle, but by then this reset had moved the
				// cutoff past the comments, so the agent settled back into
				// waiting_review and never picked the feedback up until the
				// next push. Only initialize when it was never set (the
				// PostToolUse `ccmux ci-wait` hook normally sets it on push).
				if ag.CIWaitAt.IsZero() {
					ag.CIWaitAt = time.Now()
				}
			})
		}
		// No PR yet — genuine "agent finished without making a PR".
		if err := agentStore.Update(a.ID, func(ag *agent.Agent) {
			ag.Status = agent.StatusReady
		}); err != nil {
			return err
		}
		if _, err := queueManager.Add(queue.ItemTypeIdle, a.ID, "Agent finished (no PR)", ""); err != nil {
			return err
		}
	}

	return nil
}

// sharePaneOption is the window option that records the agent's shared
// output pane, so `ccmux pane` invocations can find it again and resume /
// cleanup paths can kill it.
const sharePaneOption = "@ccmux_share_pane"

// agentPaneContext locates the calling agent's own tmux pane and window.
// Agent-facing commands (`ccmux pane`, `ccmux reload`) run inside the agent's
// pane (as a child of the harness process), so TMUX_PANE identifies the pane
// directly — this stays correct across resumes, which give the agent a fresh
// window.
type agentPaneContext struct {
	tm        *tmux.Manager
	agentID   string
	sessionID string
	agentPane string
	windowID  string
	workDir   string
}

// getAgentPaneContext resolves the caller's pane; cmdName names the command
// in the error shown when it is run outside an agent.
func getAgentPaneContext(cmdName string) (*agentPaneContext, error) {
	agentID := os.Getenv("CCMUX_AGENT_ID")
	if agentID == "" {
		return nil, fmt.Errorf("CCMUX_AGENT_ID environment variable not set — '%s' is only available inside a ccmux agent", cmdName)
	}
	agentPane := os.Getenv("TMUX_PANE")
	if agentPane == "" {
		return nil, fmt.Errorf("TMUX_PANE environment variable not set — not running inside tmux")
	}

	sessionID := getCurrentSessionID()
	tm := tmux.NewManager(fmt.Sprintf("ccmux-%s", sessionID))

	windowID, err := tm.GetPaneWindowID(agentPane)
	if err != nil {
		return nil, err
	}

	workDir, _ := os.Getwd()

	return &agentPaneContext{tm: tm, agentID: agentID, sessionID: sessionID, agentPane: agentPane, windowID: windowID, workDir: workDir}, nil
}

// currentSharePane returns the recorded share pane ID if it still exists,
// or "" if none is open.
func (c *agentPaneContext) currentSharePane() string {
	paneID, err := c.tm.GetWindowOption(c.windowID, sharePaneOption)
	if err != nil || paneID == "" {
		return ""
	}
	if !c.tm.PaneExists(paneID) {
		return ""
	}
	return paneID
}

func paneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "pane",
		Short:  "Manage the agent's shared output pane (agent-facing)",
		Hidden: true,
	}
	cmd.AddCommand(paneOpenCmd(), paneRunCmd(), paneCloseCmd())
	return cmd
}

func paneOpenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "open [command...]",
		Short:        "Open the shared pane below the agent pane, optionally running a command",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			command := strings.Join(args, " ")

			ctx, err := getAgentPaneContext("ccmux pane")
			if err != nil {
				return err
			}

			if paneID := ctx.currentSharePane(); paneID != "" {
				if command == "" {
					if dead, _ := ctx.tm.IsPaneDead(paneID); dead {
						if err := ctx.tm.RespawnPaneCmd(paneID, ""); err != nil {
							return err
						}
					}
					fmt.Printf("Shared pane %s already open\n", paneID)
					return nil
				}
				if err := ctx.tm.RespawnPaneCmd(paneID, command); err != nil {
					return err
				}
				fmt.Printf("Shared pane %s now running: %s\n", paneID, command)
				return nil
			}

			paneID, err := ctx.tm.SplitPaneBelow(ctx.agentPane, ctx.workDir, command)
			if err != nil {
				return err
			}
			// Keep output visible to the user after the command exits; the
			// pane is cleaned up by `ccmux pane close` or agent teardown.
			ctx.tm.SetPaneRemainOnExit(paneID)
			if err := ctx.tm.SetWindowOption(ctx.windowID, sharePaneOption, paneID); err != nil {
				return err
			}
			if command != "" {
				fmt.Printf("Opened shared pane %s running: %s\n", paneID, command)
			} else {
				fmt.Printf("Opened shared pane %s with an interactive shell\n", paneID)
			}
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func paneRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "run <command...>",
		Short:        "Run a shell command in the shared pane, opening it if needed",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			command := strings.Join(args, " ")

			ctx, err := getAgentPaneContext("ccmux pane")
			if err != nil {
				return err
			}

			paneID := ctx.currentSharePane()
			if paneID == "" {
				paneID, err = ctx.tm.SplitPaneBelow(ctx.agentPane, ctx.workDir, "")
				if err != nil {
					return err
				}
				ctx.tm.SetPaneRemainOnExit(paneID)
				if err := ctx.tm.SetWindowOption(ctx.windowID, sharePaneOption, paneID); err != nil {
					return err
				}
			} else if dead, _ := ctx.tm.IsPaneDead(paneID); dead {
				// The previous program exited; restart the pane with this
				// command directly.
				if err := ctx.tm.RespawnPaneCmd(paneID, command); err != nil {
					return err
				}
				fmt.Printf("Running in shared pane %s: %s\n", paneID, command)
				return nil
			} else if startCmd, _ := ctx.tm.GetPaneStartCommand(paneID); startCmd != "" {
				// The pane is running a program, not a shell — typing into it
				// would go to the program's stdin. (tmux may wrap the start
				// command in quotes; strip them for the error message.)
				return fmt.Errorf("shared pane %s is running %q; use 'ccmux pane open <command>' to replace it, or 'ccmux pane close' first", paneID, strings.Trim(startCmd, `"`))
			}

			if err := ctx.tm.SendKeys(paneID, command); err != nil {
				return err
			}
			fmt.Printf("Running in shared pane %s: %s\n", paneID, command)
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func paneCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "close",
		Short:        "Close the shared pane",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := getAgentPaneContext("ccmux pane")
			if err != nil {
				return err
			}

			paneID := ctx.currentSharePane()
			if paneID == "" {
				ctx.tm.UnsetWindowOption(ctx.windowID, sharePaneOption)
				fmt.Println("No shared pane open")
				return nil
			}

			if err := ctx.tm.KillPane(paneID); err != nil {
				return err
			}
			ctx.tm.UnsetWindowOption(ctx.windowID, sharePaneOption)
			fmt.Printf("Closed shared pane %s\n", paneID)
			return nil
		},
	}
}

// reloadDelay is how long `ccmux reload` waits before respawning the caller's
// pane: enough for the command to return and the harness to record the tool
// result in its transcript, short enough that the agent has not moved on.
const reloadDelay = 2 * time.Second

// reloadCmd lets an agent restart its own harness in place — resuming the
// conversation — so configuration that only loads at startup (MCP servers,
// tools, hooks, settings) takes effect without a human restarting it from the
// TUI. The agent's worktree, branch, tmux pane and shared pane all survive;
// only the harness process is replaced.
func reloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "reload [note...]",
		Short:        "Restart your own harness in place, resuming this conversation (agent-facing)",
		Hidden:       true,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			note := strings.TrimSpace(strings.Join(args, " "))

			ctx, err := getAgentPaneContext("ccmux reload")
			if err != nil {
				return err
			}
			agentStore, err := agent.NewStore(ctx.sessionID)
			if err != nil {
				return err
			}
			a, err := agentStore.Get(ctx.agentID)
			if err != nil {
				return fmt.Errorf("agent %s is not registered in this session: %w", ctx.agentID, err)
			}
			if reason := reloadRefusalReason(a, ctx.agentPane, ctx.windowID); reason != "" {
				return fmt.Errorf("cannot reload: %s", reason)
			}

			var projectStore *project.Store
			if ps, err := project.NewStore(); err == nil {
				projectStore = ps
			}
			h := harness.Parse(a.Harness)
			scriptPath, err := writeReloadScript(a.ID, a.WorktreePath, a.BaseBranch, a.Task, note, h, agentDraftPRs(projectStore, a.ProjectName))
			if err != nil {
				return fmt.Errorf("failed to write reload script: %w", err)
			}

			if err := ctx.tm.RespawnPaneDeferred(ctx.agentPane, "bash "+scriptPath, reloadDelay); err != nil {
				return err
			}

			fmt.Printf("Reloading %s for agent %s in pane %s in %s.\n", h.DisplayName(), a.ID, ctx.agentPane, reloadDelay)
			fmt.Printf("The harness will be restarted with: %s\n", h.ContinueWithPromptCommand())
			if note != "" {
				fmt.Printf("Your note will be delivered after the reload: %s\n", note)
			}
			fmt.Println("End your turn now — anything you start before the reload will be interrupted.")
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// reloadRefusalReason explains why `ccmux reload` must not respawn the caller's
// pane, or returns "" if it is safe. The registry records which pane the
// agent's harness lives in; if the caller is somewhere else (typically the
// agent's shared output pane, whose shell inherited CCMUX_AGENT_ID), respawning
// TMUX_PANE would kill the wrong program and leave the real harness untouched.
// Older records carry only a window, so fall back to matching on that.
func reloadRefusalReason(a *agent.Agent, tmuxPane, windowID string) string {
	switch {
	case a.TmuxPane != "" && a.TmuxPane != tmuxPane:
		return fmt.Sprintf("you are in pane %s but your harness runs in pane %s; run ccmux reload from your own pane, not the shared pane", tmuxPane, a.TmuxPane)
	case a.TmuxPane == "" && a.TmuxWindow != "" && a.TmuxWindow != windowID:
		return fmt.Sprintf("you are in window %s but your harness runs in window %s", windowID, a.TmuxWindow)
	}
	return ""
}

// reloadPrompt is the first message the reloaded harness receives. It is what
// makes the restart self-explanatory to the agent: Claude Code resumes the
// conversation so it needs only the reason and the note; Codex starts a fresh
// session (the system prompt restates the task) so it also needs to be told to
// re-orient from git.
func reloadPrompt(note string) string {
	prompt := "Your harness session was just reloaded at your own request (ccmux reload), so newly configured MCP servers, tools, hooks and settings are now loaded. " +
		"If your conversation history is visible, continue where you left off — the result of the reload call itself may be missing. " +
		"If it is not visible, review your progress with git log, git status and git diff first."
	if note != "" {
		prompt += "\n\nYour note to yourself before reloading:\n" + note
	}
	return prompt
}

// writeReloadScript writes the launcher that `ccmux reload` respawns the
// agent's pane with. It mirrors the restart script (system prompt with the
// original task, CLAUDE.md and project prompts, exit capture) but resumes with
// an explanatory message and, unlike a restart, keeps the telemetry export so
// the reloaded agent's cost still reaches the TUI.
func writeReloadScript(agentID, worktreePath, baseBranch, task, note string, h harness.Type, draftPRs bool) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+"-reload.sh")

	draftPRsFlag := "0"
	if draftPRs {
		draftPRsFlag = "1"
	}

	sq := shellutil.Quote
	script := fmt.Sprintf(`#!/bin/bash
set -e

AGENT_ID=%s
WORKTREE_PATH=%s
BASE_BRANCH=%s
TASK=%s
HARNESS=%s
DRAFT_PRS=%s
PROMPT=%s

cd "$WORKTREE_PATH"

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
RESET="\033[0m"
echo -e "${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Reloading $HARNESS at the agent's request...${RESET}"
echo ""

export CCMUX_AGENT_ID="$AGENT_ID"
unset CLAUDECODE

`+harness.TelemetryEnvBlock+`
PR_BASE_BRANCH="${BASE_BRANCH#origin/}"

PR_DRAFT_FLAG=""
PR_DRAFT_NOTE="IMPORTANT: this project opens pull requests ready for review — do NOT add a --draft flag."
if [ "$DRAFT_PRS" = "1" ]; then
  PR_DRAFT_FLAG="--draft "
  PR_DRAFT_NOTE="This project opens pull requests as drafts — keep the --draft flag."
fi

SYSTEM_PROMPT="You are working on a task as part of the ccmux agent system. Environment variable CCMUX_AGENT_ID=$AGENT_ID is set for hook integration.

IMPORTANT: You reloaded your own harness session with ccmux reload. If your conversation history is not visible, review your progress so far with git log, git status and git diff, then continue where you left off.

The original task was:
$TASK

When done with your task, commit your work and create a PR with:
    gh pr create ${PR_DRAFT_FLAG}--base $PR_BASE_BRANCH --title \"...\" --body \"...\"
${PR_DRAFT_NOTE}

`+sysprompt.SharePaneDoc+`

`+sysprompt.PeerAgentsDoc+`

`+sysprompt.ReloadDoc+`"

CLAUDE_MD_PATH="$HOME/.claude/CLAUDE.md"
if [ -f "$CLAUDE_MD_PATH" ]; then
  CLAUDE_MD_CONTENT=$(cat "$CLAUDE_MD_PATH")
  SYSTEM_PROMPT="${SYSTEM_PROMPT}

${CLAUDE_MD_CONTENT}"
fi

PROMPTS_FILE=%s
if [ -f "$PROMPTS_FILE" ]; then
  PROMPTS_CONTENT=$(cat "$PROMPTS_FILE")
  SYSTEM_PROMPT="${SYSTEM_PROMPT}

${PROMPTS_CONTENT}"
fi

`+harness.SystemPromptFileBlock+`
`+harness.ExitCapturePrologue+`%s
`+harness.ExitCaptureCapture+harness.ExitCaptureReport, sq(agentID), sq(worktreePath), sq(baseBranch), sq(task), sq(string(h)), sq(draftPRsFlag), sq(reloadPrompt(note)), sq(promptsFilePath(agentID)), h.ContinueWithPromptCommand())

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

// agentsCmd exposes the session's agent registry to the agents themselves so
// they can find and message one another. Like `ccmux pane`, it only makes
// sense from inside an agent (CCMUX_AGENT_ID identifies the caller).
func agentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "agents",
		Short:  "List and message the other agents in this session (agent-facing)",
		Hidden: true,
	}
	cmd.AddCommand(agentsListCmd(), agentsSendCmd())
	return cmd
}

// peerMessagePrefix is prepended to every agent-to-agent message so the
// recipient knows who sent it and how to reply.
func peerMessagePrefix(senderID string) string {
	return fmt.Sprintf("[message from ccmux agent %s] ", senderID)
}

// taskPreviewRunes bounds the task column of `ccmux agents list`. Task briefs
// can run to several paragraphs; the opening sentence is what an agent needs
// to pick a peer, and the full text is one --full away.
const taskPreviewRunes = 160

// formatAgentRows renders the registry as one line per agent for `ccmux
// agents list`: id, status, project, branch, PR, and the task (truncated to a
// preview unless full is set). The caller's own row is marked so it can tell
// itself apart from its peers.
func formatAgentRows(agents []*agent.Agent, selfID string, full bool) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tPROJECT\tBRANCH\tPR\tTASK")
	for _, a := range agents {
		id := a.ID
		if a.ID == selfID {
			id += " (you)"
		}
		pr := a.PRURL
		if pr == "" {
			pr = "-"
		}
		task := strings.Join(strings.Fields(a.Task), " ")
		if r := []rune(task); !full && len(r) > taskPreviewRunes {
			task = string(r[:taskPreviewRunes]) + "…"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", id, a.Status.DisplayName(), orDash(a.ProjectName), orDash(a.BranchName), pr, task)
	}
	w.Flush()
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// peerUndeliverableReason explains why a message cannot be typed into the
// target agent's pane right now, or returns "" if delivery is fine. paneAlive
// reports whether the agent's pane exists with a live process, and
// paneStartCmd is what that pane was spawned with — a recovered agent parked
// in a placeholder banner has no harness reading its input, so a message
// typed there would vanish into a sleeping bash script.
func peerUndeliverableReason(a *agent.Agent, paneAlive bool, paneStartCmd string) string {
	switch a.Status {
	case agent.StatusSpawning:
		return "it is still spawning; try again shortly"
	case agent.StatusCleaningUp, agent.StatusKilling, agent.StatusMerged, agent.StatusFailed:
		return fmt.Sprintf("it is %s and no longer running", a.Status.DisplayName())
	case agent.StatusWaitingCI, agent.StatusWaitingMergeQueue:
		return fmt.Sprintf("it is %s and not running a harness", a.Status.DisplayName())
	}
	if !paneAlive {
		return "its tmux pane is gone or dead"
	}
	if strings.Contains(paneStartCmd, "-placeholder.sh") {
		return fmt.Sprintf("it is parked after a session recovery (%s); its harness is not running until the user resumes it from the TUI", a.Status.DisplayName())
	}
	return ""
}

// agentPaneTarget returns the tmux target for an agent's own pane: the
// recorded pane ID when present, else the window (whose active pane may be
// the agent's shared output pane, so the pane ID is preferred).
func agentPaneTarget(a *agent.Agent) string {
	if a.TmuxPane != "" {
		return a.TmuxPane
	}
	return a.TmuxWindow
}

func requireAgentID() (string, error) {
	id := os.Getenv("CCMUX_AGENT_ID")
	if id == "" {
		return "", fmt.Errorf("CCMUX_AGENT_ID environment variable not set — 'ccmux agents' is only available inside a ccmux agent")
	}
	return id, nil
}

func agentsListCmd() *cobra.Command {
	var full bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List the agents in this session",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selfID, err := requireAgentID()
			if err != nil {
				return err
			}
			agentStore, err := agent.NewStore(getCurrentSessionID())
			if err != nil {
				return err
			}
			agents, err := agentStore.List()
			if err != nil {
				return err
			}
			if len(agents) == 0 {
				fmt.Println("No agents in this session")
				return nil
			}
			fmt.Print(formatAgentRows(agents, selfID, full))
			return nil
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "Show each agent's complete task instead of a preview")
	return cmd
}

func agentsSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "send <agent-id> <message...>",
		Short:        "Deliver a message to another agent's prompt",
		Args:         cobra.MinimumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selfID, err := requireAgentID()
			if err != nil {
				return err
			}
			targetID := args[0]
			message := strings.TrimSpace(strings.Join(args[1:], " "))
			if message == "" {
				return fmt.Errorf("message is empty")
			}
			if targetID == selfID {
				return fmt.Errorf("agent %s is you; pick another agent from 'ccmux agents list'", targetID)
			}

			sessionID := getCurrentSessionID()
			agentStore, err := agent.NewStore(sessionID)
			if err != nil {
				return err
			}
			target, err := agentStore.Get(targetID)
			if err != nil {
				return fmt.Errorf("no agent %s in this session (see 'ccmux agents list'): %w", targetID, err)
			}

			tm := tmux.NewManager(fmt.Sprintf("ccmux-%s", sessionID))
			paneTarget := agentPaneTarget(target)
			paneAlive := paneTarget != "" && tm.PaneExists(paneTarget)
			if paneAlive {
				if dead, _ := tm.IsPaneDead(paneTarget); dead {
					paneAlive = false
				}
			}
			startCmd := ""
			if paneAlive {
				startCmd, _ = tm.GetPaneStartCommand(paneTarget)
			}
			if reason := peerUndeliverableReason(target, paneAlive, startCmd); reason != "" {
				return fmt.Errorf("cannot message agent %s: %s", targetID, reason)
			}

			if err := tm.SendText(paneTarget, peerMessagePrefix(selfID)+message); err != nil {
				return err
			}

			// Mirror what the TUI does when the user messages an idle agent:
			// it has new input now, so it is running again and no longer
			// needs attention in the queue.
			if target.Status == agent.StatusReady {
				agentStore.Update(targetID, func(ag *agent.Agent) {
					ag.Status = agent.StatusRunning
				})
				if q, err := queue.NewQueue(sessionID); err == nil {
					q.RemoveByAgent(targetID)
				}
			}

			fmt.Printf("Sent message to agent %s\n", targetID)
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func focusCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "focus <agent-id>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID := args[0]
			sessionID := getCurrentSessionID()

			agentStore, err := agent.NewStore(sessionID)
			if err != nil {
				return err
			}

			a, err := agentStore.Get(agentID)
			if err != nil {
				return err
			}

			tmuxSessionName := fmt.Sprintf("ccmux-%s", sessionID)
			tmuxManager := tmux.NewManager(tmuxSessionName)
			return tmuxManager.SelectWindow(a.TmuxWindow)
		},
	}
}

func cleanupCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "cleanup <agent-id>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doCleanup(args[0], "Cleaned up", false)
		},
	}
}

func killCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "kill <agent-id>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doCleanup(args[0], "Killed", true)
		},
	}
}

func doCleanup(agentID, action string, closePR bool) error {
	sessionID := getCurrentSessionID()

	agentStore, err := agent.NewStore(sessionID)
	if err != nil {
		return err
	}

	a, err := agentStore.Get(agentID)
	if err != nil {
		return err
	}

	dcStore, err := dailycost.NewStore()
	if err == nil {
		costs := tui.GetAgentDailyCosts(a.WorktreePath)
		if len(costs) > 0 {
			dcStore.AddCosts(costs)
		}
	}

	// A kill abandons the agent's work, so close any PR it opened. Other
	// callers (post-merge / post-reject cleanup) already handled the PR and
	// must not touch it again — `gh pr close` errors on a merged or
	// already-closed PR.
	if closePR && a.PRURL != "" {
		closeAgentPR(a.PRURL)
	}

	runTeardownScript(a.ProjectName, a.WorktreePath, agentID)

	tmuxSessionName := fmt.Sprintf("ccmux-%s", sessionID)
	tmuxManager := tmux.NewManager(tmuxSessionName)
	tmuxManager.KillWindow(a.TmuxWindow)

	removeErr := removeAgentWorktree(a)

	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
		removeLauncherFiles(launcherDir, agentID)
	}

	// Only forget the agent once its worktree is actually gone.
	//
	// This used to be an unconditional Delete while removal failures were a
	// stderr warning — and the TUI runs `ccmux cleanup` detached, discarding
	// output, so nobody ever saw it. Worse, a GetRepoRoot error skipped removal
	// entirely. Either way the directory survived on disk with no registry entry
	// pointing at it: invisible to every later cleanup path, permanently.
	//
	// Keeping the record, parked in StatusFailed with the reason attached, means
	// the leftover stays visible in the TUI and `ccmux prune` can finish the job.
	// StatusFailed is excluded from resource polling (see isPollable), so a
	// parked entry costs nothing while it waits.
	if removeErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to remove worktree %s: %v\n", a.WorktreePath, removeErr)
		fmt.Fprintf(os.Stderr, "Keeping agent %s registered so the leftover stays visible; run `ccmux prune` to finish.\n", agentID)
		agentStore.Update(agentID, func(ag *agent.Agent) {
			ag.Status = agent.StatusFailed
			ag.FailureReason = fmt.Sprintf("teardown incomplete: %v", removeErr)
		})
		return fmt.Errorf("failed to remove worktree %s: %w", a.WorktreePath, removeErr)
	}

	agentStore.Delete(agentID)

	fmt.Printf("%s agent %s\n", action, agentID)
	return nil
}

// removeAgentWorktree deletes an agent's worktree directory and local branch.
// It returns nil when the directory is gone by the time it finishes — including
// when it was already gone before the call, which is the common case for an
// agent whose teardown is being retried.
func removeAgentWorktree(a *agent.Agent) error {
	if a.WorktreePath == "" {
		return nil
	}
	if !dirExists(a.WorktreePath) {
		return nil
	}

	repoRoot, err := project.GetRepoRoot(a.WorktreePath)
	if err != nil {
		// Previously this returned early and skipped removal altogether, which
		// is how directories were orphaned. Fall back to the project record's
		// repo path so a detached or damaged worktree can still be cleaned up.
		repoRoot = projectRepoRoot(a.ProjectName)
	}

	wtManager := worktree.NewManager(repoRoot)
	os.RemoveAll(filepath.Join(a.WorktreePath, ".claude"))
	removeErr := wtManager.Remove(a.WorktreePath)
	if removeErr == nil || !dirExists(a.WorktreePath) {
		wtManager.DeleteBranch(a.BranchName)
		return nil
	}

	// `git worktree remove` / `rift remove` can fail for reasons a plain
	// directory delete handles fine: the worktree was never registered, the
	// admin files are damaged, or rift is not on PATH any more. Since the caller
	// has already decided this agent is finished, fall back to removing the
	// directory outright rather than leaking it.
	if fallbackErr := os.RemoveAll(a.WorktreePath); fallbackErr != nil {
		return fmt.Errorf("%v (and direct removal failed: %v)", removeErr, fallbackErr)
	}
	if dirExists(a.WorktreePath) {
		return removeErr
	}
	// Tidy the now-dangling `git worktree` administrative entry, if any.
	pruneGitWorktrees(repoRoot)
	wtManager.DeleteBranch(a.BranchName)
	return nil
}

// projectRepoRoot resolves a project name to its repo path, returning "" when
// the project is unknown.
func projectRepoRoot(projectName string) string {
	if projectName == "" {
		return ""
	}
	projectStore, err := project.NewStore()
	if err != nil {
		return ""
	}
	proj, err := projectStore.Get(projectName)
	if err != nil {
		return ""
	}
	return proj.EffectivePath()
}

// pruneGitWorktrees drops administrative entries for worktree directories that
// no longer exist. Nothing in ccmux called `git worktree prune` before, so a
// removal that bypassed `git worktree remove` left git believing the worktree
// was still checked out — which then blocks reusing the branch.
func pruneGitWorktrees(repoRoot string) {
	if repoRoot == "" {
		return
	}
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = repoRoot
	cmd.Run()
}

// closeAgentPR closes the agent's PR and deletes the remote branch.
//
// Best-effort: errors are logged but never fail the kill — the PR might
// already be closed, the user might lack permission, or `gh` might not be
// authenticated. None of those should block tearing down the agent's
// worktree, tmux window, and local branch.
func closeAgentPR(prURL string) {
	cmd := exec.Command("gh", "pr", "close", prURL, "--delete-branch")
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to close PR %s: %s: %v\n", prURL, strings.TrimSpace(string(output)), err)
		return
	}
	fmt.Printf("Closed PR %s\n", prURL)
}

func runTeardownScript(projectName, worktreePath, agentID string) {
	if projectName == "" {
		return
	}
	projectStore, err := project.NewStore()
	if err != nil {
		return
	}
	proj, err := projectStore.Get(projectName)
	if err != nil || proj.TeardownScript == "" {
		return
	}
	if _, err := os.Stat(proj.TeardownScript); err != nil {
		return
	}
	fmt.Printf("Running teardown script: %s\n", proj.TeardownScript)
	cmd := exec.Command("bash", proj.TeardownScript)
	cmd.Dir = worktreePath
	cmd.Env = append(os.Environ(),
		"CCMUX_WORKTREE_PATH="+worktreePath,
		"CCMUX_AGENT_ID="+agentID,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: teardown script failed: %v\n", err)
	}
}

func killSessionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kill-session [session-id]",
		Short: "Kill an entire ccmux session and all its agents",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := defaultSessionID
			if len(args) > 0 {
				sessionID = args[0]
			}

			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}

			agentStore, err := agent.NewStore(sessionID)
			if err != nil {
				return err
			}

			launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")

			agents, _ := agentStore.List()
			for _, a := range agents {
				repoRoot, err := project.GetRepoRoot(a.WorktreePath)
				if err == nil {
					wtManager := worktree.NewManager(repoRoot)
					os.RemoveAll(filepath.Join(a.WorktreePath, ".claude"))
					wtManager.Remove(a.WorktreePath)
					wtManager.DeleteBranch(a.BranchName)
				}
				removeLauncherFiles(launcherDir, a.ID)
			}

			sessionDir := filepath.Join(homeDir, ".ccmux", "sessions", sessionID)
			os.RemoveAll(sessionDir)

			tmuxSessionName := fmt.Sprintf("ccmux-%s", sessionID)
			tmuxManager := tmux.NewManager(tmuxSessionName)

			if tmuxManager.SessionExists() {
				if err := tmuxManager.KillSession(); err != nil {
					return err
				}
			}

			fmt.Printf("Killed session %s\n", tmuxSessionName)
			return nil
		},
	}
}

func recoverOrphanedAgents(sessionID string, tmuxManager *tmux.Manager, homeDir string) (bool, error) {
	agentStore, err := agent.NewStore(sessionID)
	if err != nil {
		return false, err
	}

	agents, err := agentStore.List()
	if err != nil || len(agents) == 0 {
		return false, nil
	}

	type recoverable struct {
		agent      *agent.Agent
		scriptPath string
		kind       string // "resume" or "placeholder"
	}

	var toRecover []recoverable
	var toCleanup []*agent.Agent
	// toRemove holds whole agents rather than IDs so their worktrees can be
	// removed too, not just their registry entries.
	var toRemove []*agent.Agent
	// Agents whose waiting_review status was withdrawn for want of a PR URL;
	// their stale pr_ready queue items are dropped alongside.
	var demoted []string
	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")

	// projectStore lets recovery honour each agent's project-level
	// settings (e.g. draft PRs). A failure here is non-fatal — callers
	// fall back to the default behaviour.
	projectStore, _ := project.NewStore()

	for _, a := range agents {
		worktreeExists := a.WorktreePath != "" && dirExists(a.WorktreePath)

		switch {
		case (a.Status == agent.StatusCleaningUp || a.Status == agent.StatusKilling) && worktreeExists:
			toCleanup = append(toCleanup, a)

		case (a.Status == agent.StatusRunning || a.Status == agent.StatusSpawning) && worktreeExists:
			scriptPath, err := writeRecoveryScript(a.ID, a.WorktreePath, a.BaseBranch, sessionID, a.Task, harness.Parse(a.Harness), agentDraftPRs(projectStore, a.ProjectName))
			if err != nil {
				logging.Log("recovery: failed to write recovery script for %s: %v", a.ID, err)
				continue
			}
			toRecover = append(toRecover, recoverable{agent: a, scriptPath: scriptPath, kind: "resume"})

		case (a.Status == agent.StatusReady || a.Status == agent.StatusWaitingReview) && worktreeExists:
			// A waiting_review record with no PR URL is a stale claim: park it
			// (and re-record it) as idle so neither the pane banner nor the
			// quick-action queue advertises a PR that does not exist.
			parkedStatus := placeholderStatusFor(a)
			if parkedStatus != a.Status {
				logging.Log("recovery: agent %s was %s with no PR recorded — parking it as %s", a.ID, a.Status, parkedStatus)
				agentStore.Update(a.ID, func(ag *agent.Agent) {
					ag.Status = parkedStatus
				})
				a.Status = parkedStatus
				demoted = append(demoted, a.ID)
			}
			scriptPath, err := writePlaceholderScript(a.ID, a.WorktreePath, a.Task, a.PRURL, parkedStatus)
			if err != nil {
				logging.Log("recovery: failed to write placeholder script for %s: %v", a.ID, err)
				continue
			}
			toRecover = append(toRecover, recoverable{agent: a, scriptPath: scriptPath, kind: "placeholder"})

		case a.Status == agent.StatusWaitingCI && worktreeExists:
			scriptPath, err := writeCIWaitPlaceholderScript(a.ID, a.WorktreePath, a.Task)
			if err != nil {
				logging.Log("recovery: failed to write CI wait placeholder script for %s: %v", a.ID, err)
				continue
			}
			toRecover = append(toRecover, recoverable{agent: a, scriptPath: scriptPath, kind: "placeholder"})

		case a.Status == agent.StatusWaitingMergeQueue && worktreeExists:
			scriptPath, err := writeMergeQueuePlaceholderScript(a.ID, a.WorktreePath, a.Task)
			if err != nil {
				logging.Log("recovery: failed to write merge-queue placeholder script for %s: %v", a.ID, err)
				continue
			}
			toRecover = append(toRecover, recoverable{agent: a, scriptPath: scriptPath, kind: "placeholder"})

		default:
			toRemove = append(toRemove, a)
		}
	}

	for _, a := range toCleanup {
		logging.Log("recovery: cleaning up stale agent %s", a.ID)
		if err := removeAgentWorktree(a); err != nil {
			logging.Log("recovery: failed to remove worktree for %s: %v", a.ID, err)
		}
		removeLauncherFiles(launcherDir, a.ID)
		agentStore.Delete(a.ID)
	}

	for _, a := range toRemove {
		id := a.ID
		logging.Log("recovery: removing orphaned agent record %s", id)

		// Remove the worktree before forgetting the record.
		//
		// This branch catches StatusMerged, StatusFailed and any status whose
		// worktree no longer exists — and it used to delete only the registry
		// entry. An agent whose post-merge cleanup died mid-flight was therefore
		// erased from the registry on the next cold start while its directory
		// stayed on disk forever, with nothing left that knew about it. That is
		// half of how 25 worktrees accumulated for 8 live agents.
		//
		// A worktree holding unpushed work is left alone and reported: recovery
		// runs unattended at startup, so it must never be the thing that
		// destroys an agent's only copy of its work.
		if unsaved := worktree.Inspect(a.WorktreePath); !unsaved.IsClean() {
			logging.Log("recovery: keeping worktree %s for agent %s (%s)", a.WorktreePath, id, unsaved.Summary())
			fmt.Fprintf(os.Stderr, "Note: keeping worktree %s (%s); run `ccmux prune` to review.\n",
				a.WorktreePath, unsaved.Summary())
		} else if err := removeAgentWorktree(a); err != nil {
			logging.Log("recovery: failed to remove worktree for %s: %v", id, err)
		}

		removeLauncherFiles(launcherDir, id)
		agentStore.Delete(id)
	}

	if len(toRecover) == 0 {
		return false, nil
	}

	logging.Log("recovery: recovering %d agents", len(toRecover))

	exePath, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("failed to get executable path: %w", err)
	}
	tuiCmd := fmt.Sprintf("%s %s", exePath, sessionID)
	if err := tmuxManager.CreateSessionWithCommand(homeDir, tuiCmd); err != nil {
		return false, err
	}

	for _, r := range toRecover {
		windowID, paneID, err := tmuxManager.CreateWindow(r.agent.WorktreePath, "bash "+r.scriptPath, r.agent.ID[:8])
		if err != nil {
			logging.Log("recovery: failed to create window for %s: %v", r.agent.ID, err)
			continue
		}
		agentStore.Update(r.agent.ID, func(a *agent.Agent) {
			a.TmuxWindow = windowID
			a.TmuxPane = paneID
			if r.kind == "resume" {
				a.Status = agent.StatusRunning
			}
		})
		logging.Log("recovery: agent %s recovered (%s) -> window %s", r.agent.ID, r.kind, windowID)
	}

	queueManager, err := queue.NewQueue(sessionID)
	if err != nil {
		logging.Log("recovery: failed to create queue manager: %v", err)
	} else {
		items, _ := queueManager.List()
		activeAgents := make(map[string]bool)
		recoveredAgents, _ := agentStore.List()
		for _, a := range recoveredAgents {
			activeAgents[a.ID] = true
		}
		for _, item := range items {
			if !activeAgents[item.AgentID] {
				queueManager.Remove(item.ID)
			}
		}
		// A "PR ready" entry for an agent we just demoted points at a PR that
		// was never recorded — leaving it would keep offering the operator a
		// review that cannot be opened.
		for _, id := range demoted {
			queueManager.RemoveByAgentAndType(id, queue.ItemTypePRReady)
		}
	}

	return true, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// agentDraftPRs resolves the draft-PR setting for an agent's project,
// defaulting to true when the store is unavailable or the project is
// unknown (e.g. it was removed after the agent was spawned).
func agentDraftPRs(store *project.Store, projectName string) bool {
	if store == nil || projectName == "" {
		return true
	}
	proj, err := store.Get(projectName)
	if err != nil {
		return true
	}
	return proj.EffectiveDraftPRs()
}

func writeRecoveryScript(agentID, worktreePath, baseBranch, sessionID, task string, h harness.Type, draftPRs bool) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+"-recovery.sh")

	draftPRsFlag := "0"
	if draftPRs {
		draftPRsFlag = "1"
	}

	sq := shellutil.Quote
	script := fmt.Sprintf(`#!/bin/bash
set -e

AGENT_ID=%s
WORKTREE_PATH=%s
BASE_BRANCH=%s
SESSION_ID=%s
TASK=%s
HARNESS=%s
DRAFT_PRS=%s

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
YELLOW="\033[38;5;226m"
RESET="\033[0m"
echo -e "${YELLOW}RECOVERING${RESET} ${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Resuming after session loss...${RESET}"
echo ""

cd "$WORKTREE_PATH"

# Reinstall Claude Code hooks (Claude harness only).
if [ "$HARNESS" = "claude" ]; then
mkdir -p .claude/hooks

cat > .claude/hooks/stop.sh << 'HOOKEOF'
#!/bin/bash
ccmux agent-stopped "$CCMUX_AGENT_ID"
HOOKEOF
chmod +x .claude/hooks/stop.sh

cat > .claude/hooks/post_tool_use.sh << 'HOOKEOF'
`+postToolUseHookScript+`HOOKEOF
chmod +x .claude/hooks/post_tool_use.sh

CCMUX_STOP_CMD="CCMUX_AGENT_ID=$AGENT_ID $WORKTREE_PATH/.claude/hooks/stop.sh"
CCMUX_PTU_CMD="CCMUX_AGENT_ID=$AGENT_ID $WORKTREE_PATH/.claude/hooks/post_tool_use.sh"

if [ -f .claude/settings.json ]; then
  EXISTING=$(cat .claude/settings.json)
  CLEANED=$(echo "$EXISTING" | jq '
    .hooks.Stop = [((.hooks.Stop // [])[]) | select((.hooks // []) | any(.command | contains("ccmux agent-stopped") or contains("/.claude/hooks/stop.sh")) | not)]
    | .hooks.PostToolUse = [((.hooks.PostToolUse // [])[]) | select((.hooks // []) | any(.command | contains("/.claude/hooks/post_tool_use.sh")) | not)]
  ')
  echo "$CLEANED" | jq \
    --arg stop_cmd "$CCMUX_STOP_CMD" \
    --arg ptu_cmd "$CCMUX_PTU_CMD" \
    '.hooks.Stop = ((.hooks.Stop // []) + [{hooks: [{type: "command", command: $stop_cmd}]}])
     | .hooks.PostToolUse = ((.hooks.PostToolUse // []) + [{matcher: "Bash", hooks: [{type: "command", command: $ptu_cmd}]}])' \
    > .claude/settings.json
else
  jq -n \
    --arg stop_cmd "$CCMUX_STOP_CMD" \
    --arg ptu_cmd "$CCMUX_PTU_CMD" \
    '{hooks: {Stop: [{hooks: [{type: "command", command: $stop_cmd}]}], PostToolUse: [{matcher: "Bash", hooks: [{type: "command", command: $ptu_cmd}]}]}}' \
    > .claude/settings.json
fi

# Prevent ccmux hook files from being committed
GIT_COMMON_DIR=$(git rev-parse --git-common-dir)
EXCLUDE_FILE="$GIT_COMMON_DIR/info/exclude"
mkdir -p "$GIT_COMMON_DIR/info"
STOP_SH_TRACKED=$(git ls-files .claude/hooks/stop.sh)
PTU_SH_TRACKED=$(git ls-files .claude/hooks/post_tool_use.sh)
SETTINGS_TRACKED=$(git ls-files .claude/settings.json)

if [ -z "$STOP_SH_TRACKED" ]; then
  grep -qxF '.claude/hooks/stop.sh' "$EXCLUDE_FILE" 2>/dev/null || echo '.claude/hooks/stop.sh' >> "$EXCLUDE_FILE"
else
  git update-index --assume-unchanged .claude/hooks/stop.sh
fi

if [ -z "$PTU_SH_TRACKED" ]; then
  grep -qxF '.claude/hooks/post_tool_use.sh' "$EXCLUDE_FILE" 2>/dev/null || echo '.claude/hooks/post_tool_use.sh' >> "$EXCLUDE_FILE"
else
  git update-index --assume-unchanged .claude/hooks/post_tool_use.sh
fi

if [ -z "$SETTINGS_TRACKED" ]; then
  grep -qxF '.claude/settings.json' "$EXCLUDE_FILE" 2>/dev/null || echo '.claude/settings.json' >> "$EXCLUDE_FILE"
else
  git update-index --assume-unchanged .claude/settings.json
fi
fi

if [ "$HARNESS" = "codex" ]; then
  echo "→ Pre-trusting worktree directory in Codex..."
  ccmux trust-codex-project "$WORKTREE_PATH"
  echo "✓ Directory trusted"
  echo ""
fi

export CCMUX_AGENT_ID="$AGENT_ID"
unset CLAUDECODE

echo -e "${DIM}Resuming $HARNESS...${RESET}"
echo ""

PR_BASE_BRANCH="${BASE_BRANCH#origin/}"

PR_DRAFT_FLAG=""
PR_DRAFT_NOTE="IMPORTANT: this project opens pull requests ready for review — do NOT add a --draft flag."
if [ "$DRAFT_PRS" = "1" ]; then
  PR_DRAFT_FLAG="--draft "
  PR_DRAFT_NOTE="This project opens pull requests as drafts — keep the --draft flag."
fi

SYSTEM_PROMPT="You are working on a task as part of the ccmux agent system. Environment variable CCMUX_AGENT_ID=$AGENT_ID is set for hook integration.

IMPORTANT: Your previous session was interrupted by a session loss (e.g., tmux crash or reboot). Review your progress so far with git log, git status and git diff, then continue where you left off.

The original task was:
$TASK

When done with your task, commit your work and create a PR with:
    gh pr create ${PR_DRAFT_FLAG}--base $PR_BASE_BRANCH --title \"...\" --body \"...\"
${PR_DRAFT_NOTE}

`+sysprompt.SharePaneDoc+`

`+sysprompt.PeerAgentsDoc+`

`+sysprompt.ReloadDoc+`"

CLAUDE_MD_PATH="$HOME/.claude/CLAUDE.md"
if [ -f "$CLAUDE_MD_PATH" ]; then
  CLAUDE_MD_CONTENT=$(cat "$CLAUDE_MD_PATH")
  SYSTEM_PROMPT="${SYSTEM_PROMPT}

${CLAUDE_MD_CONTENT}"
fi

PROMPTS_FILE=%s
if [ -f "$PROMPTS_FILE" ]; then
  PROMPTS_CONTENT=$(cat "$PROMPTS_FILE")
  SYSTEM_PROMPT="${SYSTEM_PROMPT}

${PROMPTS_CONTENT}"
fi

`+harness.SystemPromptFileBlock+`
`+harness.ExitCapturePrologue+`%s
`+harness.ExitCaptureCapture+harness.ExitCaptureReport, sq(agentID), sq(worktreePath), sq(baseBranch), sq(sessionID), sq(task), sq(string(h)), sq(draftPRsFlag), sq(promptsFilePath(agentID)), h.ContinueCommand())

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

// placeholderStatusFor resolves the status a recovered agent should actually be
// parked under.
//
// StatusWaitingReview is a claim about the outside world — "this agent has a PR
// open and a human needs to look at it". An agent carrying that status with no
// PR URL recorded cannot back the claim up: nothing in ccmux can show, poll, or
// merge a PR it has no URL for, so the operator is sent to review a PR that
// does not exist. Demote it to StatusReady (idle) instead, which is what an
// agent with no PR is.
func placeholderStatusFor(a *agent.Agent) agent.Status {
	if a.Status == agent.StatusWaitingReview && a.PRURL == "" {
		return agent.StatusReady
	}
	return a.Status
}

// writePlaceholderScript parks a recovered agent's window with a banner
// describing the state it was recovered in.
//
// The banner must follow the status. Recovery routes both StatusReady and
// StatusWaitingReview agents here, and this script used to hardcode "● waiting
// for PR review / This agent has a PR up for review" for both — so after every
// ccmux restart, every idle agent's pane claimed a PR that had never been
// opened, and the operator had no way to tell a genuinely review-ready agent
// from one that had simply finished its turn.
func writePlaceholderScript(agentID, worktreePath, task, prURL string, status agent.Status) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+"-placeholder.sh")

	// Only a StatusWaitingReview agent has a PR waiting on a human. Everything
	// else routed here is idle — including an agent that holds a PR URL but was
	// flipped back to StatusReady for manual intervention (a throttled CI-fix
	// loop, say), which is emphatically not "ready for review".
	stateLines := `echo -e "${GREEN}● idle - waiting for input${RESET}"
echo -e "${DIM}This agent finished its turn without opening a PR. Use the TUI to send it more work.${RESET}"`
	if status == agent.StatusWaitingReview {
		stateLines = `echo -e "${GREEN}● waiting for PR review${RESET}"
echo -e "${DIM}PR:${RESET} $PR_URL"
echo -e "${DIM}This agent has a PR up for review. Use the TUI to accept, comment, or reject.${RESET}"`
	}

	sq := shellutil.Quote
	script := fmt.Sprintf(`#!/bin/bash

AGENT_ID=%s
WORKTREE_PATH=%s
PR_URL=%s
TASK=%s

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
GREEN="\033[38;5;46m"
RESET="\033[0m"
echo -e "${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Task:${RESET} $TASK"
echo -e "${DIM}Worktree:${RESET} $WORKTREE_PATH"
echo ""
%s
echo ""

while true; do
  sleep 3600
done
`, sq(agentID), sq(worktreePath), sq(prURL), sq(task), stateLines)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

func writeCIWaitPlaceholderScript(agentID, worktreePath, task string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+"-placeholder.sh")

	sq := shellutil.Quote
	script := fmt.Sprintf(`#!/bin/bash

AGENT_ID=%s
WORKTREE_PATH=%s
TASK=%s

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
GREEN="\033[38;5;46m"
RESET="\033[0m"
echo -e "${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Task:${RESET} $TASK"
echo -e "${DIM}Worktree:${RESET} $WORKTREE_PATH"
echo ""
echo -e "${GREEN}⏳ waiting on CI${RESET}"
echo -e "${DIM}This agent is waiting for CI to complete. The orchestrator will resume it automatically.${RESET}"
echo ""

while true; do
  sleep 3600
done
`, sq(agentID), sq(worktreePath), sq(task))

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

func writeMergeQueuePlaceholderScript(agentID, worktreePath, task string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+"-placeholder.sh")

	sq := shellutil.Quote
	script := fmt.Sprintf(`#!/bin/bash

AGENT_ID=%s
WORKTREE_PATH=%s
TASK=%s

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
ORANGE="\033[38;5;208m"
RESET="\033[0m"
echo -e "${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Task:${RESET} $TASK"
echo -e "${DIM}Worktree:${RESET} $WORKTREE_PATH"
echo ""
echo -e "${ORANGE}🚦 waiting on merge queue${RESET}"
echo -e "${DIM}This agent's PR is in the trunk.io merge queue. ccmux will clean up once it merges.${RESET}"
echo ""

while true; do
  sleep 3600
done
`, sq(agentID), sq(worktreePath), sq(task))

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

// sanitizeWorktreeName converts a user-supplied name into a safe string for
// use in branch names and directory paths: lowercase, spaces/underscores become
// hyphens, non-alphanumeric-hyphen chars are dropped, max 30 chars.
func sanitizeWorktreeName(name string) string {
	name = strings.ToLower(name)
	name = strings.Map(func(r rune) rune {
		if r == ' ' || r == '_' {
			return '-'
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, name)
	name = strings.Trim(name, "-")
	if len(name) > 30 {
		name = strings.TrimRight(name[:30], "-")
	}
	return name
}

func generateID() string {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return ""
	}
	return hex.EncodeToString(bytes)
}

func getCurrentSessionID() string {
	if !tmux.InsideTmux() {
		return defaultSessionID
	}

	cmd := exec.Command("tmux", "display-message", "-p", "#S")
	output, err := cmd.Output()
	if err != nil {
		return defaultSessionID
	}

	sessionName := strings.TrimSpace(string(output))
	if strings.HasPrefix(sessionName, "ccmux-") {
		return strings.TrimPrefix(sessionName, "ccmux-")
	}

	return defaultSessionID
}
