// Package tmux provides tmux session and window management.
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/CDFalcon/ccmux/internal/shellutil"
)

const (
	DefaultSessionWidth  = "200"
	DefaultSessionHeight = "50"
)

type Manager struct {
	sessionName string
}

func NewManager(sessionName string) *Manager {
	return &Manager{sessionName: sessionName}
}

func GetBaseIndex() int {
	cmd := exec.Command("tmux", "show-option", "-gv", "base-index")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	idx, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0
	}
	return idx
}

func (m *Manager) FirstWindowTarget() string {
	return fmt.Sprintf("%s:%d", m.sessionName, GetBaseIndex())
}

func (m *Manager) SessionName() string {
	return m.sessionName
}

func (m *Manager) SessionExists() bool {
	cmd := exec.Command("tmux", "has-session", "-t", m.sessionName)
	return cmd.Run() == nil
}

func (m *Manager) CreateSessionWithCommand(workingDir, command string) error {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", m.sessionName, "-c", workingDir, "-x", DefaultSessionWidth, "-y", DefaultSessionHeight)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create tmux session: %s: %w", string(output), err)
	}
	m.ForwardEnv()
	m.SourceUserConfig()
	m.DisableSessionRemainOnExit()
	m.SetupAgentNavigation()
	m.SetupPaneHooks()
	m.SetPaneRemainOnExit(m.FirstWindowTarget())
	if err := m.RespawnPane(m.FirstWindowTarget(), command); err != nil {
		return fmt.Errorf("failed to start command in session: %w", err)
	}
	return nil
}

// ForwardEnv synchronises the tmux session environment with the current
// process environment so commands spawned inside the session see the same
// variables as the shell that launched ccmux. This is necessary because the
// tmux server may have been started in a different environment.
//
// Copying the current variables in is not enough: the server's global
// environment (inherited from whichever shell first started the server) and
// any session environment left over from a previous launch can contain
// variables that are no longer set in the launching shell. Those are marked
// for removal so tmux strips them from the environment of new processes,
// instead of leaking stale values into every pane.
func (m *Manager) ForwardEnv() {
	current := make(map[string]string)
	for _, entry := range os.Environ() {
		if idx := strings.Index(entry, "="); idx > 0 {
			current[entry[:idx]] = entry[idx+1:]
		}
	}
	for _, name := range m.staleEnvNames(current) {
		exec.Command("tmux", "set-environment", "-t", m.sessionName, "-r", name).Run()
	}
	for key, val := range current {
		if tmuxManagedVars[key] {
			continue
		}
		exec.Command("tmux", "set-environment", "-t", m.sessionName, key, val).Run()
	}
}

// tmuxManagedVars are set by tmux itself for every pane it spawns. They are
// never forwarded or marked stale: the launching shell legitimately lacks
// them (or holds values for a different server/pane).
var tmuxManagedVars = map[string]bool{
	"TMUX":      true,
	"TMUX_PANE": true,
}

// staleEnvNames returns the variables visible in the tmux server's global
// environment or in this session's environment that are absent from the
// current process environment.
func (m *Manager) staleEnvNames(current map[string]string) []string {
	seen := make(map[string]bool)
	var stale []string
	for _, args := range [][]string{
		{"show-environment", "-g"},
		{"show-environment", "-t", m.sessionName},
	} {
		output, err := exec.Command("tmux", args...).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(output), "\n") {
			name, ok := parseEnvName(line)
			if !ok || tmuxManagedVars[name] || seen[name] {
				continue
			}
			if _, isCurrent := current[name]; isCurrent {
				continue
			}
			seen[name] = true
			stale = append(stale, name)
		}
	}
	return stale
}

// parseEnvName extracts the variable name from one line of show-environment
// output: "NAME=value" for set variables, or "-NAME" for variables already
// marked for removal. Lines that don't look like a variable definition (such
// as continuation lines of multi-line values) are rejected.
func parseEnvName(line string) (string, bool) {
	if rest, ok := strings.CutPrefix(line, "-"); ok && isValidEnvName(rest) {
		return rest, true
	}
	idx := strings.Index(line, "=")
	if idx <= 0 || !isValidEnvName(line[:idx]) {
		return "", false
	}
	return line[:idx], true
}

func isValidEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func (m *Manager) SourceUserConfig() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	configPath := homeDir + "/.tmux.conf"
	if _, err := os.Stat(configPath); err != nil {
		return nil
	}
	cmd := exec.Command("tmux", "source-file", configPath)
	cmd.Run()
	return nil
}

func (m *Manager) CreateWindow(workingDir, command, name string) (string, string, error) {
	cmd := exec.Command("tmux", "new-window", "-d", "-t", m.sessionName, "-c", workingDir, "-P", "-F", "#{window_id}\t#{pane_id}", "-n", name, command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("failed to create window: %s: %w", string(output), err)
	}
	parts := strings.SplitN(strings.TrimSpace(string(output)), "\t", 2)
	windowID := parts[0]
	var paneID string
	if len(parts) > 1 {
		paneID = parts[1]
	}
	m.SetPaneRemainOnExit(windowID)
	return windowID, paneID, nil
}

func (m *Manager) KillWindow(windowID string) error {
	cmd := exec.Command("tmux", "kill-window", "-t", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to kill window: %s: %w", string(output), err)
	}
	return nil
}

// SplitPaneBelow creates a new pane below targetPane (vertical stack) taking
// roughly 30% of the window height. If command is empty the pane runs the
// default shell. Returns the new pane's ID. The pane is created without
// changing focus.
func (m *Manager) SplitPaneBelow(targetPane, workingDir, command string) (string, error) {
	args := []string{"split-window", "-v", "-d", "-t", targetPane, "-l", "30%", "-P", "-F", "#{pane_id}"}
	if workingDir != "" {
		args = append(args, "-c", workingDir)
	}
	if command != "" {
		args = append(args, command)
	}
	cmd := exec.Command("tmux", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to split pane: %s: %w", string(output), err)
	}
	return strings.TrimSpace(string(output)), nil
}

// PaneExists reports whether the pane with the given ID still exists.
func (m *Manager) PaneExists(paneID string) bool {
	cmd := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_id}")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == paneID
}

// GetPaneWindowID returns the window ID containing the given pane.
func (m *Manager) GetPaneWindowID(paneID string) (string, error) {
	cmd := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{window_id}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get window for pane: %s: %w", string(output), err)
	}
	return strings.TrimSpace(string(output)), nil
}

// GetPaneStartCommand returns the command the pane was spawned with, or ""
// for a pane running the default shell.
func (m *Manager) GetPaneStartCommand(paneID string) (string, error) {
	cmd := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_start_command}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get pane start command: %s: %w", string(output), err)
	}
	return strings.TrimSpace(string(output)), nil
}

// RespawnPaneCmd kills whatever is running in target and respawns it with
// command, or with the default shell if command is empty.
func (m *Manager) RespawnPaneCmd(target, command string) error {
	args := []string{"respawn-pane", "-k", "-t", target}
	if command != "" {
		args = append(args, command)
	}
	cmd := exec.Command("tmux", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to respawn pane: %s: %w", string(output), err)
	}
	return nil
}

// RespawnPaneDeferred schedules RespawnPaneCmd to run on target after delay,
// from a background job owned by the tmux server rather than from this
// process.
//
// It exists for `ccmux reload`, which an agent runs from inside the very pane
// it is asking to respawn. respawn-pane -k hangs up the pane's whole process
// tree — the launcher script, the harness, the ccmux process issuing the
// command, and the tmux client it spawned — so a direct call would kill the
// harness mid-tool-call before it could record the tool's result. Detaching
// the respawn into `run-shell -b` and waiting a moment lets ccmux print its
// confirmation and exit and the harness flush the result to its transcript,
// so the resumed conversation is intact.
func (m *Manager) RespawnPaneDeferred(target, command string, delay time.Duration) error {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found on PATH: %w", err)
	}
	secs := int(delay.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	job := fmt.Sprintf("sleep %d; exec %s respawn-pane -k -t %s %s",
		secs, shellutil.Quote(tmuxBin), shellutil.Quote(target), shellutil.Quote(command))
	cmd := exec.Command("tmux", "run-shell", "-b", job)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to schedule pane respawn: %s: %w", string(output), err)
	}
	return nil
}

// SetWindowOption sets a window option on the window containing target.
func (m *Manager) SetWindowOption(target, name, value string) error {
	cmd := exec.Command("tmux", "set-option", "-w", "-t", target, name, value)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set window option: %s: %w", string(output), err)
	}
	return nil
}

// GetWindowOption returns the value of a window option on the window
// containing target, or "" if unset.
func (m *Manager) GetWindowOption(target, name string) (string, error) {
	cmd := exec.Command("tmux", "show-options", "-w", "-t", target, "-qv", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get window option: %s: %w", string(output), err)
	}
	return strings.TrimSpace(string(output)), nil
}

// UnsetWindowOption removes a window option from the window containing target.
func (m *Manager) UnsetWindowOption(target, name string) error {
	cmd := exec.Command("tmux", "set-option", "-w", "-t", target, "-u", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to unset window option: %s: %w", string(output), err)
	}
	return nil
}

func (m *Manager) KillPane(paneID string) error {
	cmd := exec.Command("tmux", "kill-pane", "-t", paneID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to kill pane: %s: %w", string(output), err)
	}
	return nil
}

func (m *Manager) SelectWindow(windowID string) error {
	cmd := exec.Command("tmux", "select-window", "-t", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to select window: %s: %w", string(output), err)
	}
	return nil
}

func (m *Manager) SelectFirstWindow() error {
	return m.SelectWindow(m.FirstWindowTarget())
}

func (m *Manager) SendKeys(target, keys string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", target, keys, "Enter")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to send keys: %s: %w", string(output), err)
	}
	return nil
}

// SendText types text into target as input and submits it with Enter. A
// single-line message is sent as literal keys (no tmux key-name lookup, so a
// message that happens to be "Enter" or "C-c" is typed, not interpreted). A
// multi-line message goes through a tmux paste buffer with bracketed paste,
// so applications that support it (the agent harnesses do) receive the
// newlines as part of the text instead of as early submits.
func (m *Manager) SendText(target, text string) error {
	if strings.Contains(text, "\n") {
		bufName := fmt.Sprintf("ccmux-msg-%d", time.Now().UnixNano())
		load := exec.Command("tmux", "load-buffer", "-b", bufName, "-")
		load.Stdin = strings.NewReader(text)
		if output, err := load.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to load paste buffer: %s: %w", string(output), err)
		}
		paste := exec.Command("tmux", "paste-buffer", "-p", "-d", "-b", bufName, "-t", target)
		if output, err := paste.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to paste text: %s: %w", string(output), err)
		}
	} else {
		cmd := exec.Command("tmux", "send-keys", "-t", target, "-l", text)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to send text: %s: %w", string(output), err)
		}
	}
	cmd := exec.Command("tmux", "send-keys", "-t", target, "Enter")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to send Enter: %s: %w", string(output), err)
	}
	return nil
}

func (m *Manager) RespawnPane(windowID, command string) error {
	cmd := exec.Command("tmux", "respawn-pane", "-k", "-t", windowID, command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to respawn pane: %s: %w", string(output), err)
	}
	return nil
}

func (m *Manager) AttachSession() error {
	cmd := exec.Command("tmux", "attach-session", "-t", m.sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (m *Manager) KillSession() error {
	cmd := exec.Command("tmux", "kill-session", "-t", m.sessionName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to kill session: %s: %w", string(output), err)
	}
	return nil
}

func (m *Manager) GetWindowActivity(windowID string) (time.Time, error) {
	cmd := exec.Command("tmux", "display-message", "-t", windowID, "-p", "#{window_activity}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get window activity: %s: %w", string(output), err)
	}
	epoch, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse window activity timestamp: %w", err)
	}
	return time.Unix(epoch, 0), nil
}

func (m *Manager) GetPanePID(windowID string) (int, error) {
	cmd := exec.Command("tmux", "display-message", "-t", windowID, "-p", "#{pane_pid}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to get pane pid: %s: %w", string(output), err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, fmt.Errorf("failed to parse pane pid: %w", err)
	}
	return pid, nil
}

// LiveWindowIDs returns the set of window IDs (e.g. "@7") that currently exist
// in this session, in a single tmux call.
//
// Callers use it to avoid per-agent tmux/git work for registry entries whose
// window is already gone. One fork here replaces N doomed
// `tmux display-message -t <dead-window>` forks per refresh.
//
// A nil map (with a nil error) is never returned: an error means "could not
// determine", and callers are expected to fail open rather than treat every
// agent as dead.
func (m *Manager) LiveWindowIDs() (map[string]bool, error) {
	cmd := exec.Command("tmux", "list-windows", "-t", m.sessionName, "-F", "#{window_id}")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list windows: %w", err)
	}
	live := make(map[string]bool)
	for _, line := range strings.Split(string(output), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			live[id] = true
		}
	}
	return live, nil
}

func (m *Manager) RenameWindow(windowID, name string) error {
	cmd := exec.Command("tmux", "rename-window", "-t", windowID, name)
	cmd.Run()
	return nil
}

func (m *Manager) EnsureRemainOnExit() {
	m.RemoveRemainOnExitHook()
	m.DisableSessionRemainOnExit()
	m.SetPaneRemainOnExit(m.FirstWindowTarget())
}

func (m *Manager) RemoveRemainOnExitHook() {
	exec.Command("tmux", "set-hook", "-u", "-t", m.sessionName, "after-new-window").Run()
}

func (m *Manager) DisableSessionRemainOnExit() {
	exec.Command("tmux", "set-option", "-t", m.sessionName, "remain-on-exit", "off").Run()
}


func (m *Manager) SetPaneRemainOnExit(windowID string) {
	exec.Command("tmux", "set-option", "-p", "-t", windowID, "remain-on-exit", "on").Run()
}

// SetupPaneHooks installs a session-level after-split-window hook so that any
// new pane created inside an agent window automatically cds to the worktree
// root. Agent windows store their worktree path in the @ccmux_worktree window
// option (set by the launcher script); non-agent windows leave the option
// unset, so the hook is a no-op there.
func (m *Manager) SetupPaneHooks() {
	// tmux expands #{window_id} and #{pane_id} before passing the shell command
	// to run-shell, so the script receives the concrete IDs as arguments.
	// ##{pane_start_command} is escaped so it is expanded later, by
	// display-message, for the new pane: panes spawned with an explicit command
	// (e.g. `ccmux pane open <command>`) must not have "cd ..." typed into the
	// running program, so the cd is only sent to plain shell panes.
	hookCmd := `run-shell 'wdir=$(tmux show-options -w -t "#{window_id}" -qv @ccmux_worktree 2>/dev/null); startcmd=$(tmux display-message -p -t "#{pane_id}" "##{pane_start_command}"); if [ -n "$wdir" ] && [ -z "$startcmd" ]; then tmux send-keys -t "#{pane_id}" "cd $wdir" Enter; fi'`
	exec.Command("tmux", "set-hook", "-t", m.sessionName, "after-split-window", hookCmd).Run()
}

func (m *Manager) SetupAgentNavigation() {
	baseIdx := GetBaseIndex()
	firstWindow := fmt.Sprintf("%s:%d", m.sessionName, baseIdx)

	exec.Command("tmux", "bind-key", "-n", "F12",
		"if-shell", "-F",
		fmt.Sprintf("#{!=:#{window_index},%d}", baseIdx),
		fmt.Sprintf("select-window -t %s", firstWindow),
		"").Run()

	statusFmt := fmt.Sprintf(
		"#{?#{==:#{window_index},%d},, #[fg=colour245]F12: return to ccmux }",
		baseIdx,
	)
	exec.Command("tmux", "set-option", "-t", m.sessionName, "status-right", statusFmt).Run()
}

func (m *Manager) IsPaneDead(windowID string) (bool, error) {
	cmd := exec.Command("tmux", "display-message", "-t", windowID, "-p", "#{pane_dead}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("failed to check pane status: %s: %w", string(output), err)
	}
	return strings.TrimSpace(string(output)) == "1", nil
}

func (m *Manager) RespawnDeadPane(windowID, command string) error {
	cmd := exec.Command("tmux", "respawn-pane", "-t", windowID, command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to respawn pane: %s: %w", string(output), err)
	}
	return nil
}

func InsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

