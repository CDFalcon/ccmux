package worktree

import (
	"testing"
)

func TestNewManager_ShouldCreateManager_GivenRepoRoot(t *testing.T) {
	// Execute.
	m := NewManager("/tmp/test-repo")

	// Assert.
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.repoRoot != "/tmp/test-repo" {
		t.Errorf("expected repo root '/tmp/test-repo', got '%s'", m.repoRoot)
	}
}

func TestRemove_ShouldFail_GivenNonexistentWorktree(t *testing.T) {
	// Setup.
	m := NewManager("/tmp/nonexistent-repo-root-12345")

	// Execute.
	err := m.Remove("/tmp/nonexistent-worktree-12345")

	// Assert.
	if err == nil {
		t.Error("expected error for nonexistent worktree, got nil")
	}
}

func TestDeleteBranch_ShouldFail_GivenNonexistentRepo(t *testing.T) {
	// Setup.
	m := NewManager("/tmp/nonexistent-repo-root-12345")

	// Execute.
	err := m.DeleteBranch("nonexistent-branch")

	// Assert.
	if err == nil {
		t.Error("expected error for nonexistent repo, got nil")
	}
}
