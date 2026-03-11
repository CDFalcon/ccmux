<p align="center">
  <img src="assets/logo.svg" alt="ccmux logo" width="490">
</p>

# ccmux — Colby's Claude MUltipleXer

A terminal-based orchestrator for managing multiple [Claude Code](https://docs.anthropic.com/en/docs/claude-code) agents working on tasks in parallel. Provides a unified tmux-backed interface to spawn, monitor, intervene with, and manage concurrent AI agents across git projects.

When spawned, each agent gets its own git worktree, branch, and tmux window. ccmux will watch each agent while they work, queuing user actions (e.g. PR reviews) as appropriate. After an agent's PR is marked as accepted by the user, its worktree will be automatically cleaned up.

ccmux is designed to not interfere with users' current Claude Code setups. Spawned agents will respect existing Claude .MD's and additional agent prompting is kept to a minimum.

This project is in active development, so expect frequent updates. ccmux supports in-session updating, meaning users can update without fear of interrupting their existing agents. ccmux will automatically flag when updates are available. 

## Setup (Linux x86_64)
```bash
# Install system dependencies
sudo apt-get update && sudo apt-get install -y git tmux jq

# Install GitHub CLI
(type -p wget >/dev/null || (sudo apt update && sudo apt-get install wget -y)) \
  && sudo mkdir -p -m 755 /etc/apt/keyrings \
  && out=$(mktemp) && wget -nv -O$out https://cli.github.com/packages/githubcli-archive-keyring.gpg \
  && cat $out | sudo tee /etc/apt/keyrings/githubcli-archive-keyring.gpg > /dev/null \
  && sudo chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
  && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
  && sudo apt update && sudo apt install gh -y

# Authenticate with GitHub (needed for private repos and PR operations)
gh auth login

# Install Claude Code
npm install -g @anthropic-ai/claude-code

# Install ccmux
gh release download --repo colby-duke-ai/ccmux -p 'ccmux-linux-amd64'
chmod +x ccmux-linux-amd64
sudo mv ccmux-linux-amd64 /usr/local/bin/ccmux

# Optional: install proj for near-instant worktree creation on large repos
# Requires an XFS filesystem with reflink support (see proj README for setup)
# git clone git@github.com:Applied-Shared/proj.git && export PATH="$PATH:$(pwd)/proj/src"
```

## Quick Start

1. **Start a session:** `ccmux` (or `ccmux <name>` for a named session).

2. **Register a project:** Press `p` to open project management, then `a` to add a git repository.

3. **Spawn an agent:** Press `n`, select a project and base branch, describe the task. ccmux creates a worktree and launches Claude Code.

4. **Monitor and work the queue:** As agents work, items appear in the quick action queue. Press `q` to pop the top item and take action:

   - 💤 **Idle** — agent's terminal has gone quiet (may be stuck). Jump in to check on it or send it a message.
   - ❓ **Question** — agent explicitly asked for help. Read the details and respond.
   - 🔀 **PR Ready** — agent opened a pull request. **`a`**ccept (merge + cleanup), **`c`**omment (resume agent to address feedback), **`r`**eject (close PR + cleanup), or **`b`**rowser (open in browser).
