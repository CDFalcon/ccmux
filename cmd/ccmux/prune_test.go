package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/project"
	"github.com/CDFalcon/ccmux/internal/worktree"
)

// TestPrunableReason_ShouldFlagStaleEntry_GivenWindowReaped is the field-reported
// leak: the PR merged, ccmux reaped the tmux window, and the registry entry
// stayed behind at status ready with its worktree still on disk — being polled
// every 2s for as long as it existed.
func TestPrunableReason_ShouldFlagStaleEntry_GivenWindowReaped(t *testing.T) {
	// Setup.
	dir := t.TempDir()
	a := &agent.Agent{
		ID: "abc12345", WorktreePath: dir,
		TmuxWindow: "@9", Status: agent.StatusReady,
	}

	// Execute.
	reason, stale := prunableReason(a, true, map[string]bool{"@1": true})

	// Assert.
	if !stale {
		t.Fatal("expected an agent whose window is gone to be prunable")
	}
	if reason != reasonNoWindow {
		t.Errorf("expected reason %q, got %q", reasonNoWindow, reason)
	}
}

func TestPrunableReason_ShouldNotFlag_GivenLiveAgent(t *testing.T) {
	// Setup.
	dir := t.TempDir()
	a := &agent.Agent{
		ID: "abc12345", WorktreePath: dir,
		TmuxWindow: "@1", Status: agent.StatusRunning,
	}

	// Execute.
	_, stale := prunableReason(a, true, map[string]bool{"@1": true})

	// Assert.
	if stale {
		t.Error("expected a running agent with a live window to be left alone")
	}
}

func TestPrunableReason_ShouldFlagStaleEntry_GivenTerminalStatus(t *testing.T) {
	// Setup. StatusMerged is where an entry parks when post-merge cleanup fails.
	dir := t.TempDir()
	a := &agent.Agent{
		ID: "abc12345", WorktreePath: dir,
		TmuxWindow: "@1", Status: agent.StatusMerged,
	}

	// Execute.
	reason, stale := prunableReason(a, true, map[string]bool{"@1": true})

	// Assert.
	if !stale || reason != reasonTerminal {
		t.Errorf("expected a merged agent to be prunable as %q, got stale=%v reason=%q",
			reasonTerminal, stale, reason)
	}
}

// TestPrunableReason_ShouldFlagStaleEntry_GivenSpawnNeverRegistered covers the
// spawn race: the launcher died before `ccmux register-agent`, so the entry sits
// in StatusSpawning with an empty worktree_path and branch_name forever.
func TestPrunableReason_ShouldFlagStaleEntry_GivenSpawnNeverRegistered(t *testing.T) {
	// Setup.
	a := &agent.Agent{ID: "abc12345", Status: agent.StatusSpawning, TmuxWindow: "@3"}

	// Execute.
	reason, stale := prunableReason(a, true, map[string]bool{"@3": true})

	// Assert.
	if !stale || reason != reasonFailedSpawn {
		t.Errorf("expected an unregistered spawn to be prunable as %q, got stale=%v reason=%q",
			reasonFailedSpawn, stale, reason)
	}
}

func TestPrunableReason_ShouldFlagStaleEntry_GivenSessionNotRunning(t *testing.T) {
	// Setup.
	dir := t.TempDir()
	a := &agent.Agent{ID: "abc12345", WorktreePath: dir, TmuxWindow: "@1", Status: agent.StatusReady}

	// Execute.
	reason, stale := prunableReason(a, false, nil)

	// Assert.
	if !stale || reason != reasonNoSession {
		t.Errorf("expected an agent in a dead session to be prunable as %q, got stale=%v reason=%q",
			reasonNoSession, stale, reason)
	}
}

// TestPrunableReason_ShouldNotFlag_GivenUnknownWindowSet keeps a transient tmux
// failure from proposing the deletion of every live agent.
func TestPrunableReason_ShouldNotFlag_GivenUnknownWindowSet(t *testing.T) {
	// Setup.
	dir := t.TempDir()
	a := &agent.Agent{ID: "abc12345", WorktreePath: dir, TmuxWindow: "@9", Status: agent.StatusRunning}

	// Execute. sessionAlive, but the window set could not be enumerated.
	_, stale := prunableReason(a, true, nil)

	// Assert.
	if stale {
		t.Error("expected no window-based judgement when the live window set is unknown")
	}
}

// TestFindOrphanedWorktrees_ShouldFindDirectory_GivenNoAgentRecord covers the
// truly invisible leftover: teardown deleted the registry entry but failed to
// delete the directory, so nothing remained that knew the directory existed.
func TestFindOrphanedWorktrees_ShouldFindDirectory_GivenNoAgentRecord(t *testing.T) {
	// Setup. A project repo with three ccmux-* snapshots beside it: one
	// referenced by a live agent, one already planned, one a true orphan.
	home := t.TempDir()
	t.Setenv("HOME", home)

	codeDir := filepath.Join(home, "Code")
	repo := filepath.Join(codeDir, "mining")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	// The project store validates that a project path is a real repo.
	runOrFail(t, "", "git", "init", repo)
	live := filepath.Join(codeDir, "ccmux-live-11111111")
	planned := filepath.Join(codeDir, "ccmux-planned-22222222")
	orphan := filepath.Join(codeDir, "ccmux-orphan-33333333")
	for _, d := range []string{live, planned, orphan} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	store, err := project.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(&project.Project{Name: "mining", Path: repo}); err != nil {
		t.Fatal(err)
	}

	referenced := map[string]bool{live: true}
	plannedCandidates := []pruneCandidate{{agentID: "22222222", worktreePath: planned}}

	// Execute.
	got, err := findOrphanedWorktrees(referenced, plannedCandidates)
	if err != nil {
		t.Fatal(err)
	}

	// Assert. Only the true orphan, and never the project repo itself.
	if len(got) != 1 {
		var paths []string
		for _, c := range got {
			paths = append(paths, c.worktreePath)
		}
		t.Fatalf("expected exactly 1 orphan, got %d: %v", len(got), paths)
	}
	if got[0].worktreePath != orphan {
		t.Errorf("expected orphan %s, got %s", orphan, got[0].worktreePath)
	}
	if got[0].reason != reasonNoAgent {
		t.Errorf("expected reason %q, got %q", reasonNoAgent, got[0].reason)
	}
}

// TestFindOrphanedWorktrees_ShouldNotProposeProjectRepo_GivenCcmuxPrefixedRepo
// guards against eating the user's actual repo if it happens to be named
// ccmux-something — which is exactly the case in this repository's own worktrees.
func TestFindOrphanedWorktrees_ShouldNotProposeProjectRepo_GivenCcmuxPrefixedRepo(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)

	codeDir := filepath.Join(home, "Code")
	repo := filepath.Join(codeDir, "ccmux-main-checkout")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runOrFail(t, "", "git", "init", repo)

	store, err := project.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(&project.Project{Name: "ccmux", Path: repo}); err != nil {
		t.Fatal(err)
	}

	// Execute.
	got, err := findOrphanedWorktrees(map[string]bool{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	for _, c := range got {
		if c.worktreePath == repo {
			t.Fatalf("prune proposed deleting the project repo itself: %s", repo)
		}
	}
}

// TestReportAndMaybeApply_ShouldNotRemove_GivenDryRun pins the default: prune
// changes nothing unless --apply is passed.
func TestReportAndMaybeApply_ShouldNotRemove_GivenDryRun(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := filepath.Join(home, "ccmux-dryrun-44444444")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	candidates := []pruneCandidate{{worktreePath: wt, reason: reasonNoAgent}}

	// Execute.
	if err := reportAndMaybeApply(candidates, false, false, "prune"); err != nil {
		t.Fatal(err)
	}

	// Assert.
	if !dirExists(wt) {
		t.Error("dry run removed the worktree; it must change nothing")
	}
}

// TestReportAndMaybeApply_ShouldRefuse_GivenUnpushedWork is the safety guard the
// brief asked for: report the work, do not destroy it, even with --apply.
func TestReportAndMaybeApply_ShouldRefuse_GivenUnpushedWork(t *testing.T) {
	// Setup. A worktree holding a commit that exists nowhere else.
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := makeRepoWithUnpushedCommit(t, home)

	candidates := []pruneCandidate{{
		worktreePath: wt,
		reason:       reasonNoAgent,
		unsaved:      inspectForTest(wt),
	}}

	// Execute. --apply set, --force not.
	if err := reportAndMaybeApply(candidates, true, false, "prune"); err != nil {
		t.Fatal(err)
	}

	// Assert.
	if !dirExists(wt) {
		t.Error("prune destroyed a worktree holding unpushed work; it must report it instead")
	}
}

// TestReportAndMaybeApply_ShouldRemove_GivenUnpushedWorkAndForce pins the escape
// hatch, so an operator who has decided the work is disposable can still clean up.
func TestReportAndMaybeApply_ShouldRemove_GivenUnpushedWorkAndForce(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := makeRepoWithUnpushedCommit(t, home)

	candidates := []pruneCandidate{{
		worktreePath: wt,
		reason:       reasonNoAgent,
		unsaved:      inspectForTest(wt),
	}}

	// Execute.
	if err := reportAndMaybeApply(candidates, true, true, "prune"); err != nil {
		t.Fatal(err)
	}

	// Assert.
	if dirExists(wt) {
		t.Error("expected --force to remove the worktree despite unpushed work")
	}
}

// TestReportAndMaybeApply_ShouldRemoveCleanWorktree_GivenApply is the happy path
// an operator uses to clear historical leftovers.
func TestReportAndMaybeApply_ShouldRemoveCleanWorktree_GivenApply(t *testing.T) {
	// Setup. A directory that is not a repo would be "undetermined", so build a
	// genuinely clean, fully pushed worktree.
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := makeCleanPushedRepo(t, home)

	candidates := []pruneCandidate{{
		worktreePath: wt,
		reason:       reasonNoAgent,
		unsaved:      inspectForTest(wt),
	}}

	// Execute.
	if err := reportAndMaybeApply(candidates, true, false, "prune"); err != nil {
		t.Fatal(err)
	}

	// Assert.
	if dirExists(wt) {
		t.Errorf("expected the clean leftover %s to be removed", wt)
	}
}

// TestExecutePrune_ShouldDeleteRegistryEntry_GivenAgentRecord closes the
// lifecycle gap the brief reported: nothing used to purge the registry entry, so
// a finished agent stayed listed (and polled) forever.
func TestExecutePrune_ShouldDeleteRegistryEntry_GivenAgentRecord(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, err := agent.NewStore("testsession")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(&agent.Agent{
		ID: "deadbeef", Status: agent.StatusMerged, ProjectName: "mining",
	}); err != nil {
		t.Fatal(err)
	}

	// Execute.
	err = executePrune(pruneCandidate{agentID: "deadbeef", sessionID: "testsession"})
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if _, err := store.Get("deadbeef"); err == nil {
		t.Error("expected the registry entry to be purged")
	}
}

// TestPlanAgentRm_ShouldFindAgent_AcrossSessions lets an operator name an agent
// without knowing which session recorded it.
func TestPlanAgentRm_ShouldFindAgent_AcrossSessions(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)

	other, err := agent.NewStore("session-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Create(&agent.Agent{ID: "aaaaaaaa", Status: agent.StatusReady}); err != nil {
		t.Fatal(err)
	}
	target, err := agent.NewStore("session-b")
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Create(&agent.Agent{
		ID: "bbbbbbbb", Status: agent.StatusReady, BranchName: "ccmux/thing-bbbbbbbb",
	}); err != nil {
		t.Fatal(err)
	}

	// Execute.
	got, err := planAgentRm("bbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if got.sessionID != "session-b" {
		t.Errorf("expected session-b, got %q", got.sessionID)
	}
	if got.branchName != "ccmux/thing-bbbbbbbb" {
		t.Errorf("expected the branch to be carried through, got %q", got.branchName)
	}
}

func TestPlanAgentRm_ShouldError_GivenUnknownAgent(t *testing.T) {
	// Setup.
	t.Setenv("HOME", t.TempDir())

	// Execute.
	_, err := planAgentRm("nosuchid")

	// Assert.
	if err == nil {
		t.Error("expected an error for an unknown agent id")
	}
}

// --- helpers ---

func inspectForTest(path string) worktree.UnsavedWork {
	return worktree.Inspect(path)
}

func makeCleanPushedRepo(t *testing.T, parent string) string {
	t.Helper()
	upstream := filepath.Join(parent, "upstream.git")
	runOrFail(t, "", "git", "init", "--bare", upstream)

	wt := filepath.Join(parent, "ccmux-clean-55555555")
	runOrFail(t, "", "git", "init", wt)
	runOrFail(t, wt, "git", "config", "user.email", "t@t.com")
	runOrFail(t, wt, "git", "config", "user.name", "t")
	runOrFail(t, wt, "git", "remote", "add", "origin", upstream)
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrFail(t, wt, "git", "add", ".")
	runOrFail(t, wt, "git", "commit", "-m", "base")
	runOrFail(t, wt, "git", "push", "-u", "origin", "HEAD")
	return wt
}

func makeRepoWithUnpushedCommit(t *testing.T, parent string) string {
	t.Helper()
	wt := makeCleanPushedRepo(t, parent)
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrFail(t, wt, "git", "add", ".")
	runOrFail(t, wt, "git", "commit", "-m", "unpushed work")
	return wt
}

func runOrFail(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
