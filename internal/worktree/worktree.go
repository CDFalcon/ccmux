// Package worktree manages git worktree lifecycle.
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
//
// `rift remove` does not delete anything. It moves the workspace into rift's
// trash beside it, and only `rift gc` frees the space. Nothing ever ran
// `rift gc`, and it empties the whole trash at once, so every finished agent's
// workspace piled up there (465 of them by October 2026). So once rift has
// moved the workspace, Remove deletes that workspace's own trashed copy —
// unless keepRiftTrash is set. Callers set it when the workspace holds work
// that would be lost: a rift workspace keeps its branch in its own .git, so the
// trashed copy is then the only place that work still exists. keepRiftTrash
// has no effect on a plain git worktree.
func (m *Manager) Remove(worktreePath string, keepRiftTrash bool) error {
	if IsRift(worktreePath) {
		// Read the id now: it names the trashed copy, and the workspace is
		// about to move.
		id := riftID(worktreePath)

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
		if keepRiftTrash {
			return nil
		}
		return purgeRiftTrash(worktreePath, id)
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

// IsRift reports whether path is a rift workspace rather than a git worktree.
func IsRift(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".rift"))
	return err == nil
}

// riftID returns the workspace id recorded in path's `.rift` marker, or "" if
// there is none.
func riftID(path string) string {
	data, err := os.ReadFile(filepath.Join(path, ".rift"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// riftTrashPath is where `rift remove` moves the workspace at workspacePath:
// `<parent>/.trash/<id>-<name>`. This is rift's layout, not a documented
// interface, which is why purgeRiftTrash checks the marker before deleting.
func riftTrashPath(workspacePath, id string) string {
	return filepath.Join(filepath.Dir(workspacePath), ".trash", id+"-"+filepath.Base(workspacePath))
}

// purgeRiftTrash deletes the copy of a rift workspace that `rift remove` moved
// into trash. It deletes only a directory it can positively identify: the
// expected path must carry a `.rift` marker with the workspace's own id. If
// rift ever changes where trash goes, this reports an error and leaves the copy
// for `rift gc` rather than deleting a guess.
//
// rift's registry keeps a row for the trashed copy; `rift gc` drops rows whose
// directory is already gone, so deleting it here leaves rift consistent.
func purgeRiftTrash(workspacePath, id string) error {
	if id == "" {
		return fmt.Errorf("no id in %s, so its trashed copy can't be found; left for `rift gc`",
			filepath.Join(workspacePath, ".rift"))
	}
	trashed := riftTrashPath(workspacePath, id)
	if got := riftID(trashed); got != id {
		return fmt.Errorf("no trashed copy of rift workspace %s at %s; left for `rift gc`", id, trashed)
	}
	if err := os.RemoveAll(trashed); err != nil {
		return fmt.Errorf("failed to delete trashed rift workspace %s: %w", trashed, err)
	}
	return nil
}
