package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CDFalcon/ccmux/internal/agent"
)

const testRiftID = "01TESTRIFTID"

// installFakeRift puts a stand-in `rift` on PATH whose `remove` moves the
// workspace (its cwd) to <parent>/.trash/<id>-<name>, as rift 0.0.8 does.
func installFakeRift(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
set -e
[ "$1" = "remove" ] || { echo "fake rift: unsupported: $*" >&2; exit 2; }
here=$(pwd)
id=$(cat .rift)
mkdir -p "$(dirname "$here")/.trash"
mv "$here" "$(dirname "$here")/.trash/$id-$(basename "$here")"
`
	if err := os.WriteFile(filepath.Join(bin, "rift"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// makeRiftWorkspace turns repo into what `rift create` leaves behind: a `.rift`
// marker that git ignores through info/exclude.
func makeRiftWorkspace(t *testing.T, repo string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, ".rift"), []byte(testRiftID+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("/.rift\n"); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(filepath.Dir(repo), ".trash", testRiftID+"-"+filepath.Base(repo))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRemoveAgentWorktree_ShouldDeleteRiftTrash_GivenCleanWorkspaceTrackingClaudeDir
// covers the leak itself (`rift remove` only trashes, and nothing emptied the
// trash) and the robots repo's layout: it tracks files under .claude/, which
// teardown deletes. Inspecting after that delete would see every such
// workspace as dirty and keep it all.
func TestRemoveAgentWorktree_ShouldDeleteRiftTrash_GivenCleanWorkspaceTrackingClaudeDir(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)
	installFakeRift(t)
	wt := makeCleanPushedRepo(t, home)
	writeFile(t, filepath.Join(wt, ".claude", "plans", "plan.md"), "plan\n")
	runOrFail(t, wt, "git", "add", ".")
	runOrFail(t, wt, "git", "commit", "-m", "plan")
	runOrFail(t, wt, "git", "push")
	trashed := makeRiftWorkspace(t, wt)

	// Execute.
	err := removeAgentWorktree(&agent.Agent{WorktreePath: wt, BranchName: "ccmux/clean"})

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if dirExists(wt) {
		t.Errorf("expected workspace %s to be gone", wt)
	}
	if dirExists(trashed) {
		t.Errorf("expected the clean workspace's trashed copy %s to be deleted", trashed)
	}
}

func TestRemoveAgentWorktree_ShouldKeepRiftTrash_GivenUnpushedCommit(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)
	installFakeRift(t)
	wt := makeRepoWithUnpushedCommit(t, home)
	trashed := makeRiftWorkspace(t, wt)

	// Execute.
	err := removeAgentWorktree(&agent.Agent{WorktreePath: wt, BranchName: "ccmux/wip"})

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if dirExists(wt) {
		t.Errorf("expected workspace %s to be moved out of place", wt)
	}
	if !fileExists(filepath.Join(trashed, "wip.txt")) {
		t.Errorf("expected the trashed copy %s to keep the unpushed work", trashed)
	}
}

// TestRemoveAgentWorktree_ShouldKeepClaudeDir_GivenUncommittedPlan: the kept copy
// is only worth keeping whole. Deleting .claude first would drop the very file
// that made the workspace worth keeping.
func TestRemoveAgentWorktree_ShouldKeepClaudeDir_GivenUncommittedPlan(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)
	installFakeRift(t)
	wt := makeCleanPushedRepo(t, home)
	writeFile(t, filepath.Join(wt, ".claude", "plans", "draft.md"), "draft\n")
	trashed := makeRiftWorkspace(t, wt)

	// Execute.
	err := removeAgentWorktree(&agent.Agent{WorktreePath: wt, BranchName: "ccmux/draft"})

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(trashed, ".claude", "plans", "draft.md")) {
		t.Errorf("expected the trashed copy %s to keep the uncommitted .claude file", trashed)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
