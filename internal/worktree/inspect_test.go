package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepoWithRemote builds a worktree that looks like an agent's: a real repo
// with a remote-tracking ref, so "unpushed" is answerable.
func initRepoWithRemote(t *testing.T) string {
	t.Helper()

	upstream := t.TempDir()
	mustRun(t, "", "git", "init", "--bare", upstream)

	dir := t.TempDir()
	mustRun(t, "", "git", "init", dir)
	mustRun(t, dir, "git", "config", "user.email", "test@test.com")
	mustRun(t, dir, "git", "config", "user.name", "test")
	mustRun(t, dir, "git", "remote", "add", "origin", upstream)

	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "git", "add", ".")
	mustRun(t, dir, "git", "commit", "-m", "base")
	mustRun(t, dir, "git", "push", "-u", "origin", "HEAD")

	return dir
}

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func TestInspect_ShouldReportClean_GivenFullyPushedWorktree(t *testing.T) {
	// Setup.
	dir := initRepoWithRemote(t)

	// Execute.
	got := Inspect(dir)

	// Assert.
	if !got.IsClean() {
		t.Errorf("expected a fully pushed worktree to be clean, got %q", got.Summary())
	}
}

func TestInspect_ShouldReportUncommitted_GivenModifiedFile(t *testing.T) {
	// Setup.
	dir := initRepoWithRemote(t)
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Execute.
	got := Inspect(dir)

	// Assert.
	if got.IsClean() {
		t.Fatal("expected a modified worktree to be reported as not clean")
	}
	if len(got.Uncommitted) != 1 {
		t.Errorf("expected 1 uncommitted entry, got %v", got.Uncommitted)
	}
}

func TestInspect_ShouldReportUncommitted_GivenUntrackedFile(t *testing.T) {
	// Setup.
	dir := initRepoWithRemote(t)
	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Execute.
	got := Inspect(dir)

	// Assert.
	if got.IsClean() {
		t.Fatal("expected an untracked file to be reported as not clean")
	}
}

// TestInspect_ShouldReportUnpushed_GivenLocalOnlyCommit is the case that makes
// the guard worth having: the worktree is pristine, but it holds the only copy
// of a commit. Deleting it silently loses the agent's work.
func TestInspect_ShouldReportUnpushed_GivenLocalOnlyCommit(t *testing.T) {
	// Setup.
	dir := initRepoWithRemote(t)
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "git", "add", ".")
	mustRun(t, dir, "git", "commit", "-m", "unpushed feature work")

	// Execute.
	got := Inspect(dir)

	// Assert.
	if got.IsClean() {
		t.Fatal("expected an unpushed commit to be reported as not clean")
	}
	if len(got.UnpushedCommits) != 1 {
		t.Fatalf("expected 1 unpushed commit, got %v", got.UnpushedCommits)
	}
	if got.Summary() == "" {
		t.Error("expected a non-empty summary for a dirty worktree")
	}
}

// TestInspect_ShouldBeUndetermined_GivenNoRemoteTrackingRefs pins the
// conservative branch. Without remote refs, `HEAD --not --remotes` would list
// the entire history and we cannot distinguish pushed from unpushed — so we must
// say "don't know", which the callers treat as unsafe.
func TestInspect_ShouldBeUndetermined_GivenNoRemoteTrackingRefs(t *testing.T) {
	// Setup. A repo with commits but no remote.
	dir := t.TempDir()
	mustRun(t, "", "git", "init", dir)
	mustRun(t, dir, "git", "config", "user.email", "test@test.com")
	mustRun(t, dir, "git", "config", "user.name", "test")
	mustRun(t, dir, "git", "commit", "--allow-empty", "-m", "local only")

	// Execute.
	got := Inspect(dir)

	// Assert.
	if got.IsClean() {
		t.Error("expected a repo with no remote-tracking refs to be undetermined, not clean")
	}
	if got.Undetermined == "" {
		t.Error("expected an Undetermined reason to be recorded")
	}
}

// TestInspect_ShouldReportClean_GivenMissingDirectory keeps prune able to tidy
// registry entries whose directory is already gone: there is nothing left to
// lose, so the guard must not block.
func TestInspect_ShouldReportClean_GivenMissingDirectory(t *testing.T) {
	// Setup.
	missing := filepath.Join(t.TempDir(), "never-existed")

	// Execute.
	got := Inspect(missing)

	// Assert.
	if !got.IsClean() {
		t.Errorf("expected a missing worktree to be clean, got %q", got.Summary())
	}
}

func TestInspect_ShouldBeUndetermined_GivenNonRepoDirectory(t *testing.T) {
	// Setup.
	dir := t.TempDir()

	// Execute.
	got := Inspect(dir)

	// Assert.
	if got.IsClean() {
		t.Error("expected a non-repo directory to be undetermined rather than clean")
	}
}

func TestInspect_ShouldBeUndetermined_GivenEmptyPath(t *testing.T) {
	// Setup / Execute.
	got := Inspect("")

	// Assert.
	if got.IsClean() {
		t.Error("expected an empty path to be undetermined rather than clean")
	}
}
