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

// InstallsClaudeHooks reports whether ccmux should install the Claude Code
// Stop/PostToolUse hooks (and the .claude/settings.json wiring) into the
// worktree for this harness. Only Claude Code consumes those hooks.
func (t Type) InstallsClaudeHooks() bool {
	return t == Claude
}
