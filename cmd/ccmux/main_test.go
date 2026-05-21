package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/CDFalcon/ccmux/internal/harness"
)

// assertValidBash syntax-checks a generated script with `bash -n`.
func assertValidBash(t *testing.T, scriptPath string) {
	t.Helper()
	out, err := exec.Command("bash", "-n", scriptPath).CombinedOutput()
	if err != nil {
		t.Fatalf("generated script %s failed bash syntax check: %v\n%s", scriptPath, err, out)
	}
}

func TestWriteLauncherScript_ShouldProduceValidHarnessSpecificScript(t *testing.T) {
	for _, h := range harness.All() {
		t.Run(string(h), func(t *testing.T) {
			agentID := "test-" + string(h)
			path, err := writeLauncherScript(agentID, "do the thing", "/tmp/repo", "origin/main", "sess", false, "", "", "", h)
			if err != nil {
				t.Fatalf("writeLauncherScript failed: %v", err)
			}
			defer os.Remove(path)

			assertValidBash(t, path)

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read script: %v", err)
			}
			content := string(data)

			if !strings.Contains(content, h.StartCommand()) {
				t.Errorf("script for %s does not invoke its start command", h)
			}
			// The Claude hook block is gated behind the harness check; only
			// Claude should actually install hooks.
			hasHookInstall := strings.Contains(content, "Installing Claude Code hooks")
			if !hasHookInstall {
				t.Errorf("expected the (gated) Claude hook block to be present in the template")
			}
		})
	}
}

func TestWriteRecoveryScript_ShouldProduceValidHarnessSpecificScript(t *testing.T) {
	for _, h := range harness.All() {
		t.Run(string(h), func(t *testing.T) {
			agentID := "rec-" + string(h)
			path, err := writeRecoveryScript(agentID, "/tmp/repo/wt", "origin/main", "sess", "the original task", h)
			if err != nil {
				t.Fatalf("writeRecoveryScript failed: %v", err)
			}
			defer os.Remove(path)

			assertValidBash(t, path)

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read script: %v", err)
			}
			content := string(data)

			if !strings.Contains(content, h.ContinueCommand()) {
				t.Errorf("recovery script for %s does not invoke its continue command", h)
			}
			if !strings.Contains(content, "The original task was:") {
				t.Errorf("recovery script for %s should embed the original task", h)
			}
		})
	}
}
