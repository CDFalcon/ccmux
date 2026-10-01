package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/CDFalcon/ccmux/internal/harness"
	"github.com/spf13/cobra"
)

// runOpenCodeCmd is the hidden wrapper every OpenCode launch goes through
// (see internal/harness/opencode.go for why the launcher scripts cannot call
// `opencode` directly). It prepares the plugin, config and session id, then
// replaces itself with opencode so the pane's process — and the exit code the
// launcher's ExitCapture block records — is opencode's own.
func runOpenCodeCmd() *cobra.Command {
	var resume bool
	var prompt string

	cmd := &cobra.Command{
		Use:    "run-opencode",
		Hidden: true,
		Short:  "Launch OpenCode for the current ccmux agent",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			launch, err := harness.PrepareOpenCodeLaunch(os.Getenv("CCMUX_AGENT_ID"), resume, prompt, os.Environ())
			if err != nil {
				return err
			}
			bin, err := exec.LookPath("opencode")
			if err != nil {
				return fmt.Errorf("opencode is not installed (no \"opencode\" on PATH): %w", err)
			}
			return syscall.Exec(bin, launch.Args, launch.Env)
		},
	}

	cmd.Flags().BoolVar(&resume, "resume", false, "Resume the agent's recorded OpenCode session (falls back to a fresh one)")
	cmd.Flags().StringVar(&prompt, "prompt", "", "First message: the task for a fresh session, the follow-up for a resumed one")
	return cmd
}
