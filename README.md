# ccmux — Colby's Claude Multiplexer

A terminal-based orchestrator for managing multiple [Claude Code](https://claude.ai/claude-code) agents working on tasks in parallel. Provides a unified tmux-backed interface to spawn, monitor, intervene with, and manage concurrent AI agents across git projects.

Each agent gets its own git worktree, branch, and tmux window — so multiple agents can work on different tasks in the same repo without conflicts.

## Prerequisites

- [tmux](https://github.com/tmux/tmux)
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI (`claude`)
- [GitHub CLI](https://cli.github.com/) (`gh`)
- Git

## Installation

Download the latest binary for your platform using the GitHub CLI (required since this is a private repo):

```bash
# macOS (Apple Silicon)
gh release download --repo colby-duke-ai/ccmux -p 'ccmux-darwin-arm64'

# macOS (Intel)
gh release download --repo colby-duke-ai/ccmux -p 'ccmux-darwin-amd64'

# Linux (x86_64)
gh release download --repo colby-duke-ai/ccmux -p 'ccmux-linux-amd64'

# Linux (ARM64)
gh release download --repo colby-duke-ai/ccmux -p 'ccmux-linux-arm64'
```

Then make it executable and move it to your PATH:

```bash
chmod +x ccmux-*
mv ccmux-* /usr/local/bin/ccmux  # or ~/bin/, ~/.local/bin/, etc.
```

## Quick Start

1. **Start a session:** `ccmux` (or `ccmux <name>` for a named session).

2. **Register a project:** Press `p` to open project management, then `a` to add a git repository.

3. **Spawn an agent:** Press `n`, select a project and base branch, describe the task. ccmux creates a worktree and launches Claude Code.

4. **Monitor and work the queue:** As agents work, items appear in the quick action queue. Press `q` to pop the top item and take action:

   - 💤 **Idle** — agent's terminal has gone quiet (may be stuck). Jump in to check on it or send it a message.
   - ❓ **Question** — agent explicitly asked for help. Read the details and respond.
   - 🔀 **PR Ready** — agent opened a pull request. **`a`**ccept (merge + cleanup), **`c`**omment (resume agent to address feedback), **`r`**eject (close PR + cleanup), or **`b`**rowser (open in browser).
