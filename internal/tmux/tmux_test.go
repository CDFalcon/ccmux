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

func TestNewWindow_ShouldNotInheritRemainOnExit_GivenSessionLevelRemainOnExit(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-session-roe")
	exec.Command("tmux", "set-option", "-t", mgr.SessionName(), "remain-on-exit", "on").Run()
	mgr.EnsureRemainOnExit()

	// Execute.
	cmd := exec.Command("tmux", "new-window", "-d", "-t", mgr.SessionName(), "-P", "-F", "#{pane_id}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to create new window: %s: %v", string(out), err)
	}
	newPaneID := strings.TrimSpace(string(out))

	// Assert.
	opt := getPaneOption(t, newPaneID, "remain-on-exit")
	if strings.Contains(opt, "on") {
		t.Errorf("user-created window pane should not have remain-on-exit, got: %q", opt)
	}
}


func TestCreateWindow_ShouldSetRemainOnExitOnPane_GivenAgentWindow(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-agent-roe")

	// Execute.
	windowID, _, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
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
	windowID, _, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
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

func TestCreateWindow_ShouldReturnPaneID_GivenNewWindow(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-pane-id")

	// Execute.
	_, paneID, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
	if err != nil {
		t.Fatalf("failed to create window: %v", err)
	}

	// Assert.
	if paneID == "" {
		t.Error("expected non-empty pane ID")
	}
	if !strings.HasPrefix(paneID, "%") {
		t.Errorf("expected pane ID to start with %%, got: %q", paneID)
	}
}

func showSessionEnv(t *testing.T, session, name string) string {
	t.Helper()
	out, _ := exec.Command("tmux", "show-environment", "-t", session, name).CombinedOutput()
	return strings.TrimSpace(string(out))
}

func TestForwardEnv_ShouldForwardCurrentVars_GivenVarSetInProcessEnv(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-fwd-env")
	t.Setenv("CCMUX_TEST_FORWARD", "fresh-value")

	// Execute.
	mgr.ForwardEnv()

	// Assert.
	got := showSessionEnv(t, mgr.SessionName(), "CCMUX_TEST_FORWARD")
	if got != "CCMUX_TEST_FORWARD=fresh-value" {
		t.Errorf("expected forwarded variable, got: %q", got)
	}
}

func TestForwardEnv_ShouldMarkStaleSessionVarForRemoval_GivenVarNotInProcessEnv(t *testing.T) {
	skipIfNoTmux(t)

	// Setup. A leftover session variable from a previous launch that the
	// current (launching) shell does not have.
	mgr := createTestSession(t, "ccmux-test-stale-session")
	exec.Command("tmux", "set-environment", "-t", mgr.SessionName(), "CCMUX_TEST_STALE_SESSION", "old-value").Run()

	// Execute.
	mgr.ForwardEnv()

	// Assert.
	got := showSessionEnv(t, mgr.SessionName(), "CCMUX_TEST_STALE_SESSION")
	if got != "-CCMUX_TEST_STALE_SESSION" {
		t.Errorf("expected stale session variable to be marked for removal, got: %q", got)
	}
}

func TestForwardEnv_ShouldMaskStaleGlobalVar_GivenVarOnlyInServerGlobalEnv(t *testing.T) {
	skipIfNoTmux(t)

	// Setup. Simulates a tmux server started from an old shell whose global
	// environment still carries a variable the relaunching shell lacks.
	mgr := createTestSession(t, "ccmux-test-stale-global")
	exec.Command("tmux", "set-environment", "-g", "CCMUX_TEST_STALE_GLOBAL", "old-value").Run()
	t.Cleanup(func() {
		exec.Command("tmux", "set-environment", "-gu", "CCMUX_TEST_STALE_GLOBAL").Run()
	})

	// Execute.
	mgr.ForwardEnv()

	// Assert.
	got := showSessionEnv(t, mgr.SessionName(), "CCMUX_TEST_STALE_GLOBAL")
	if got != "-CCMUX_TEST_STALE_GLOBAL" {
		t.Errorf("expected stale global variable to be masked in session env, got: %q", got)
	}
}

func TestForwardEnv_ShouldOverwriteStaleValue_GivenVarChangedInProcessEnv(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-overwrite")
	exec.Command("tmux", "set-environment", "-t", mgr.SessionName(), "CCMUX_TEST_CHANGED", "old-value").Run()
	t.Setenv("CCMUX_TEST_CHANGED", "new-value")

	// Execute.
	mgr.ForwardEnv()

	// Assert.
	got := showSessionEnv(t, mgr.SessionName(), "CCMUX_TEST_CHANGED")
	if got != "CCMUX_TEST_CHANGED=new-value" {
		t.Errorf("expected variable to be overwritten with current value, got: %q", got)
	}
}

func TestStaleEnvNames_ShouldSkipTmuxManagedVars_GivenEmptyCurrentEnv(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-skip-managed")
	exec.Command("tmux", "set-environment", "-t", mgr.SessionName(), "TMUX_PANE", "%0").Run()

	// Execute.
	stale := mgr.staleEnvNames(map[string]string{})

	// Assert.
	for _, name := range stale {
		if name == "TMUX" || name == "TMUX_PANE" {
			t.Errorf("tmux-managed variable %s should never be marked stale", name)
		}
	}
}

func TestParseEnvName(t *testing.T) {
	tests := []struct {
		line     string
		wantName string
		wantOK   bool
	}{
		{"FOO=bar", "FOO", true},
		{"FOO=", "FOO", true},
		{"_UNDERSCORE=x", "_UNDERSCORE", true},
		{"VAR2=x", "VAR2", true},
		{"-REMOVED", "REMOVED", true},
		{"", "", false},
		{"no-equals-sign", "", false},
		{"=value", "", false},
		{"2BAD=x", "", false},
		{"has space=x", "", false},
		{"-not a name", "", false},
		{"continuation line of a multi-line value", "", false},
	}
	for _, tt := range tests {
		name, ok := parseEnvName(tt.line)
		if name != tt.wantName || ok != tt.wantOK {
			t.Errorf("parseEnvName(%q) = (%q, %v), want (%q, %v)", tt.line, name, ok, tt.wantName, tt.wantOK)
		}
	}
}

func TestSplitPaneBelow_ShouldCreatePaneInSameWindow_GivenAgentPane(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-split-below")
	windowID, agentPaneID, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
	if err != nil {
		t.Fatalf("failed to create window: %v", err)
	}

	// Execute.
	paneID, err := mgr.SplitPaneBelow(agentPaneID, "/tmp", "sleep 60")
	if err != nil {
		t.Fatalf("failed to split pane below: %v", err)
	}

	// Assert.
	if !strings.HasPrefix(paneID, "%") {
		t.Errorf("expected pane ID to start with %%, got: %q", paneID)
	}
	gotWindow, err := mgr.GetPaneWindowID(paneID)
	if err != nil {
		t.Fatalf("failed to get window for new pane: %v", err)
	}
	if gotWindow != windowID {
		t.Errorf("expected new pane in window %s, got %s", windowID, gotWindow)
	}
	out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_at_bottom}").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to query pane_at_bottom: %s: %v", string(out), err)
	}
	if strings.TrimSpace(string(out)) != "1" {
		t.Errorf("expected new pane at the bottom of the window, got pane_at_bottom=%q", strings.TrimSpace(string(out)))
	}
}

func TestSplitPaneBelow_ShouldOpenShell_GivenNoCommand(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-split-shell")
	_, agentPaneID, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
	if err != nil {
		t.Fatalf("failed to create window: %v", err)
	}

	// Execute.
	paneID, err := mgr.SplitPaneBelow(agentPaneID, "/tmp", "")
	if err != nil {
		t.Fatalf("failed to split pane below: %v", err)
	}

	// Assert: a shell pane has an empty start command.
	out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_start_command}").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to query pane_start_command: %s: %v", string(out), err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected empty start command for shell pane, got: %q", strings.TrimSpace(string(out)))
	}
}

func TestPaneExists_ShouldReportLifecycle_GivenPaneKilled(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-pane-exists")
	_, agentPaneID, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
	if err != nil {
		t.Fatalf("failed to create window: %v", err)
	}
	paneID, err := mgr.SplitPaneBelow(agentPaneID, "/tmp", "sleep 60")
	if err != nil {
		t.Fatalf("failed to split pane below: %v", err)
	}

	// Assert alive.
	if !mgr.PaneExists(paneID) {
		t.Errorf("expected pane %s to exist", paneID)
	}

	// Execute.
	if err := mgr.KillPane(paneID); err != nil {
		t.Fatalf("failed to kill pane: %v", err)
	}

	// Assert gone.
	if mgr.PaneExists(paneID) {
		t.Errorf("expected pane %s to no longer exist", paneID)
	}
}

func TestWindowOptions_ShouldRoundTrip_GivenSetGetUnset(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-win-opts")
	windowID, _, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
	if err != nil {
		t.Fatalf("failed to create window: %v", err)
	}

	// Execute + assert set/get.
	if err := mgr.SetWindowOption(windowID, "@ccmux_share_pane", "%99"); err != nil {
		t.Fatalf("failed to set window option: %v", err)
	}
	val, err := mgr.GetWindowOption(windowID, "@ccmux_share_pane")
	if err != nil {
		t.Fatalf("failed to get window option: %v", err)
	}
	if val != "%99" {
		t.Errorf("expected option value %%99, got: %q", val)
	}

	// Execute + assert unset.
	if err := mgr.UnsetWindowOption(windowID, "@ccmux_share_pane"); err != nil {
		t.Fatalf("failed to unset window option: %v", err)
	}
	val, err = mgr.GetWindowOption(windowID, "@ccmux_share_pane")
	if err != nil {
		t.Fatalf("failed to get window option after unset: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty option after unset, got: %q", val)
	}
}

func TestRespawnPaneCmd_ShouldRestartPane_GivenNewCommand(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-respawn-cmd")
	_, agentPaneID, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
	if err != nil {
		t.Fatalf("failed to create window: %v", err)
	}
	paneID, err := mgr.SplitPaneBelow(agentPaneID, "/tmp", "sleep 60")
	if err != nil {
		t.Fatalf("failed to split pane below: %v", err)
	}

	// Execute.
	if err := mgr.RespawnPaneCmd(paneID, "sleep 30"); err != nil {
		t.Fatalf("failed to respawn pane with command: %v", err)
	}

	// Assert.
	out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_start_command}").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to query pane_start_command: %s: %v", string(out), err)
	}
	if !strings.Contains(string(out), "sleep 30") {
		t.Errorf("expected respawned pane running 'sleep 30', got: %q", strings.TrimSpace(string(out)))
	}
}

func TestSetupPaneHooks_ShouldGuardOnStartCommand_GivenHookInstalled(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-hook-guard")

	// Execute.
	mgr.SetupPaneHooks()

	// Assert: the hook must only send `cd` into plain shell panes, so panes
	// spawned with an explicit command don't get keys typed into the program.
	hook := getHook(t, mgr.SessionName(), "after-split-window")
	if hook == "" {
		t.Fatal("expected after-split-window hook to be installed")
	}
	if !strings.Contains(hook, "pane_start_command") {
		t.Errorf("expected hook to guard on pane_start_command, got: %s", hook)
	}
}

func TestKillPane_ShouldPreserveOtherPanes_GivenWindowWithSplitPanes(t *testing.T) {
	skipIfNoTmux(t)

	// Setup.
	mgr := createTestSession(t, "ccmux-test-kill-pane")
	windowID, agentPaneID, err := mgr.CreateWindow("/tmp", "sleep 60", "test-agent")
	if err != nil {
		t.Fatalf("failed to create window: %v", err)
	}
	splitCmd := exec.Command("tmux", "split-window", "-d", "-t", windowID, "-P", "-F", "#{pane_id}", "sleep 60")
	out, err := splitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to split pane: %s: %v", string(out), err)
	}
	userPaneID := strings.TrimSpace(string(out))

	// Execute.
	err = mgr.KillPane(agentPaneID)
	if err != nil {
		t.Fatalf("failed to kill pane: %v", err)
	}

	// Assert.
	checkCmd := exec.Command("tmux", "display-message", "-t", userPaneID, "-p", "#{pane_id}")
	checkOut, checkErr := checkCmd.CombinedOutput()
	if checkErr != nil {
		t.Errorf("user pane %s should still exist after killing agent pane, but got error: %v", userPaneID, checkErr)
	}
	if strings.TrimSpace(string(checkOut)) != userPaneID {
		t.Errorf("expected user pane %s to still exist, got: %q", userPaneID, string(checkOut))
	}
}
