package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/logging"
	"github.com/CDFalcon/ccmux/internal/project"
	"github.com/CDFalcon/ccmux/internal/queue"
	"github.com/CDFalcon/ccmux/internal/tmux"
	"github.com/CDFalcon/ccmux/internal/tui"
	"github.com/CDFalcon/ccmux/internal/worktree"
	"github.com/spf13/cobra"
)

const defaultSessionID = "default"

func main() {
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
		spawnCmd(),
		registerAgentCmd(),
		queueAddCmd(),
		prReadyCmd(),
		needHelpCmd(),
		agentStoppedCmd(),
		focusCmd(),
		cleanupCmd(),
		killCmd(),
		killSessionCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runSession(sessionID string) error {
	tmuxSessionName := fmt.Sprintf("ccmux-%s", sessionID)
	tmuxManager := tmux.NewManager(tmuxSessionName)

	if !tmux.InsideTmux() {
		if !tmuxManager.SessionExists() {
			exePath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to get executable path: %w", err)
			}
			cmd := fmt.Sprintf("%s %s", exePath, sessionID)
			homeDir, _ := os.UserHomeDir()
			if err := tmuxManager.CreateSessionWithCommand(homeDir, cmd); err != nil {
				return err
			}
		} else {
			tmuxManager.SourceUserConfig()
			tmuxManager.EnsureRemainOnExit()
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

	return tui.Run(agentStore, queueManager, projectStore, tmuxManager, sessionID)
}

func spawnCmd() *cobra.Command {
	var projectName string
	var baseBranch string

	cmd := &cobra.Command{
		Use:    "spawn <task>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			task := args[0]
			logging.Log("spawn: starting for task=%q project=%q branch=%q", task, projectName, baseBranch)

			if projectName == "" {
				return fmt.Errorf("--project is required")
			}

			if baseBranch == "" {
				baseBranch = "origin/master"
			}

			sessionID := getCurrentSessionID()
			agentID := generateID()
			logging.Log("spawn: generated agentID=%s sessionID=%s", agentID, sessionID)

			projectStore, err := project.NewStore()
			if err != nil {
				return err
			}
			proj, err := projectStore.Get(projectName)
			if err != nil {
				return fmt.Errorf("project not found: %s", projectName)
			}

			tmuxSessionName := fmt.Sprintf("ccmux-%s", sessionID)
			tmuxManager := tmux.NewManager(tmuxSessionName)

			launcherScript, err := writeLauncherScript(agentID, task, proj.Path, baseBranch, sessionID)
			if err != nil {
				return fmt.Errorf("failed to create launcher script: %w", err)
			}

			windowID, err := tmuxManager.CreateWindow(proj.Path, "bash "+launcherScript, agentID[:8])
			if err != nil {
				os.Remove(launcherScript)
				return fmt.Errorf("failed to create tmux window: %w", err)
			}
			logging.Log("spawn: created window=%s with launcher script", windowID)

			agentStore, err := agent.NewStore(sessionID)
			if err != nil {
				return err
			}
			a := &agent.Agent{
				ID:         agentID,
				Task:       task,
				TmuxWindow: windowID,
				BaseBranch: baseBranch,
				Status:     agent.StatusSpawning,
			}
			if err := agentStore.Create(a); err != nil {
				return err
			}

			fmt.Printf("Spawned agent %s for task: %s\n", agentID, task)
			return nil
		},
	}

	cmd.Flags().StringVar(&projectName, "project", "", "Project to use")
	cmd.Flags().StringVar(&baseBranch, "branch", "", "Base branch to create worktree from (default: origin/master)")
	cmd.MarkFlagRequired("project")

	return cmd
}

func writeLauncherScript(agentID, task, repoPath, baseBranch, sessionID string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+".sh")

	script := fmt.Sprintf(`#!/bin/bash
set -e

AGENT_ID="%s"
TASK=%q
REPO_PATH="%s"
BASE_BRANCH="%s"
SESSION_ID="%s"

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
RESET="\033[0m"
echo -e "${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Task:${RESET} $TASK"
echo ""

# Create worktree
WORKTREE_PATH="$(dirname "$REPO_PATH")/ccmux-$AGENT_ID"
BRANCH_NAME="ccmux/$AGENT_ID"

echo "→ Creating worktree at $WORKTREE_PATH..."
cd "$REPO_PATH"
git worktree add -b "$BRANCH_NAME" "$WORKTREE_PATH" "$BASE_BRANCH"
cd "$WORKTREE_PATH"
echo "✓ Worktree created"
echo ""

# Install hooks
echo "→ Installing Claude Code hooks..."
mkdir -p .claude/hooks

cat > .claude/hooks/stop.sh << 'HOOKEOF'
#!/bin/bash
ccmux agent-stopped "$CCMUX_AGENT_ID"
HOOKEOF
chmod +x .claude/hooks/stop.sh

cat > .claude/settings.json << SETTINGSEOF
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "CCMUX_AGENT_ID=$AGENT_ID $WORKTREE_PATH/.claude/hooks/stop.sh"
          }
        ]
      }
    ]
  }
}
SETTINGSEOF
echo "✓ Hooks installed"
echo ""

# Pre-trust worktree directory in Claude Code
echo "→ Pre-trusting worktree directory..."
CLAUDE_JSON="$HOME/.claude.json"
if [ -f "$CLAUDE_JSON" ]; then
  jq --arg path "$WORKTREE_PATH" '.projects[$path].hasTrustDialogAccepted = true' "$CLAUDE_JSON" > "${CLAUDE_JSON}.tmp" && mv "${CLAUDE_JSON}.tmp" "$CLAUDE_JSON"
else
  echo '{}' | jq --arg path "$WORKTREE_PATH" '.projects[$path].hasTrustDialogAccepted = true' > "$CLAUDE_JSON"
fi
echo "✓ Directory trusted"
echo ""

# Register agent
echo "→ Registering agent..."
WINDOW_ID=$(tmux display-message -p '#{window_id}')
ccmux register-agent --id="$AGENT_ID" --task="$TASK" --worktree="$WORKTREE_PATH" --branch="$BRANCH_NAME" --base="$BASE_BRANCH" --window="$WINDOW_ID"
echo "✓ Agent registered"
echo ""

echo -e "${DIM}Starting Claude Code...${RESET}"
echo ""

export CCMUX_AGENT_ID="$AGENT_ID"
export CLAUDE_CODE_USE_BEDROCK=1
export AWS_REGION=us-west-2
unset CLAUDECODE

claude --permission-mode dontAsk --system-prompt "You are working on a task as part of the ccmux agent system. Environment variable CCMUX_AGENT_ID=$AGENT_ID is set for hook integration.

When done with your task:
1. Commit your work and create a PR with: gh pr create --title \"...\" --body \"...\"
2. After creating the PR, run: ccmux pr-ready <pr-url>" \
  "$TASK"

ccmux agent-stopped "$AGENT_ID"
`, agentID, task, repoPath, baseBranch, sessionID)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
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

			return agentStore.Update(agentID, func(a *agent.Agent) {
				a.Status = agent.StatusReady
			})
		},
	}
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

func needHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "need-help <description>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			description := args[0]

			agentID := os.Getenv("CCMUX_AGENT_ID")
			if agentID == "" {
				return fmt.Errorf("CCMUX_AGENT_ID environment variable not set")
			}

			sessionID := getCurrentSessionID()
			queueManager, err := queue.NewQueue(sessionID)
			if err != nil {
				return err
			}

			_, err = queueManager.Add(queue.ItemTypeQuestion, agentID, description, description)
			return err
		},
	}
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

			switch a.Status {
			case agent.StatusReady:
				// Agent made a PR - it's already in queue, nothing to do
			case agent.StatusRunning:
				// Agent stopped without making a PR - add to queue for review
				agentStore.Update(agentID, func(ag *agent.Agent) {
					ag.Status = agent.StatusReady
				})
				_, err = queueManager.Add(queue.ItemTypeIdle, agentID, "Agent finished (no PR)", "")
				if err != nil {
					return err
				}
			}

			return nil
		},
	}
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
			return doCleanup(args[0], "Cleaned up")
		},
	}
}

func killCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "kill <agent-id>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doCleanup(args[0], "Killed")
		},
	}
}

func doCleanup(agentID, action string) error {
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
	tmuxManager.KillWindow(a.TmuxWindow)

	repoRoot, err := project.GetRepoRoot(a.WorktreePath)
	if err == nil {
		wtManager := worktree.NewManager(repoRoot)
		os.RemoveAll(filepath.Join(a.WorktreePath, ".claude"))
		if err := wtManager.Remove(a.WorktreePath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove worktree: %v\n", err)
		}
		wtManager.DeleteBranch(a.BranchName)
	}

	queueManager, err := queue.NewQueue(sessionID)
	if err != nil {
		return err
	}
	queueManager.RemoveByAgent(agentID)

	agentStore.Delete(agentID)

	fmt.Printf("%s agent %s\n", action, agentID)
	return nil
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

			tmuxSessionName := fmt.Sprintf("ccmux-%s", sessionID)
			tmuxManager := tmux.NewManager(tmuxSessionName)

			if !tmuxManager.SessionExists() {
				return fmt.Errorf("session %s does not exist", tmuxSessionName)
			}

			agentStore, err := agent.NewStore(sessionID)
			if err != nil {
				return err
			}

			agents, _ := agentStore.List()
			for _, a := range agents {
				repoRoot, err := project.GetRepoRoot(a.WorktreePath)
				if err == nil {
					wtManager := worktree.NewManager(repoRoot)
					os.RemoveAll(filepath.Join(a.WorktreePath, ".claude"))
					wtManager.Remove(a.WorktreePath)
					wtManager.DeleteBranch(a.BranchName)
				}
				agentStore.Delete(a.ID)
			}

			if err := tmuxManager.KillSession(); err != nil {
				return err
			}

			fmt.Printf("Killed session %s\n", tmuxSessionName)
			return nil
		},
	}
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
