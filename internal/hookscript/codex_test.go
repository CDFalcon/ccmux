package hookscript

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, dir string, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func extractInstalledHook(t *testing.T) string {
	t.Helper()
	start := strings.Index(InstallCodexPrePush, "<< 'CCMUXHOOK'\n")
	if start < 0 {
		t.Fatal("installer missing CCMUXHOOK heredoc start")
	}
	start += len("<< 'CCMUXHOOK'\n")
	end := strings.Index(InstallCodexPrePush[start:], "\nCCMUXHOOK")
	if end < 0 {
		t.Fatal("installer missing CCMUXHOOK heredoc end")
	}
	return InstallCodexPrePush[start : start+end]
}

func TestInstallCodexPrePush_ShouldBeValidBash(t *testing.T) {
	tmp := t.TempDir()

	outer := filepath.Join(tmp, "install.sh")
	if err := os.WriteFile(outer, []byte("#!/bin/bash\nset -e\nAGENT_ID=agent-1\nHARNESS=codex\n"+InstallCodexPrePush), 0755); err != nil {
		t.Fatalf("write outer script: %v", err)
	}
	if out, err := exec.Command("bash", "-n", outer).CombinedOutput(); err != nil {
		t.Fatalf("outer installer failed bash syntax check: %v\n%s", err, out)
	}

	inner := filepath.Join(tmp, "pre-push")
	if err := os.WriteFile(inner, []byte(extractInstalledHook(t)), 0755); err != nil {
		t.Fatalf("write inner hook: %v", err)
	}
	if out, err := exec.Command("bash", "-n", inner).CombinedOutput(); err != nil {
		t.Fatalf("inner hook failed bash syntax check: %v\n%s", err, out)
	}
}

func TestInstallCodexPrePush_ShouldInstallWrapperAndPreserveExistingHook(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "git", "init", "-q")

	hookPath := strings.TrimSpace(string(run(t, repo, "git", "rev-parse", "--git-path", "hooks/pre-push")))
	existingHook := filepath.Join(repo, hookPath)
	writeExecutable(t, existingHook, "#!/bin/sh\necho existing-hook\n")

	installer := filepath.Join(repo, "install.sh")
	writeExecutable(t, installer, "#!/bin/bash\nset -e\nAGENT_ID=agent-123\nHARNESS=codex\n"+InstallCodexPrePush)
	run(t, repo, "bash", installer)

	wrapper, err := os.ReadFile(existingHook)
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	if !strings.Contains(string(wrapper), "ccmux git pre-push hook") {
		t.Fatalf("installed hook should be the ccmux wrapper, got:\n%s", wrapper)
	}

	backup, err := os.ReadFile(existingHook + ".ccmux-user")
	if err != nil {
		t.Fatalf("read preserved user hook: %v", err)
	}
	if !strings.Contains(string(backup), "existing-hook") {
		t.Fatalf("user hook backup should preserve existing content, got:\n%s", backup)
	}

	agentID, err := os.ReadFile(existingHook + ".ccmux-agent-id")
	if err != nil {
		t.Fatalf("read hook agent id: %v", err)
	}
	if strings.TrimSpace(string(agentID)) != "agent-123" {
		t.Fatalf("agent id file = %q, want agent-123", strings.TrimSpace(string(agentID)))
	}
}

func TestInstallCodexPrePush_ShouldNotFailWhenHooksAreDisabled(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "git", "init", "-q")
	run(t, repo, "git", "config", "core.hooksPath", "/dev/null")

	installer := filepath.Join(repo, "install.sh")
	writeExecutable(t, installer, "#!/bin/bash\nset -e\nAGENT_ID=agent-disabled\nHARNESS=codex\n"+InstallCodexPrePush)
	run(t, repo, "bash", installer)
}

func TestInstalledCodexPrePushHook_ShouldRunCIWaitAfterRemoteShaMatches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook test is Unix-specific")
	}

	repo := t.TempDir()
	run(t, repo, "git", "init", "-q")

	installer := filepath.Join(repo, "install.sh")
	writeExecutable(t, installer, "#!/bin/bash\nset -e\nAGENT_ID=agent-456\nHARNESS=codex\n"+InstallCodexPrePush)
	run(t, repo, "bash", installer)

	hookPath := strings.TrimSpace(string(run(t, repo, "git", "rev-parse", "--git-path", "hooks/pre-push")))
	installedHook := filepath.Join(repo, hookPath)
	fakeBin := filepath.Join(repo, "fake-bin")
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}

	expectedSHA := "1111111111111111111111111111111111111111"
	marker := filepath.Join(repo, "ci-wait-called")
	writeExecutable(t, filepath.Join(fakeBin, "git"), "#!/bin/sh\nif [ \"$1\" = \"ls-remote\" ]; then\n  echo \""+expectedSHA+" refs/heads/feature\"\n  exit 0\nfi\nexec git \"$@\"\n")
	writeExecutable(t, filepath.Join(fakeBin, "ccmux"), "#!/bin/sh\necho \"$CCMUX_AGENT_ID $*\" > "+shellQuote(marker)+"\n")

	cmd := exec.Command(installedHook, "origin", "git@example.com:owner/repo.git")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader("refs/heads/feature " + expectedSHA + " refs/heads/feature 0000000000000000000000000000000000000000\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pre-push hook failed: %v\n%s", err, out)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(marker)
		if err == nil {
			got := strings.TrimSpace(string(data))
			if got != "agent-456 ci-wait" {
				t.Fatalf("ccmux invocation = %q, want %q", got, "agent-456 ci-wait")
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for hook to invoke ccmux ci-wait")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
