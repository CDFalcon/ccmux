// Package worktree manages git worktree lifecycle.
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Manager struct {
	repoRoot string
}

func NewManager(repoRoot string) *Manager {
	return &Manager{repoRoot: repoRoot}
}

// Remove deletes the worktree at the given path. It auto-detects whether
// the path is a rift-managed copy-on-write snapshot (presence of a `.rift`
// marker at the workspace root) or a plain `git worktree` and dispatches
// to the right teardown command. The two cases must not be mixed:
// `git worktree remove` errors on a rift snapshot (it isn't registered
// as a worktree), and `rift remove` errors on a normal worktree.
func (m *Manager) Remove(worktreePath string) error {
	if isRiftWorkspace(worktreePath) {
		// `rift remove` reads the workspace's own `.rift` marker, so we
		// run it FROM the workspace (cwd) rather than from the repo root.
		// `--here` would also work but isn't documented across versions;
		// the cwd form is the only invocation the README guarantees.
		cmd := exec.Command("rift", "remove")
		cmd.Dir = worktreePath
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to remove rift workspace: %s: %w", string(output), err)
		}
		return nil
	}

	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = m.repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to remove worktree: %s: %w", string(output), err)
	}

	return nil
}

// DeleteBranch removes the named branch from the manager's repo root.
//
// Note: rift workspaces have their own `.git`, so branches created inside
// a snapshot do NOT exist in the source repo. Calling this for a rift
// agent is effectively a no-op (git will error with "branch not found")
// and the call site already discards the error. The branch dies with the
// snapshot in Remove.
func (m *Manager) DeleteBranch(branchName string) error {
	cmd := exec.Command("git", "branch", "-D", branchName)
	cmd.Dir = m.repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete branch: %s: %w", string(output), err)
	}

	return nil
}

func isRiftWorkspace(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".rift"))
	return err == nil
}
