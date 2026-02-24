# ccmux — Colby's Claude Multiplexer

A terminal-based orchestrator for managing multiple [Claude Code](https://claude.ai/claude-code) agents working on tasks in parallel. Provides a unified tmux-backed interface to spawn, monitor, intervene with, and manage concurrent AI agents across git projects.

Each agent gets its own git worktree, branch, and tmux window — so multiple agents can work on different tasks in the same repo without conflicts.

## Prerequisites

- [tmux](https://github.com/tmux/tmux)
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI (`claude`)
- [GitHub CLI](https://cli.github.com/) (`gh`)
- Git

## Installation

Download the latest binary for your platform from [GitHub Releases](https://github.com/CDFalcon/ccmux/releases):

```bash
# macOS (Apple Silicon)
curl -L https://github.com/CDFalcon/ccmux/releases/latest/download/ccmux-darwin-arm64 -o ccmux

# macOS (Intel)
curl -L https://github.com/CDFalcon/ccmux/releases/latest/download/ccmux-darwin-amd64 -o ccmux

# Linux (x86_64)
curl -L https://github.com/CDFalcon/ccmux/releases/latest/download/ccmux-linux-amd64 -o ccmux

# Linux (ARM64)
curl -L https://github.com/CDFalcon/ccmux/releases/latest/download/ccmux-linux-arm64 -o ccmux
```

Then make it executable and move it to your PATH:

```bash
chmod +x ccmux
mv ccmux /usr/local/bin/  # or ~/bin/, ~/.local/bin/, etc.
```

## Quick Start

1. **Start a session:**

   ```bash
   ccmux
   ```

2. **Register a project:** Press `p` to open project management, then add a git repository.

3. **Spawn an agent:** Press `n`, select a project and base branch, describe the task. ccmux creates a worktree and launches Claude Code.

4. **Monitor and review:** Agents appear in the dashboard. When an agent opens a PR, it shows up in the queue for review.
