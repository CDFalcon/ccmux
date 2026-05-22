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
			path, err := writeLauncherScript(agentID, "do the thing", "/tmp/repo", "origin/main", "sess", false, "", "", "", h, true)
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

func TestWriteLauncherScript_ShouldReflectDraftPRsSetting(t *testing.T) {
	cases := map[bool]string{
		true:  "DRAFT_PRS='1'",
		false: "DRAFT_PRS='0'",
	}
	for draftPRs, want := range cases {
		path, err := writeLauncherScript("draft-test", "task", "/tmp/repo", "origin/main", "sess", false, "", "", "", harness.Default, draftPRs)
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

		if !strings.Contains(content, want) {
			t.Errorf("launcher script with draftPRs=%v should contain %q", draftPRs, want)
		}
		// The agent's gh pr create instruction picks up --draft at runtime
		// via the PR_DRAFT_FLAG shell variable.
		if !strings.Contains(content, "gh pr create ${PR_DRAFT_FLAG}--base") {
			t.Error("launcher script should build the gh pr create command from PR_DRAFT_FLAG")
		}
	}
}

func TestOptionalArg_ShouldTreatDashAsDefault(t *testing.T) {
	cases := map[string]string{
		"-":       "",
		"":        "",
		"claude":  "claude",
		"codex":   "codex",
		"origin/m": "origin/m",
	}
	for in, want := range cases {
		if got := optionalArg(in); got != want {
			t.Errorf("optionalArg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTaskCmd_ShouldAcceptTwoToFivePositionalArgs(t *testing.T) {
	cmd := taskCmd()

	if cmd.Use == "" || !strings.HasPrefix(cmd.Use, "task ") {
		t.Errorf("taskCmd Use = %q, want it to start with %q", cmd.Use, "task ")
	}
	if cmd.Hidden {
		t.Error("taskCmd should be visible so agents can discover it via --help")
	}

	cases := []struct {
		args    []string
		wantErr bool
	}{
		{[]string{"proj"}, true},
		{[]string{"proj", "desc"}, false},
		{[]string{"proj", "desc", "claude"}, false},
		{[]string{"proj", "desc", "claude", "origin/main"}, false},
		{[]string{"proj", "desc", "claude", "origin/main", "branch"}, false},
		{[]string{"proj", "desc", "claude", "origin/main", "branch", "extra"}, true},
	}
	for _, tc := range cases {
		err := cmd.Args(cmd, tc.args)
		if tc.wantErr && err == nil {
			t.Errorf("taskCmd.Args(%v) = nil, want error", tc.args)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("taskCmd.Args(%v) = %v, want nil", tc.args, err)
		}
	}
}

// newRepoWithOriginBranch builds a throwaway git repo whose "origin" remote
// exposes exactly one branch, and returns the repo path.
func newRepoWithOriginBranch(t *testing.T, branch string) string {
	t.Helper()

	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	originDir := t.TempDir()
	runGit(originDir, "init", "-q")
	runGit(originDir, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	runGit(originDir, "config", "user.email", "test@test.com")
	runGit(originDir, "config", "user.name", "test")
	runGit(originDir, "commit", "--allow-empty", "-q", "-m", "init")

	repoDir := t.TempDir()
	runGit(repoDir, "init", "-q")
	runGit(repoDir, "remote", "add", "origin", originDir)

	return repoDir
}

func TestVerifyBaseBranch_ShouldAcceptExistingBranch(t *testing.T) {
	repo := newRepoWithOriginBranch(t, "master")

	// Both the "origin/"-prefixed and bare forms should resolve, since the
	// launcher strips the prefix before fetching.
	for _, ref := range []string{"origin/master", "master"} {
		if err := verifyBaseBranch("proj", repo, ref); err != nil {
			t.Errorf("verifyBaseBranch(%q) = %v, want nil", ref, err)
		}
	}
}

func TestVerifyBaseBranch_ShouldRejectMissingBranch(t *testing.T) {
	repo := newRepoWithOriginBranch(t, "master")

	err := verifyBaseBranch("proj", repo, "origin/main")
	if err == nil {
		t.Fatal("verifyBaseBranch(origin/main) = nil, want error for a repo with only master")
	}
	// The error should be actionable: name the bad branch and list what is
	// actually available so a calling agent can self-correct.
	if !strings.Contains(err.Error(), "origin/main") || !strings.Contains(err.Error(), "master") {
		t.Errorf("error %q should mention the missing branch and the available ones", err)
	}
}

func TestWriteRecoveryScript_ShouldProduceValidHarnessSpecificScript(t *testing.T) {
	for _, h := range harness.All() {
		t.Run(string(h), func(t *testing.T) {
			agentID := "rec-" + string(h)
			path, err := writeRecoveryScript(agentID, "/tmp/repo/wt", "origin/main", "sess", "the original task", h, true)
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
