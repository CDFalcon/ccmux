// Package tmux provides tmux session and window management.
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
	m.SetPaneRemainOnExit(m.FirstWindowTarget())
	if err := m.RespawnPane(m.FirstWindowTarget(), command); err != nil {
		return fmt.Errorf("failed to start command in session: %w", err)
	}
	return nil
}

// ForwardEnv sets environment variables from the current process into the tmux
// session so they are available to commands spawned inside it. This is necessary
// because the tmux server may have been started in a different environment.
func (m *Manager) ForwardEnv() {
	for _, entry := range os.Environ() {
		if idx := strings.Index(entry, "="); idx > 0 {
			key := entry[:idx]
			val := entry[idx+1:]
			exec.Command("tmux", "set-environment", "-t", m.sessionName, key, val).Run()
		}
	}
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

func (m *Manager) CreateWindow(workingDir, command, name string) (string, error) {
	cmd := exec.Command("tmux", "new-window", "-d", "-t", m.sessionName, "-c", workingDir, "-P", "-F", "#{window_id}", "-n", name, command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to create window: %s: %w", string(output), err)
	}
	windowID := strings.TrimSpace(string(output))
	m.SetPaneRemainOnExit(windowID)
	return windowID, nil
}

func (m *Manager) KillWindow(windowID string) error {
	cmd := exec.Command("tmux", "kill-window", "-t", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to kill window: %s: %w", string(output), err)
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

