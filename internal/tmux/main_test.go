package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// anchorSession keeps the test tmux server alive for the whole package run.
//
// Every test creates its own session and kills it on cleanup. Without an
// anchor, killing the last session shuts the server down, and the next
// test's new-session races the exiting server: tmux reports "server exited
// unexpectedly" and the test fails. Locally a developer's own tmux server
// usually masks this; in CI there is no other server, so it flakes.
const anchorSession = "ccmux-test-anchor"

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("tmux"); err != nil {
		os.Exit(m.Run())
	}

	// Run on a private tmux server so tests neither touch the developer's
	// live server (some tests set global options and environment) nor
	// depend on one existing. TMUX_TMPDIR moves the default socket;
	// TMUX must be unset or tmux would use the enclosing server's socket
	// from it instead.
	dir, err := os.MkdirTemp("", "ccmux-tmux-test-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create tmux temp dir: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("TMUX_TMPDIR", dir)
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")

	start := exec.Command("tmux", "new-session", "-d", "-s", anchorSession, "-x", "80", "-y", "24")
	if out, err := start.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "start test tmux server: %s: %v\n", out, err)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()

	exec.Command("tmux", "kill-server").Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
