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
			if !strings.Contains(content, "The original task was:") {
				t.Errorf("recovery script for %s should embed the original task", h)
			}
		})
	}
}

// runPlaceholderBanner writes a placeholder script for the given state and
// returns the banner it prints. The banner is the only thing an operator sees
// in a parked pane, so it is worth asserting on what actually reaches the
// terminal rather than on the generated source.
func runPlaceholderBanner(t *testing.T, a *agent.Agent) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	path, err := writePlaceholderScript(a.ID, "/tmp/repo/wt", "the original task", a.PRURL, placeholderStatusFor(a))
	if err != nil {
		t.Fatalf("writePlaceholderScript: %v", err)
	}
	assertValidBash(t, path)

	// The script parks on `while true; sleep 3600` after printing, so run only
	// the part before the loop rather than waiting on the process.
	out, err := exec.Command("bash", "-c", "awk '/^while true; do/{exit} {print}' "+path+" | bash").CombinedOutput()
	if err != nil {
		t.Fatalf("running placeholder banner: %v\n%s", err, out)
	}
	return string(out)
}

func TestWritePlaceholderScript_ShouldNotClaimAPR_GivenIdleAgent(t *testing.T) {
	// Recovery parks StatusReady and StatusWaitingReview agents through the
	// same script. It used to hardcode the review banner for both, so after
	// every ccmux restart each idle agent's pane announced "This agent has a PR
	// up for review" — several agents at once, none of which had opened a PR.

	banner := runPlaceholderBanner(t, &agent.Agent{ID: "idle-agent", Status: agent.StatusReady})

	if strings.Contains(banner, "PR up for review") || strings.Contains(banner, "waiting for PR review") {
		t.Errorf("idle agent's pane claims a PR is up for review:\n%s", banner)
	}
	if !strings.Contains(banner, "idle - waiting for input") {
		t.Errorf("idle agent's pane should say it is idle, got:\n%s", banner)
	}
}

func TestWritePlaceholderScript_ShouldShowPR_GivenAgentWaitingOnReview(t *testing.T) {
	banner := runPlaceholderBanner(t, &agent.Agent{
		ID:     "review-agent",
		Status: agent.StatusWaitingReview,
		PRURL:  "https://github.com/o/r/pull/42",
	})

	if !strings.Contains(banner, "waiting for PR review") {
		t.Errorf("review-ready agent's pane should say so, got:\n%s", banner)
	}
	// The URL is what makes the claim checkable — an operator seeing "there's a
	// PR" with no link cannot tell a real one from a stale record.
	if !strings.Contains(banner, "https://github.com/o/r/pull/42") {
		t.Errorf("review banner should name the PR, got:\n%s", banner)
	}
}

func TestWritePlaceholderScript_ShouldNotClaimReview_GivenReadyAgentHoldingPR(t *testing.T) {
	// StatusReady + a PR URL is a real state: a throttled CI-fix loop parks the
	// agent for manual intervention while its PR stays open. That is not
	// "ready for review", and the pane must not say it is.

	banner := runPlaceholderBanner(t, &agent.Agent{
		ID:     "throttled-agent",
		Status: agent.StatusReady,
		PRURL:  "https://github.com/o/r/pull/42",
	})

	if strings.Contains(banner, "waiting for PR review") {
		t.Errorf("agent parked for manual intervention should not be shown as review-ready:\n%s", banner)
	}
}

func TestPlaceholderStatusFor_ShouldDemoteWaitingReview_GivenNoPRURL(t *testing.T) {
	// Nothing in ccmux can show, poll or merge a PR it has no URL for, so this
	// record cannot mean what it says. `ccmux pr-ready` used to produce exactly
	// this state by setting the status without recording the URL.

	got := placeholderStatusFor(&agent.Agent{ID: "no-pr", Status: agent.StatusWaitingReview})

	if got != agent.StatusReady {
		t.Errorf("status = %s, want %s — waiting_review with no PR URL is an unbacked claim", got, agent.StatusReady)
	}
}

func TestPlaceholderStatusFor_ShouldPreserveStatus_GivenPRURL(t *testing.T) {
	for _, status := range []agent.Status{agent.StatusWaitingReview, agent.StatusReady} {
		t.Run(string(status), func(t *testing.T) {
			got := placeholderStatusFor(&agent.Agent{ID: "has-pr", Status: status, PRURL: "https://github.com/o/r/pull/42"})
			if got != status {
				t.Errorf("status = %s, want %s preserved", got, status)
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
	// CIWaitAt is the "new review feedback since" cutoff for the poller. The
	// Stop hook fires at every end-of-turn, so bumping it here would swallow
	// review comments that arrived mid-turn (the poller's busy gate defers
	// the resume until the turn ends — by which time the bump had moved the
	// cutoff past the comments, leaving the agent stuck in waiting_review
	// until the next push). It must be preserved.
	ciWaitAt := time.Now().Add(-10 * time.Minute)

	if err := store.Create(&agent.Agent{
		ID:       "agent-with-pr",
		Status:   agent.StatusRunning,
		PRURL:    "https://github.com/o/r/pull/42",
		CIWaitAt: ciWaitAt,
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
	if !got.CIWaitAt.Equal(ciWaitAt) {
		t.Errorf("CIWaitAt = %v, want %v preserved — bumping the new-review cutoff at end-of-turn swallows mid-turn review comments", got.CIWaitAt, ciWaitAt)
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

func TestHandleAgentStopped_ShouldInitializeCIWaitAt_WhenAgentHasPRButNoCutoff(t *testing.T) {
	// An agent record without CIWaitAt (older stores, or a PR recorded
	// through a path that skipped it) still needs a cutoff — the poller only
	// checks for new reviews when CIWaitAt is set.

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
		t.Errorf("status = %s, want %s", got.Status, agent.StatusWaitingCI)
	}
	if got.CIWaitAt.Before(before) {
		t.Errorf("CIWaitAt = %v, want >= %v — zero cutoff should be initialized so review polling works", got.CIWaitAt, before)
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

func TestFormatAgentRows_ShouldMarkSelfAndFlattenTask(t *testing.T) {
	agents := []*agent.Agent{
		{ID: "aaaa1111", Status: agent.StatusRunning, ProjectName: "proj", BranchName: "ccmux/aaaa1111", Task: "fix the\nlogin  bug"},
		{ID: "bbbb2222", Status: agent.StatusReady, Task: "write docs", PRURL: "https://github.com/x/y/pull/1"},
	}

	out := formatAgentRows(agents, "bbbb2222", false)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + 2 rows, got %d lines:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[1], "fix the login bug") {
		t.Errorf("expected task whitespace flattened onto one row, got: %q", lines[1])
	}
	if strings.Contains(lines[1], "(you)") {
		t.Errorf("expected peer row not to be marked as self: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "bbbb2222 (you)") {
		t.Errorf("expected self row to be marked (you): %q", lines[2])
	}
	if !strings.Contains(lines[2], "idle") || !strings.Contains(lines[2], "https://github.com/x/y/pull/1") {
		t.Errorf("expected display status and PR URL in self row: %q", lines[2])
	}
	if !strings.Contains(lines[1], " -  ") {
		t.Errorf("expected missing PR rendered as '-': %q", lines[1])
	}
}

func TestFormatAgentRows_ShouldPreviewLongTask_UnlessFull(t *testing.T) {
	long := strings.Repeat("word ", 100) // 500 chars
	agents := []*agent.Agent{{ID: "aaaa1111", Status: agent.StatusRunning, Task: long}}

	preview := formatAgentRows(agents, "self", false)
	full := formatAgentRows(agents, "self", true)

	if !strings.Contains(preview, "…") || strings.Contains(preview, strings.TrimSpace(long)) {
		t.Errorf("expected the long task truncated with an ellipsis, got:\n%s", preview)
	}
	if !strings.Contains(full, strings.TrimSpace(long)) {
		t.Errorf("expected --full to keep the whole task, got:\n%s", full)
	}
}

func TestPeerUndeliverableReason_ShouldAllowOnlyLiveHarnessPanes(t *testing.T) {
	cases := []struct {
		name      string
		agent     *agent.Agent
		paneAlive bool
		startCmd  string
		wantOK    bool
	}{
		{"running agent with live pane", &agent.Agent{Status: agent.StatusRunning}, true, "bash /home/u/.ccmux/launchers/abc.sh", true},
		{"idle agent with live pane", &agent.Agent{Status: agent.StatusReady}, true, "", true},
		{"waiting review with live pane", &agent.Agent{Status: agent.StatusWaitingReview}, true, "", true},
		{"spawning", &agent.Agent{Status: agent.StatusSpawning}, true, "", false},
		{"merged", &agent.Agent{Status: agent.StatusMerged}, true, "", false},
		{"failed", &agent.Agent{Status: agent.StatusFailed}, true, "", false},
		{"waiting on CI", &agent.Agent{Status: agent.StatusWaitingCI}, true, "", false},
		{"dead pane", &agent.Agent{Status: agent.StatusRunning}, false, "", false},
		{"parked placeholder", &agent.Agent{Status: agent.StatusReady}, true, "bash /home/u/.ccmux/launchers/abc-placeholder.sh", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := peerUndeliverableReason(tc.agent, tc.paneAlive, tc.startCmd)
			if tc.wantOK && reason != "" {
				t.Errorf("expected deliverable, got reason %q", reason)
			}
			if !tc.wantOK && reason == "" {
				t.Errorf("expected an undeliverable reason")
			}
		})
	}
}

func TestPeerMessagePrefix_ShouldNameSender(t *testing.T) {
	if got := peerMessagePrefix("e19f78a4") + "hello"; got != "[message from ccmux agent e19f78a4] hello" {
		t.Errorf("unexpected message text: %q", got)
	}
}

// TestAgentScripts_ShouldTeachAgentFacingCommands pins the agent-facing
// tooling into every system prompt an agent can start from: an agent that
// does not know `ccmux agents`, `ccmux pane` or `ccmux reload` exist cannot
// use them, and a resumed or reloaded agent must not forget.
func TestAgentScripts_ShouldTeachAgentFacingCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	launcher, err := writeLauncherScript("peer-launch", "task", "/tmp/repo", "origin/main", "sess", false, "", "", "", harness.Claude, true)
	if err != nil {
		t.Fatalf("writeLauncherScript failed: %v", err)
	}
	recovery, err := writeRecoveryScript("peer-recover", "/tmp/wt", "origin/main", "sess", "task", harness.Claude, true)
	if err != nil {
		t.Fatalf("writeRecoveryScript failed: %v", err)
	}
	reload, err := writeReloadScript("peer-reload", "/tmp/wt", "origin/main", "task", "", harness.Claude, true)
	if err != nil {
		t.Fatalf("writeReloadScript failed: %v", err)
	}

	for _, path := range []string{launcher, recovery, reload} {
		assertValidBash(t, path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"ccmux agents list", "ccmux agents send <agent-id>", "ccmux pane open", "ccmux reload [note...]"} {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s: system prompt should mention %q", filepath.Base(path), want)
			}
		}
	}
}

// --- reload: an agent restarting its own harness ---------------------------

func TestWriteReloadScript_ShouldResumeConversation_WithNoteAndTelemetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	note := "I added the chrome MCP server to .mcp.json; verify its tools loaded, then continue with step 3"
	path, err := writeReloadScript("reload-1", "/tmp/wt", "origin/main", "the original task", note, harness.Claude, false)
	if err != nil {
		t.Fatalf("writeReloadScript failed: %v", err)
	}
	if !strings.HasSuffix(path, "reload-1-reload.sh") {
		t.Errorf("script path = %q, want it to end in reload-1-reload.sh so teardown can find it", path)
	}
	assertValidBash(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)

	for _, want := range []string{
		harness.Claude.ContinueWithPromptCommand(), // resumes, does not start over
		note,                          // the note reaches the resumed agent
		"the original task",           // Codex-style fresh sessions need it; harmless for Claude
		"OTEL_EXPORTER_OTLP_ENDPOINT", // cost telemetry survives the reload
		"ccmux agent-stopped",         // exit capture keeps the status machine honest
		"do NOT add a --draft flag",   // draftPRs=false threaded through
	} {
		if !strings.Contains(script, want) {
			t.Errorf("reload script should contain %q", want)
		}
	}
}

func TestWriteReloadScript_ShouldStartFreshSession_GivenCodex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := writeReloadScript("reload-codex", "/tmp/wt", "origin/main", "task", "", harness.Codex, true)
	if err != nil {
		t.Fatalf("writeReloadScript failed: %v", err)
	}
	assertValidBash(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), harness.Codex.ContinueWithPromptCommand()) {
		t.Errorf("codex reload script should invoke %q", harness.Codex.ContinueWithPromptCommand())
	}
	if strings.Contains(string(data), "claude --continue") {
		t.Error("codex reload script must not invoke claude")
	}
	if !strings.Contains(string(data), "keep the --draft flag") {
		t.Error("draftPRs=true should be threaded through to the PR instructions")
	}
}

func TestReloadPrompt_ShouldExplainReload_AndCarryNote(t *testing.T) {
	bare := reloadPrompt("")
	if !strings.Contains(bare, "ccmux reload") || !strings.Contains(bare, "MCP servers") {
		t.Errorf("prompt should say the harness was reloaded and why: %q", bare)
	}
	if strings.Contains(bare, "note to yourself") {
		t.Errorf("prompt without a note should not mention one: %q", bare)
	}
	noted := reloadPrompt("check the new tools")
	if !strings.HasSuffix(noted, "check the new tools") || !strings.Contains(noted, "note to yourself") {
		t.Errorf("prompt should end with the agent's note: %q", noted)
	}
}

func TestReloadRefusalReason_ShouldOnlyAllowTheAgentsOwnPane(t *testing.T) {
	cases := []struct {
		name         string
		a            *agent.Agent
		pane, window string
		wantRefused  bool
	}{
		{"own pane", &agent.Agent{TmuxPane: "%5", TmuxWindow: "@2"}, "%5", "@2", false},
		{"shared pane in same window", &agent.Agent{TmuxPane: "%5", TmuxWindow: "@2"}, "%9", "@2", true},
		{"legacy record, own window", &agent.Agent{TmuxWindow: "@2"}, "%5", "@2", false},
		{"legacy record, other window", &agent.Agent{TmuxWindow: "@2"}, "%5", "@7", true},
		{"no pane recorded at all", &agent.Agent{}, "%5", "@2", false},
	}
	for _, tc := range cases {
		reason := reloadRefusalReason(tc.a, tc.pane, tc.window)
		if (reason != "") != tc.wantRefused {
			t.Errorf("%s: reloadRefusalReason = %q, want refused=%v", tc.name, reason, tc.wantRefused)
		}
	}
}

func TestRemoveLauncherFiles_ShouldDeleteEveryScriptKind(t *testing.T) {
	dir := t.TempDir()
	for _, suffix := range launcherFileSuffixes {
		if err := os.WriteFile(filepath.Join(dir, "agent-x"+suffix), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-y.sh"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	removeLauncherFiles(dir, "agent-x")

	left, _ := filepath.Glob(filepath.Join(dir, "agent-x*"))
	if len(left) != 0 {
		t.Errorf("launcher files left behind: %v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-y.sh")); err != nil {
		t.Error("another agent's launcher must survive")
	}
	for _, want := range []string{"-reload.sh", "-restart.sh", "-prompts.txt"} {
		found := false
		for _, s := range launcherFileSuffixes {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Errorf("launcherFileSuffixes should include %q", want)
		}
	}
}

// --- ci-wait: refusing a subagent's PR -------------------------------------

func setupCIWaitTest(t *testing.T, a *agent.Agent) (*agent.Store, *queue.Queue) {
	t.Helper()
	store, q := setupStopHookTestStores(t)
	if err := store.Create(a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return store, q
}

func TestRecordPRForCIWait_ShouldRecordPR_WhenHeadBranchIsAgentsOwn(t *testing.T) {
	store, q := setupCIWaitTest(t, &agent.Agent{ID: "a1", Status: agent.StatusRunning, BranchName: "ccmux/a1"})

	lookup := func(string) (string, error) { return "ccmux/a1", nil }
	if err := recordPRForCIWait(store, q, "a1", "https://github.com/o/r/pull/7", lookup); err != nil {
		t.Fatalf("recordPRForCIWait: %v", err)
	}

	got, _ := store.Get("a1")
	if got.Status != agent.StatusWaitingCI || got.PRURL != "https://github.com/o/r/pull/7" {
		t.Errorf("got status=%s pr=%q, want waiting_ci + the PR recorded", got.Status, got.PRURL)
	}
}

func TestRecordPRForCIWait_ShouldIgnorePR_WhenHeadBranchBelongsToSomeoneElse(t *testing.T) {
	// A Claude subagent running under <worktree>/.claude/worktrees/<x> shares
	// the parent's hooks and CCMUX_AGENT_ID. Its `gh pr create` opens a PR on
	// its own branch; recording that against the parent flipped the parent to
	// waiting_ci for a PR that isn't its work.
	store, q := setupCIWaitTest(t, &agent.Agent{ID: "parent", Status: agent.StatusRunning, BranchName: "ccmux/parent"})

	lookup := func(string) (string, error) { return "worktree-subagent-abc", nil }
	if err := recordPRForCIWait(store, q, "parent", "https://github.com/o/r/pull/8", lookup); err != nil {
		t.Fatalf("recordPRForCIWait: %v", err)
	}

	got, _ := store.Get("parent")
	if got.Status != agent.StatusRunning {
		t.Errorf("status = %s, want %s untouched — a subagent's PR must not move the parent", got.Status, agent.StatusRunning)
	}
	if got.PRURL != "" {
		t.Errorf("PRURL = %q, want empty — the subagent's PR must not be recorded as the parent's", got.PRURL)
	}
}

func TestRecordPRForCIWait_ShouldFailOpen_WhenHeadLookupErrors(t *testing.T) {
	// Offline / gh unauthenticated must not lose a genuine PR: keep the
	// pre-existing behaviour rather than dropping it on the floor.
	store, q := setupCIWaitTest(t, &agent.Agent{ID: "a1", Status: agent.StatusRunning, BranchName: "ccmux/a1"})

	lookup := func(string) (string, error) { return "", os.ErrDeadlineExceeded }
	if err := recordPRForCIWait(store, q, "a1", "https://github.com/o/r/pull/9", lookup); err != nil {
		t.Fatalf("recordPRForCIWait: %v", err)
	}

	got, _ := store.Get("a1")
	if got.Status != agent.StatusWaitingCI || got.PRURL != "https://github.com/o/r/pull/9" {
		t.Errorf("got status=%s pr=%q, want the PR recorded despite the lookup failure", got.Status, got.PRURL)
	}
}

func TestRecordPRForCIWait_ShouldSkipLookup_WhenURLIsAlreadyTheAgentsPR(t *testing.T) {
	store, q := setupCIWaitTest(t, &agent.Agent{
		ID: "a1", Status: agent.StatusWaitingReview, BranchName: "ccmux/a1", PRURL: "https://github.com/o/r/pull/7",
	})

	called := false
	lookup := func(string) (string, error) { called = true; return "", nil }
	if err := recordPRForCIWait(store, q, "a1", "https://github.com/o/r/pull/7", lookup); err != nil {
		t.Fatalf("recordPRForCIWait: %v", err)
	}
	if called {
		t.Errorf("head lookup ran for a URL the agent already owns — a network round-trip per re-push is wasted")
	}
	got, _ := store.Get("a1")
	if got.Status != agent.StatusWaitingCI {
		t.Errorf("status = %s, want %s", got.Status, agent.StatusWaitingCI)
	}
}

func TestRecordPRForCIWait_ShouldUseStoredPR_WhenNoURLGiven(t *testing.T) {
	store, q := setupCIWaitTest(t, &agent.Agent{
		ID: "a1", Status: agent.StatusWaitingReview, BranchName: "ccmux/a1", PRURL: "https://github.com/o/r/pull/7",
	})

	lookup := func(string) (string, error) { t.Fatal("lookup must not run on the git-push path"); return "", nil }
	if err := recordPRForCIWait(store, q, "a1", "", lookup); err != nil {
		t.Fatalf("recordPRForCIWait: %v", err)
	}
	got, _ := store.Get("a1")
	if got.Status != agent.StatusWaitingCI || got.PRURL != "https://github.com/o/r/pull/7" {
		t.Errorf("got status=%s pr=%q, want waiting_ci on the stored PR", got.Status, got.PRURL)
	}
}

func TestRecordPRForCIWait_ShouldNoOp_WhenNoURLAndNoStoredPR(t *testing.T) {
	store, q := setupCIWaitTest(t, &agent.Agent{ID: "a1", Status: agent.StatusRunning, BranchName: "ccmux/a1"})

	if err := recordPRForCIWait(store, q, "a1", "", nil); err != nil {
		t.Fatalf("recordPRForCIWait: %v", err)
	}
	got, _ := store.Get("a1")
	if got.Status != agent.StatusRunning || got.PRURL != "" {
		t.Errorf("got status=%s pr=%q, want untouched — pushing a branch before any PR exists is not a CI wait", got.Status, got.PRURL)
	}
}

// --- post_tool_use hook: which tool calls reach ccmux ci-wait --------------

// runPostToolUseHook executes the installed hook script against one
// PostToolUse payload with a stub `ccmux` on PATH, and returns the argv the
// stub was invoked with ("" if the hook never called ccmux).
func runPostToolUseHook(t *testing.T, payload string) string {
	t.Helper()
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}

	dir := t.TempDir()
	hook := filepath.Join(dir, "post_tool_use.sh")
	if err := os.WriteFile(hook, []byte(postToolUseHookScript), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	log := filepath.Join(dir, "ccmux.log")
	stub := "#!/bin/bash\necho \"$*\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ccmux"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	cmd := exec.Command(hook)
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "CCMUX_AGENT_ID=parent")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook exited non-zero: %v\n%s", err, out)
	}

	// The hook nohups ci-wait and returns without waiting; give the stub a
	// moment to land before concluding it was never called.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if data, err := os.ReadFile(log); err == nil {
			return strings.TrimSpace(string(data))
		}
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func prCreatePayload(cwd, command string) string {
	return `{"tool_name":"Bash","cwd":"` + cwd + `","tool_input":{"command":"` + command + `"},"tool_response":{"stdout":"https://github.com/o/r/pull/12\n"}}`
}

func TestPostToolUseHook_ShouldStartCIWait_GivenPRCreateFromAgentWorktree(t *testing.T) {
	got := runPostToolUseHook(t, prCreatePayload("/Users/x/Code/ccmux-abc", "gh pr create --draft --base master --title t --body b"))
	if got != "ci-wait https://github.com/o/r/pull/12" {
		t.Errorf("ccmux argv = %q, want ci-wait with the PR URL", got)
	}
}

func TestPostToolUseHook_ShouldStartCIWait_GivenGitPush(t *testing.T) {
	got := runPostToolUseHook(t, `{"tool_name":"Bash","cwd":"/Users/x/Code/ccmux-abc","tool_input":{"command":"git push -u origin HEAD"},"tool_response":{"stdout":""}}`)
	if got != "ci-wait" {
		t.Errorf("ccmux argv = %q, want a bare ci-wait so the stored PR is re-polled", got)
	}
}

func TestPostToolUseHook_ShouldIgnorePRCreate_GivenSubagentWorktreeCwd(t *testing.T) {
	// Claude's worktree-isolated subagents live under
	// <worktree>/.claude/worktrees/<name> and inherit the parent's hooks.
	got := runPostToolUseHook(t, prCreatePayload("/Users/x/Code/ccmux-abc/.claude/worktrees/agent-1", "gh pr create --base master --title t --body b"))
	if got != "" {
		t.Errorf("ccmux argv = %q, want no call — a subagent's PR is not the parent's", got)
	}
}

func TestPostToolUseHook_ShouldIgnorePRCreate_GivenCommandEntersSubagentWorktree(t *testing.T) {
	got := runPostToolUseHook(t, prCreatePayload("/Users/x/Code/ccmux-abc", "cd /Users/x/Code/ccmux-abc/.claude/worktrees/agent-1 && gh pr create --base master --title t --body b"))
	if got != "" {
		t.Errorf("ccmux argv = %q, want no call — the command cd's into a subagent worktree", got)
	}
}

func TestPostToolUseHook_ShouldIgnoreGitPush_GivenSubagentWorktreeCwd(t *testing.T) {
	got := runPostToolUseHook(t, `{"tool_name":"Bash","cwd":"/Users/x/Code/ccmux-abc/.claude/worktrees/agent-1","tool_input":{"command":"git push -u origin HEAD"},"tool_response":{"stdout":""}}`)
	if got != "" {
		t.Errorf("ccmux argv = %q, want no call — a subagent's push must not re-arm the parent's CI wait", got)
	}
}
