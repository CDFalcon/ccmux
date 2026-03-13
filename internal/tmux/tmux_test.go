package tmux

import (
	"os/exec"
	"strings"
	"testing"
)

func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

func createTestSession(t *testing.T, name string) *Manager {
	t.Helper()
	mgr := NewManager(name)
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "-x", "80", "-y", "24")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create test session %s: %s: %v", name, string(out), err)
	}
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", name).Run()
	})
	return mgr
}

func getWindowOption(t *testing.T, target, option string) string {
	t.Helper()
	cmd := exec.Command("tmux", "show-options", "-w", "-t", target, option)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func getPaneOption(t *testing.T, target, option string) string {
	t.Helper()
	cmd := exec.Command("tmux", "show-options", "-p", "-t", target, option)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func getHook(t *testing.T, session, hook string) string {
	t.Helper()
	cmd := exec.Command("tmux", "show-hooks", "-t", session)
	out, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, hook) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func TestRemoveRemainOnExitHook_ShouldRemoveStaleHook_GivenSessionWithHook(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-remove-hook")
	exec.Command("tmux", "set-hook", "-t", mgr.SessionName(), "after-new-window", "set-option -w remain-on-exit on").Run()
	hook := getHook(t, mgr.SessionName(), "after-new-window")
	if hook == "" {
		t.Fatal("expected after-new-window hook to be set during setup")
	}

	// Execute.
	mgr.RemoveRemainOnExitHook()

	// Assert.
	hook = getHook(t, mgr.SessionName(), "after-new-window")
	if hook != "" {
		t.Errorf("expected after-new-window hook to be removed, got: %s", hook)
	}
}

func TestEnsureRemainOnExit_ShouldRemoveStaleHook_GivenSessionWithHook(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-ensure-roe")
	exec.Command("tmux", "set-hook", "-t", mgr.SessionName(), "after-new-window", "set-option -w remain-on-exit on").Run()

	// Execute.
	mgr.EnsureRemainOnExit()

	// Assert.
	hook := getHook(t, mgr.SessionName(), "after-new-window")
	if hook != "" {
		t.Errorf("expected after-new-window hook to be removed, got: %s", hook)
	}
}

func TestEnsureRemainOnExit_ShouldSetRemainOnExitOnFirstPane_GivenCleanSession(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-ensure-first")

	// Execute.
	mgr.EnsureRemainOnExit()

	// Assert.
	opt := getPaneOption(t, mgr.FirstWindowTarget(), "remain-on-exit")
	if !strings.Contains(opt, "on") {
		t.Errorf("expected remain-on-exit on for first pane, got: %q", opt)
	}
}

func TestNewWindow_ShouldNotInheritRemainOnExit_GivenHookRemoved(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-no-inherit")
	exec.Command("tmux", "set-hook", "-t", mgr.SessionName(), "after-new-window", "set-option -w remain-on-exit on").Run()
	mgr.EnsureRemainOnExit()

	// Execute.
	cmd := exec.Command("tmux", "new-window", "-d", "-t", mgr.SessionName(), "-P", "-F", "#{window_id}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to create new window: %s: %v", string(out), err)
	}
	newWindowID := strings.TrimSpace(string(out))

	// Assert.
	opt := getWindowOption(t, newWindowID, "remain-on-exit")
	if strings.Contains(opt, "on") {
		t.Errorf("user-created window should not have remain-on-exit, got: %q", opt)
	}
}

func TestCreateWindow_ShouldSetRemainOnExitOnPane_GivenAgentWindow(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-agent-roe")

	// Execute.
	windowID, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
	if err != nil {
		t.Fatalf("failed to create window: %v", err)
	}

	// Assert.
	opt := getPaneOption(t, windowID, "remain-on-exit")
	if !strings.Contains(opt, "on") {
		t.Errorf("agent pane should have remain-on-exit on, got: %q", opt)
	}
}

func TestSplitPane_ShouldNotInheritRemainOnExit_GivenAgentWindowWithRemainOnExit(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-split-roe")
	windowID, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
	if err != nil {
		t.Fatalf("failed to create window: %v", err)
	}

	// Execute.
	cmd := exec.Command("tmux", "split-window", "-d", "-t", windowID, "-P", "-F", "#{pane_id}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to split pane: %s: %v", string(out), err)
	}
	splitPaneID := strings.TrimSpace(string(out))

	// Assert.
	opt := getPaneOption(t, splitPaneID, "remain-on-exit")
	if strings.Contains(opt, "on") {
		t.Errorf("split pane should not have remain-on-exit, got: %q", opt)
	}
}
