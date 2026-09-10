// Package sysprompt holds shared fragments of the agent system prompt that
// launcher scripts embed. Fragments are spliced into double-quoted bash
// strings inside Sprintf format strings, so they must not contain double
// quotes, backticks, dollar signs, backslashes, or percent signs.
package sysprompt

// SharePaneDoc documents the `ccmux pane` commands agents can use to share
// live program output with the user in a split pane below their own.
const SharePaneDoc = `You can share live output with the user in a tmux split pane below your main pane:
    ccmux pane open [command...]   open the shared pane; runs the command there, or an interactive shell if omitted
    ccmux pane run <command...>    run a shell command in the shared pane (opens a shell pane first if needed)
    ccmux pane close               close the shared pane
Use it to show the user long-running programs and outputs: dev servers, log tails, test watchers, builds, or demos. Output stays visible to the user even after the command exits.`

// PeerAgentsDoc documents the `ccmux agents` commands agents can use to
// discover and message the other agents in their session.
const PeerAgentsDoc = `Other ccmux agents may be working alongside you in this session. You can list them and message them:
    ccmux agents list                          list the agents in this session (id, status, project, branch, PR, task preview; --full for whole tasks); your own row is marked (you)
    ccmux agents send <agent-id> <message...>  deliver a message to another agent; it lands in that agent's prompt as: [message from ccmux agent <your-id>] ...
Use this to coordinate on shared files, avoid duplicate work, ask another agent a question, or hand off results. Messages from other agents arrive in your prompt with the same prefix; reply with ccmux agents send <their-id> <message>. Only agents whose harness is currently running can receive messages; the command tells you when one cannot.`

// ReloadDoc documents `ccmux reload`, which an agent runs on itself to
// restart its harness in place and pick up newly configured MCP servers,
// tools, hooks or settings.
const ReloadDoc = `You can reload your own harness session in place, e.g. after adding an MCP server or changing settings that only load at startup:
    ccmux reload [note...]         restart the harness in your pane, resuming this conversation; the optional note is handed to you after the reload
The reload happens a couple of seconds after the command returns, so end your turn there: do not start more work after calling it. Your conversation history is preserved where the harness supports it (Claude Code resumes it; Codex starts fresh with the original task and your note), and your worktree, branch, and shared pane are untouched.`
