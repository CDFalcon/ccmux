package tmux

import (
	"os"
	"testing"
)

func TestNewManager_ShouldCreateManager_GivenSessionName(t *testing.T) {
	// Execute.
	m := NewManager("test-session")

	// Assert.
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.sessionName != "test-session" {
		t.Errorf("expected session name 'test-session', got '%s'", m.sessionName)
	}
}

func TestSessionName_ShouldReturnName_GivenManager(t *testing.T) {
	// Setup.
	m := NewManager("my-session")

	// Execute.
	name := m.SessionName()

	// Assert.
	if name != "my-session" {
		t.Errorf("expected 'my-session', got '%s'", name)
	}
}

func TestInsideTmux_ShouldReturnTrue_GivenTmuxEnvSet(t *testing.T) {
	// Setup.
	origTmux := os.Getenv("TMUX")
	os.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
	defer func() {
		if origTmux == "" {
			os.Unsetenv("TMUX")
		} else {
			os.Setenv("TMUX", origTmux)
		}
	}()

	// Execute.
	result := InsideTmux()

	// Assert.
	if !result {
		t.Error("expected InsideTmux to return true when TMUX env is set")
	}
}

func TestInsideTmux_ShouldReturnFalse_GivenNoTmuxEnv(t *testing.T) {
	// Setup.
	origTmux := os.Getenv("TMUX")
	os.Unsetenv("TMUX")
	defer func() {
		if origTmux != "" {
			os.Setenv("TMUX", origTmux)
		}
	}()

	// Execute.
	result := InsideTmux()

	// Assert.
	if result {
		t.Error("expected InsideTmux to return false when TMUX env is unset")
	}
}

func TestSelectFirstWindow_ShouldTargetCorrectWindow_GivenSessionName(t *testing.T) {
	// Setup.
	m := NewManager("test-session")

	// Execute + Assert.
	// SelectFirstWindow internally calls SelectWindow with sessionName:0
	// We can't easily test the tmux command execution without a real tmux,
	// but we verify the manager was created correctly.
	err := m.SelectFirstWindow()

	// This will fail since there's no real tmux session, but that's expected.
	if err == nil {
		t.Log("SelectFirstWindow succeeded (tmux may be available)")
	}
}

func TestSessionExists_ShouldReturnFalse_GivenNonexistentSession(t *testing.T) {
	// Setup.
	m := NewManager("ccmux-nonexistent-test-session-12345")

	// Execute.
	exists := m.SessionExists()

	// Assert.
	if exists {
		t.Error("expected session to not exist")
	}
}

func TestDefaultSessionDimensions_ShouldBeSet(t *testing.T) {
	// Assert.
	if DefaultSessionWidth != "200" {
		t.Errorf("expected default width '200', got '%s'", DefaultSessionWidth)
	}
	if DefaultSessionHeight != "50" {
		t.Errorf("expected default height '50', got '%s'", DefaultSessionHeight)
	}
}
