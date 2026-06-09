package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/harness"
	"github.com/CDFalcon/ccmux/internal/queue"
)

// assertValidBash syntax-checks a generated script with `bash -n`.
func assertValidBash(t *testing.T, scriptPath string) {
	t.Helper()
	out, err := exec.Command("bash", "-n", scriptPath).CombinedOutput()
	if err != nil {
		t.Fatalf("generated script %s failed bash syntax check: %v\n%s", scriptPath, err, out)
	}
}

func TestUpsertCodexProjectTrust_ShouldAppendMissingProject(t *testing.T) {
	projectPath := "/tmp/ccmux-test"
	got := upsertCodexProjectTrust("model = \"gpt-5.5\"\n", projectPath)

	header := codexProjectTableHeader(projectPath)
	if !strings.Contains(got, header+"\ntrust_level = \"trusted\"") {
		t.Fatalf("updated config should append trusted project section, got:\n%s", got)
	}
	if strings.Count(got, header) != 1 {
		t.Fatalf("updated config should contain one project table, got:\n%s", got)
	}
}

func TestUpsertCodexProjectTrust_ShouldUpdateExistingTrustLevel(t *testing.T) {
	projectPath := "/tmp/ccmux-test"
	header := codexProjectTableHeader(projectPath)
	config := "model = \"gpt-5.5\"\n\n" + header + "\ntrust_level = \"untrusted\"\nfoo = \"bar\"\n\n[features]\njs_repl = false\n"

	got := upsertCodexProjectTrust(config, projectPath)

	if strings.Contains(got, `trust_level = "untrusted"`) {
		t.Fatalf("updated config should remove untrusted state, got:\n%s", got)
	}
	if !strings.Contains(got, header+"\ntrust_level = \"trusted\"\nfoo = \"bar\"") {
		t.Fatalf("updated config should preserve project settings while trusting it, got:\n%s", got)
	}
	if !strings.Contains(got, "[features]\njs_repl = false") {
		t.Fatalf("updated config should preserve following sections, got:\n%s", got)
	}
}

func TestUpsertCodexProjectTrust_ShouldNotRewriteTrustLevelPrefixKeys(t *testing.T) {
	projectPath := "/tmp/ccmux-test"
	header := codexProjectTableHeader(projectPath)
	config := header + "\ntrust_level_hint = \"keep\"\n"

	got := upsertCodexProjectTrust(config, projectPath)

	if !strings.Contains(got, header+"\ntrust_level = \"trusted\"\ntrust_level_hint = \"keep\"") {
		t.Fatalf("updated config should insert trust_level without replacing prefix keys, got:\n%s", got)
	}
}

func TestUpsertCodexProjectTrust_ShouldEscapeProjectPathInTableHeader(t *testing.T) {
	projectPath := `/tmp/ccmux-"quoted"\path`
	got := upsertCodexProjectTrust("", projectPath)

	wantHeader := `[projects."/tmp/ccmux-\"quoted\"\\path"]`
	if !strings.Contains(got, wantHeader) {
		t.Fatalf("trusted project header should be TOML-escaped as %q, got:\n%s", wantHeader, got)
	}
}

func TestTrustCodexProject_ShouldWriteConfigUnderCodexHome(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	projectPath := filepath.Join(codexHome, "repo")

	if err := trustCodexProject(projectPath); err != nil {
		t.Fatalf("trustCodexProject: %v", err)
	}
	if err := trustCodexProject(projectPath); err != nil {
		t.Fatalf("trustCodexProject second call: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	header := codexProjectTableHeader(projectPath)
	if strings.Count(content, header) != 1 {
		t.Fatalf("config should contain one trusted project table, got:\n%s", content)
	}
	if !strings.Contains(content, header+"\ntrust_level = \"trusted\"") {
		t.Fatalf("config should mark project trusted, got:\n%s", content)
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
			if !strings.Contains(content, `if [ "$HARNESS" = "codex" ]; then`) ||
				!strings.Contains(content, `ccmux trust-codex-project "$WORKTREE_PATH"`) {
				t.Errorf("script for %s should include the Codex trust gate", h)
			}
			if !strings.Contains(content, "hooks/pre-push") ||
				!strings.Contains(content, "ccmux git pre-push hook") {
				t.Errorf("script for %s should include the Codex Git push hook installer", h)
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
	cases := []struct {
		draftPRs bool
		wantFlag string // the DRAFT_PRS shell assignment
		wantNote string // an explicit phrase the agent must see
	}{
		{true, "DRAFT_PRS='1'", "keep the --draft flag"},
		{false, "DRAFT_PRS='0'", "do NOT add a --draft flag"},
	}
	for _, tc := range cases {
		path, err := writeLauncherScript("draft-test", "task", "/tmp/repo", "origin/main", "sess", false, "", "", "", harness.Default, tc.draftPRs)
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

		if !strings.Contains(content, tc.wantFlag) {
			t.Errorf("launcher script with draftPRs=%v should contain %q", tc.draftPRs, tc.wantFlag)
		}
		// The agent's gh pr create instruction picks up --draft at runtime
		// via the PR_DRAFT_FLAG shell variable.
		if !strings.Contains(content, "gh pr create ${PR_DRAFT_FLAG}--base") {
			t.Error("launcher script should build the gh pr create command from PR_DRAFT_FLAG")
		}
		// Omitting --draft from the example command is too implicit: agents
		// re-add it from habit. The script must also state the intent
		// explicitly so a disabled setting is actually honoured.
		if !strings.Contains(content, tc.wantNote) {
			t.Errorf("launcher script with draftPRs=%v should explicitly instruct the agent: %q", tc.draftPRs, tc.wantNote)
		}
	}
}

// TestWriteLauncherScript_ShouldWireOTelEnv_GivenClaudeHarness pins the
// OpenTelemetry export block in the generated launcher script. The TUI's
// in-process collector relies on every spawned claude session pointing its
// OTLP exporter at the local endpoint with ccmux.agent.id stamped on the
// resource block — without it, cost.usage metrics would be unattributable
// and the displayed cost would silently fall back to the JSONL estimate.
func TestWriteLauncherScript_ShouldWireOTelEnv_GivenClaudeHarness(t *testing.T) {
	// Setup.
	agentID := "otel-test-agent"
	path, err := writeLauncherScript(agentID, "task", "/tmp/repo", "origin/main", "sess", false, "", "", "", harness.Claude, true)
	if err != nil {
		t.Fatalf("writeLauncherScript: %v", err)
	}
	defer os.Remove(path)

	assertValidBash(t, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	content := string(data)

	// Assert. The literal env exports the OTel SDK consumes.
	wantExports := []string{
		`export CLAUDE_CODE_ENABLE_TELEMETRY=1`,
		`export OTEL_METRICS_EXPORTER=otlp`,
		`export OTEL_EXPORTER_OTLP_PROTOCOL=http/json`,
		`export OTEL_EXPORTER_OTLP_ENDPOINT=`,
		`export OTEL_RESOURCE_ATTRIBUTES="ccmux.agent.id=$AGENT_ID,ccmux.worktree.path=$WORKTREE_PATH"`,
	}
	for _, want := range wantExports {
		if !strings.Contains(content, want) {
			t.Errorf("launcher script missing required OTel export: %q", want)
		}
	}

	// The block must be gated on (1) Claude harness, (2) no pre-existing
	// OTEL_EXPORTER_OTLP_ENDPOINT (don't clobber the user's setup),
	// (3) the endpoint advertisement file being readable.
	if !strings.Contains(content, `[ "$HARNESS" = "claude" ]`) {
		t.Error("OTel block should be gated on HARNESS=claude")
	}
	if !strings.Contains(content, `[ -z "${OTEL_EXPORTER_OTLP_ENDPOINT:-}" ]`) {
		t.Error("OTel block should respect a user-set OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if !strings.Contains(content, `[ -r "$HOME/.ccmux/otel-endpoint" ]`) {
		t.Error("OTel block should require the endpoint advertisement file")
	}
}

// TestWriteLauncherScript_ShouldNotWireOTelEnv_GivenCodexHarness guards
// the Codex carve-out: Codex CLI does not currently emit OTel metrics, so
// injecting CLAUDE_CODE_ENABLE_TELEMETRY (and friends) into its env would
// be noise at best, an Anthropic-specific feature flag in a non-Anthropic
// process at worst.
func TestWriteLauncherScript_ShouldNotWireOTelEnv_GivenCodexHarness(t *testing.T) {
	// Setup.
	path, err := writeLauncherScript("codex-test", "task", "/tmp/repo", "origin/main", "sess", false, "", "", "", harness.Codex, true)
	if err != nil {
		t.Fatalf("writeLauncherScript: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	content := string(data)

	// Assert. The block is in the template but its enclosing `if` predicate
	// ('"$HARNESS" = "claude"') keeps it dormant for Codex. The script must
	// not unconditionally export Claude-specific env.
	if !strings.Contains(content, `HARNESS='codex'`) {
		t.Fatal("expected codex harness assignment in script — test fixture has drifted")
	}
	// The OTel export should remain gated; what we're really asserting is
	// that the gate is on HARNESS rather than something that's always true
	// for Codex too.
	if !strings.Contains(content, `[ "$HARNESS" = "claude" ] && [ -z "${OTEL_EXPORTER_OTLP_ENDPOINT:-}" ]`) {
		t.Error("OTel block must be gated on HARNESS=claude so Codex doesn't pick it up")
	}
}

func TestOptionalArg_ShouldTreatDashAsDefault(t *testing.T) {
	cases := map[string]string{
		"-":        "",
		"":         "",
		"claude":   "claude",
		"codex":    "codex",
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
			if !strings.Contains(content, `if [ "$HARNESS" = "codex" ]; then`) ||
				!strings.Contains(content, `ccmux trust-codex-project "$WORKTREE_PATH"`) {
				t.Errorf("recovery script for %s should include the Codex trust gate", h)
			}
			if !strings.Contains(content, "hooks/pre-push") ||
				!strings.Contains(content, "ccmux git pre-push hook") {
				t.Errorf("recovery script for %s should include the Codex Git push hook installer", h)
			}
			if !strings.Contains(content, "The original task was:") {
				t.Errorf("recovery script for %s should embed the original task", h)
			}
		})
	}
}

// setupStopHookTestStores points HOME at a tmpdir and returns a fresh
// agent.Store + queue.Queue rooted there. Used by the handleAgentStopped
// tests so they can exercise the real on-disk paths without touching the
// developer's ~/.ccmux/.
func setupStopHookTestStores(t *testing.T) (*agent.Store, *queue.Queue) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	store, err := agent.NewStore("stop-hook-test")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	q, err := queue.NewQueue("stop-hook-test")
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	return store, q
}

func TestHandleAgentStopped_ShouldHandOffToCIPoller_WhenAgentHasPR(t *testing.T) {
	// Stop hooks fire at end-of-turn during interactive CI-fix / review
	// resumes. If the agent decided to retrigger CI instead of pushing
	// (e.g. `gh run rerun` on a flaky eval), nothing else flips status back
	// to WaitingCI — so handleAgentStopped MUST do it for any Running agent
	// with a known PR. Otherwise the agent gets stuck in Ready + "Agent
	// finished (no PR)" forever, and the CI poller (which only walks
	// WaitingCI agents) never re-examines the PR.

	store, q := setupStopHookTestStores(t)
	before := time.Now()

	if err := store.Create(&agent.Agent{
		ID:     "agent-with-pr",
		Status: agent.StatusRunning,
		PRURL:  "https://github.com/o/r/pull/42",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	a, _ := store.Get("agent-with-pr")

	if err := handleAgentStopped(store, q, a); err != nil {
		t.Fatalf("handleAgentStopped: %v", err)
	}

	got, _ := store.Get("agent-with-pr")
	if got.Status != agent.StatusWaitingCI {
		t.Errorf("status = %s, want %s — agent with a PR must go back to the CI poller, not be marked idle", got.Status, agent.StatusWaitingCI)
	}
	if got.CIWaitAt.Before(before) {
		t.Errorf("CIWaitAt = %v, want >= %v — CIWaitAt should be bumped so the poller treats this as a fresh wait window", got.CIWaitAt, before)
	}

	items, err := q.List()
	if err != nil {
		t.Fatalf("queue List: %v", err)
	}
	for _, it := range items {
		if it.AgentID == "agent-with-pr" {
			t.Errorf("queue gained item %q for an agent we handed back to the CI poller — should be empty", it.Summary)
		}
	}
}

func TestHandleAgentStopped_ShouldMarkIdle_WhenAgentNeverMadePR(t *testing.T) {
	// Genuinely new agent that finished its first task without opening a PR.
	// This is the only Running case where the user does want to be told
	// "this one needs your attention".

	store, q := setupStopHookTestStores(t)

	if err := store.Create(&agent.Agent{
		ID:     "agent-no-pr",
		Status: agent.StatusRunning,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	a, _ := store.Get("agent-no-pr")

	if err := handleAgentStopped(store, q, a); err != nil {
		t.Fatalf("handleAgentStopped: %v", err)
	}

	got, _ := store.Get("agent-no-pr")
	if got.Status != agent.StatusReady {
		t.Errorf("status = %s, want %s", got.Status, agent.StatusReady)
	}

	items, err := q.List()
	if err != nil {
		t.Fatalf("queue List: %v", err)
	}
	var found *queue.QueueItem
	for _, it := range items {
		if it.AgentID == "agent-no-pr" {
			found = it
		}
	}
	if found == nil {
		t.Fatalf("expected an idle queue item for the no-PR agent, got none")
	}
	if found.Type != queue.ItemTypeIdle {
		t.Errorf("queue item type = %s, want %s", found.Type, queue.ItemTypeIdle)
	}
	if !strings.Contains(found.Summary, "no PR") {
		t.Errorf("queue item summary = %q, want it to mention there's no PR", found.Summary)
	}
}

func TestHandleAgentStopped_ShouldNotDisturb_NonRunningStatuses(t *testing.T) {
	// Stop hooks fire many times across an agent's life. For any status
	// other than Running, the agent is in a state another part of ccmux owns
	// (CI poller, review queue, merge queue, post-cleanup) and we must
	// leave it alone.

	cases := []agent.Status{
		agent.StatusReady,
		agent.StatusWaitingReview,
		agent.StatusWaitingCI,
		agent.StatusWaitingMergeQueue,
	}
	for _, status := range cases {
		t.Run(string(status), func(t *testing.T) {
			store, q := setupStopHookTestStores(t)

			id := "agent-" + string(status)
			if err := store.Create(&agent.Agent{
				ID:     id,
				Status: status,
				PRURL:  "https://github.com/o/r/pull/99",
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			a, _ := store.Get(id)

			if err := handleAgentStopped(store, q, a); err != nil {
				t.Fatalf("handleAgentStopped: %v", err)
			}

			got, _ := store.Get(id)
			if got.Status != status {
				t.Errorf("status changed from %s to %s — Stop hook should be a no-op for non-Running agents", status, got.Status)
			}

			items, _ := q.List()
			for _, it := range items {
				if it.AgentID == id {
					t.Errorf("queue gained item %q for an agent in %s — Stop hook should be a no-op", it.Summary, status)
				}
			}
		})
	}
}
