// Package harness describes the coding-agent CLIs ("harnesses") that ccmux
// can launch and drive. Historically ccmux only spoke to Claude Code; this
// package adds an abstraction so other CLIs (currently OpenAI's Codex) can be
// driven through the same spawn/recover/resume machinery.
package harness

import (
	"os/exec"
	"strings"
)

// Type identifies a coding-agent CLI that ccmux can launch.
type Type string

const (
	// Claude is Anthropic's Claude Code CLI (`claude`).
	Claude Type = "claude"
	// Codex is OpenAI's Codex CLI (`codex`).
	Codex Type = "codex"
)

// Default is the harness used when none is specified. It is Claude so that
// existing projects and agents (which predate harness selection) keep their
// original behaviour.
const Default = Claude

// All returns the selectable harnesses in display order.
func All() []Type {
	return []Type{Claude, Codex}
}

// Parse normalises a stored or flag-provided string into a Type. Empty or
// unrecognised values fall back to Default.
func Parse(s string) Type {
	switch Type(strings.ToLower(strings.TrimSpace(s))) {
	case Codex:
		return Codex
	case Claude:
		return Claude
	default:
		return Default
	}
}

// Valid reports whether s names a known harness (ignoring case/whitespace).
func Valid(s string) bool {
	switch Type(strings.ToLower(strings.TrimSpace(s))) {
	case Claude, Codex:
		return true
	default:
		return false
	}
}

// DisplayName is the human-readable label shown in the TUI.
func (t Type) DisplayName() string {
	switch t {
	case Codex:
		return "Codex"
	default:
		return "Claude Code"
	}
}

// CLIName is the executable name invoked for this harness.
func (t Type) CLIName() string {
	switch t {
	case Codex:
		return "codex"
	default:
		return "claude"
	}
}

// Installed reports whether the harness CLI is available on PATH.
func (t Type) Installed() bool {
	_, err := exec.LookPath(t.CLIName())
	return err == nil
}

// StartCommand returns the shell command that starts a fresh agent session.
// The launcher script must define the SYSTEM_PROMPT and TASK shell variables
// before invoking it.
//
// Claude Code accepts a dedicated --system-prompt flag; Codex has no such
// flag, so the system prompt is prepended to the task as the initial message.
func (t Type) StartCommand() string {
	switch t {
	case Codex:
		return "codex --dangerously-bypass-approvals-and-sandbox \"$SYSTEM_PROMPT\n\n$TASK\""
	default:
		return "claude --dangerously-skip-permissions --system-prompt \"$SYSTEM_PROMPT\" \"$TASK\""
	}
}

// ContinueCommand returns the shell command used to resume an agent after a
// session loss or restart, without handing it a new instruction. The launcher
// script must define the SYSTEM_PROMPT shell variable (which, for resume
// flows, also carries the original task and "continue where you left off"
// context).
//
// Claude Code resumes its prior conversation with --continue. Codex sessions
// are not addressable per-worktree, so ccmux instead starts a fresh Codex
// session seeded with the full context; the worktree's commits and working
// tree carry the actual progress.
func (t Type) ContinueCommand() string {
	switch t {
	case Codex:
		return "codex --dangerously-bypass-approvals-and-sandbox \"$SYSTEM_PROMPT\""
	default:
		return "claude --continue --dangerously-skip-permissions --system-prompt \"$SYSTEM_PROMPT\""
	}
}

// ContinueWithPromptCommand returns the shell command that resumes an agent
// with a fresh system prompt AND an initial message, for flows where the agent
// must keep its conversation but be told why it is starting again — currently
// `ccmux reload`, which an agent runs on itself to pick up newly configured
// MCP servers, tools, hooks or settings. The launcher script must define the
// SYSTEM_PROMPT and PROMPT shell variables.
//
// Claude Code resumes the prior conversation with --continue and treats the
// positional argument as the next user message. Codex cannot resume, so as
// with ContinueCommand it starts a fresh session seeded with the system prompt
// (which carries the original task) followed by the message.
func (t Type) ContinueWithPromptCommand() string {
	switch t {
	case Codex:
		return "codex --dangerously-bypass-approvals-and-sandbox \"$SYSTEM_PROMPT\n\n$PROMPT\""
	default:
		return "claude --continue --dangerously-skip-permissions --system-prompt \"$SYSTEM_PROMPT\" \"$PROMPT\""
	}
}

// ResumeWithPromptPrefix returns the leading portion of a command that resumes
// an agent and hands it a new, self-contained instruction (PR-review, CI-fix
// and merge-conflict flows). Callers append a shell-quoted prompt string.
//
// As with ContinueCommand, Claude Code keeps its conversation via --continue
// while Codex starts a fresh session driven entirely by the appended prompt.
func (t Type) ResumeWithPromptPrefix() string {
	switch t {
	case Codex:
		return "codex --dangerously-bypass-approvals-and-sandbox"
	default:
		return "claude --continue --dangerously-skip-permissions"
	}
}

// TelemetryEnvBlock is the launcher-script fragment that points the Claude
// harness's OpenTelemetry exporter at the in-process ccmux collector, so the
// TUI gets Anthropic's own per-turn cost figure instead of re-deriving it from
// the JSONL transcript. It is shared by every script that starts a harness in
// an agent's pane (spawn and `ccmux reload`) so a reloaded agent keeps
// reporting cost. Scripts using it must define AGENT_ID, WORKTREE_PATH and
// HARNESS. Safe to embed in a fmt.Sprintf format string: no percent verbs.
//
// Best-effort:
//   - We never clobber a user's existing OTEL_EXPORTER_OTLP_ENDPOINT
//     (e.g. someone already running TokenKeeper). They keep their
//     pipeline; ccmux falls back to the JSONL estimate for those agents.
//   - We only enable for the Claude harness — the Codex CLI does not
//     currently emit OTel metrics. Re-evaluate if/when it does.
//   - If no collector is running (no TUI, or it crashed) the endpoint
//     file is absent and we skip the export. The agent runs normally
//     with no telemetry side-effects.
const TelemetryEnvBlock = `# OpenTelemetry to in-process ccmux collector (Claude harness only; skipped
# when the user already exports OTEL_EXPORTER_OTLP_ENDPOINT or no ccmux
# collector is running).
if [ "$HARNESS" = "claude" ] && [ -z "${OTEL_EXPORTER_OTLP_ENDPOINT:-}" ] && [ -r "$HOME/.ccmux/otel-endpoint" ]; then
  CCMUX_OTEL_ENDPOINT=$(cat "$HOME/.ccmux/otel-endpoint" 2>/dev/null || true)
  if [ -n "$CCMUX_OTEL_ENDPOINT" ]; then
    export CLAUDE_CODE_ENABLE_TELEMETRY=1
    export OTEL_METRICS_EXPORTER=otlp
    export OTEL_EXPORTER_OTLP_PROTOCOL=http/json
    export OTEL_EXPORTER_OTLP_ENDPOINT="$CCMUX_OTEL_ENDPOINT"
    export OTEL_METRIC_EXPORT_INTERVAL=15000
    # Stamp every metric with the ccmux agent id (resource attribute) so
    # the collector can attribute cost without depending on Claude's
    # internal session.id mapping. The worktree path is informational —
    # handy for future per-project rollups.
    export OTEL_RESOURCE_ATTRIBUTES="ccmux.agent.id=$AGENT_ID,ccmux.worktree.path=$WORKTREE_PATH"
  fi
fi
`

// ExitCapturePrologue and ExitCaptureEpilogue bracket the harness invocation in
// every generated agent script.
//
// All those scripts run under `set -e`, and the harness invocation was the last
// unguarded command before `ccmux agent-stopped`. A harness that exited non-zero
// therefore aborted the script *before* the status transition — the reported case
// being `claude --continue` printing "No conversation found to continue" and
// exiting 1, which stranded the agent in StatusRunning behind a dead pane with
// nothing to indicate it had stopped. Since tmux keeps the pane on
// remain-on-exit, the only signal was a status column that still said "running".
//
// Bracketing the call means the status is always updated: a clean exit takes the
// normal Stop-hook path, and a non-zero one is recorded as StatusFailed carrying
// the exit code, which the TUI surfaces in the review queue.
//
// Usage is three parts, in order: ExitCapturePrologue, the harness invocation,
// ExitCaptureCapture, then any post-run commands, then ExitCaptureReport. The
// capture must come immediately after the harness call — anything in between
// would clobber $? — which is why it is separate from the report.
//
// All three are safe to embed in a fmt.Sprintf format string; they contain no
// percent verbs. Scripts using them must define AGENT_ID.
const (
	ExitCapturePrologue = "set +e\n"

	ExitCaptureCapture = `CCMUX_HARNESS_EXIT=$?
set -e
`

	ExitCaptureReport = `
if [ "${CCMUX_HARNESS_EXIT:-0}" -ne 0 ]; then
  ccmux agent-failed "$AGENT_ID" --reason="harness exited ${CCMUX_HARNESS_EXIT}" || true
else
  ccmux agent-stopped "$AGENT_ID"
fi
`
)

// InstallsClaudeHooks reports whether ccmux should install the Claude Code
// Stop/PostToolUse hooks (and the .claude/settings.json wiring) into the
// worktree for this harness. Only Claude Code consumes those hooks.
func (t Type) InstallsClaudeHooks() bool {
	return t == Claude
}
