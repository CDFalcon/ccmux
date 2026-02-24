# ccmux — Colby's Claude Multiplexer

A terminal-based orchestrator for managing multiple [Claude Code](https://claude.ai/claude-code) agents working on tasks in parallel. Provides a unified tmux-backed interface to spawn, monitor, intervene with, and manage concurrent AI agents across git projects.

## How It Works

ccmux creates isolated environments for each agent using git worktrees and tmux windows. Each agent gets its own branch, worktree, and terminal session, so multiple agents can work on different tasks in the same repo without conflicts.

## Prerequisites

- Go 1.24+
- [tmux](https://github.com/tmux/tmux)
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI (`claude`)
- [GitHub CLI](https://cli.github.com/) (`gh`) — for PR creation and management
- Git

## Installation

```bash
go build -o ccmux ./cmd/ccmux && mv ccmux ~/bin/
```

Or use the included script:

```bash
./build_and_install.sh
```

## Quick Start

1. **Start a session:**

   ```bash
   ccmux
   ```

   This creates (or attaches to) a tmux session named `ccmux-default` with the TUI in the first window.

2. **Register a project:** Press `p` to open project management, then add a git repository.

3. **Spawn an agent:** Press `n`, select a project, and describe the task. ccmux creates a git worktree and launches Claude Code with the task.

4. **Monitor and review:** Agents appear in the dashboard. When an agent finishes and opens a PR, it shows up in the queue. Press `r` to review.

## Usage

### Starting a Session

```bash
ccmux              # uses "default" session
ccmux my-session   # uses named session
```

Each session maintains its own set of agents and queue. Sessions are backed by tmux, so you can detach (`q`) and reattach later.

### TUI Key Bindings

| Key | Action |
|-----|--------|
| `n` | Spawn a new agent on a task |
| `r` | Review PRs from completed agents |
| `j` | Jump to an agent's tmux window |
| `k` | Kill an agent (cleanup worktree/branch) |
| `p` | Manage registered projects |
| `K` | Kill the entire session |
| `q` | Detach from session |

### Agent Lifecycle

1. **Spawning** — Worktree and tmux window created, Claude Code launching
2. **Running** — Agent is actively working on the task
3. **Ready** — Agent created a PR or finished; waiting for review
4. **Merged/Failed** — Terminal states after cleanup

### CLI Commands

Most interaction happens through the TUI, but ccmux also exposes commands used internally by agents:

```bash
ccmux spawn "fix the login bug" --project myapp    # spawn an agent
ccmux pr-ready <pr-url>                             # agent signals PR is ready
ccmux need-help "stuck on database schema"          # agent requests intervention
ccmux kill-session [session-id]                     # tear down a session
```

## Architecture

### Data Storage

All state lives in `~/.ccmux/`:

```
~/.ccmux/
├── projects.json                    # registered git projects
├── sessions/<session-id>/
│   ├── agents.json                  # agent states
│   └── queue.json                   # event queue
├── launchers/<agent-id>.sh          # agent startup scripts
└── logs/ccmux.log                    # debug logs
```

### Agent Isolation

Each agent operates in its own:
- **Git worktree** — branched from the project's base branch (default: `origin/master`)
- **Tmux window** — separate terminal within the ccmux session
- **Branch** — named `ccmux/<agent-id>`

### Event Queue

Agents communicate back to ccmux through an event queue with three item types:
- `pr_ready` — agent created a PR
- `question` — agent needs human input
- `idle` — agent has been inactive for 10+ seconds

### Claude Code Integration

Agents run Claude Code with:
- A system prompt explaining the ccmux workflow and available commands
- Shell hooks (`.claude/hooks/stop.sh`) for lifecycle notifications
- Environment variables (`CMUX_AGENT_ID`) for agent identification

## Development

### Running Tests

```bash
go test ./...
```

### Project Structure

```
cmd/ccmux/           CLI entry point and all commands (Cobra)
internal/
  agent/             Agent state management and persistence
  logging/           File-based debug logging
  project/           Git project registry
  queue/             Event queue for agent notifications
  tmux/              Tmux session/window management
  tui/               Terminal UI (Bubble Tea)
  worktree/          Git worktree operations
```
