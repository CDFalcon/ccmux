package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeRiftScript stands in for `rift remove`: it moves the workspace (its cwd)
// to <parent>/.trash/<id>-<name>, which is what rift 0.0.8 does.
const fakeRiftScript = `#!/bin/sh
set -e
[ "$1" = "remove" ] || { echo "fake rift: unsupported: $*" >&2; exit 2; }
here=$(pwd)
id=$(cat .rift)
mkdir -p "$(dirname "$here")/.trash"
mv "$here" "$(dirname "$here")/.trash/$id-$(basename "$here")"
`

func installFakeRift(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "rift"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func makeRiftWorkspace(t *testing.T, parent, name, id string) string {
	t.Helper()
	ws := filepath.Join(parent, name)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".rift"), []byte(id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "file.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRemove_ShouldDeleteTrashedCopy_GivenRiftWorkspace(t *testing.T) {
	// Setup.
	installFakeRift(t, fakeRiftScript)
	parent := t.TempDir()
	ws := makeRiftWorkspace(t, parent, "ccmux-1234abcd", "01TESTID")
	trashed := riftTrashPath(ws, "01TESTID")

	// Execute.
	err := NewManager(parent).Remove(ws, false)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if exists(ws) {
		t.Errorf("expected workspace %s to be gone", ws)
	}
	if exists(trashed) {
		t.Errorf("expected trashed copy %s to be deleted, not left for rift gc", trashed)
	}
}

func TestRemove_ShouldKeepTrashedCopy_GivenKeepRiftTrash(t *testing.T) {
	// Setup.
	installFakeRift(t, fakeRiftScript)
	parent := t.TempDir()
	ws := makeRiftWorkspace(t, parent, "ccmux-1234abcd", "01TESTID")
	trashed := riftTrashPath(ws, "01TESTID")

	// Execute.
	err := NewManager(parent).Remove(ws, true)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if exists(ws) {
		t.Errorf("expected workspace %s to be moved out of place", ws)
	}
	if !exists(filepath.Join(trashed, "file.txt")) {
		t.Errorf("expected trashed copy %s to be kept intact", trashed)
	}
}

// TestRemove_ShouldOnlyDeleteOwnTrashedCopy_GivenOtherTrash guards the reason
// ccmux doesn't just run `rift gc`: that empties the whole trash, including
// workspaces kept on purpose because they hold unpushed work.
func TestRemove_ShouldOnlyDeleteOwnTrashedCopy_GivenOtherTrash(t *testing.T) {
	// Setup.
	installFakeRift(t, fakeRiftScript)
	parent := t.TempDir()
	kept := makeRiftWorkspace(t, filepath.Join(parent, ".trash"), "01OTHERID-ccmux-feedbeef", "01OTHERID")
	ws := makeRiftWorkspace(t, parent, "ccmux-1234abcd", "01TESTID")

	// Execute.
	err := NewManager(parent).Remove(ws, false)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(kept, "file.txt")) {
		t.Errorf("expected unrelated trash %s to be untouched", kept)
	}
}

func TestRemove_ShouldLeaveTrash_GivenUnexpectedTrashLayout(t *testing.T) {
	// Setup: a rift that trashes somewhere ccmux doesn't expect.
	installFakeRift(t, `#!/bin/sh
set -e
here=$(pwd)
mkdir -p "$(dirname "$here")/.trash"
mv "$here" "$(dirname "$here")/.trash/elsewhere"
`)
	parent := t.TempDir()
	ws := makeRiftWorkspace(t, parent, "ccmux-1234abcd", "01TESTID")

	// Execute.
	err := NewManager(parent).Remove(ws, false)

	// Assert.
	if err == nil {
		t.Error("expected an error reporting the trashed copy could not be found")
	}
	if !exists(filepath.Join(parent, ".trash", "elsewhere", "file.txt")) {
		t.Error("expected the unrecognised trashed copy to be left alone")
	}
}

func TestPurgeRiftTrash_ShouldNotDelete_GivenMarkerMismatch(t *testing.T) {
	// Setup: the expected path exists but belongs to a different workspace.
	parent := t.TempDir()
	ws := filepath.Join(parent, "ccmux-1234abcd")
	other := makeRiftWorkspace(t, filepath.Join(parent, ".trash"), "01TESTID-ccmux-1234abcd", "01DIFFERENT")

	// Execute.
	err := purgeRiftTrash(ws, "01TESTID")

	// Assert.
	if err == nil {
		t.Error("expected an error for a marker mismatch")
	}
	if !exists(filepath.Join(other, "file.txt")) {
		t.Error("expected a directory with someone else's marker to be left alone")
	}
}

func TestPurgeRiftTrash_ShouldError_GivenNoID(t *testing.T) {
	// Execute.
	err := purgeRiftTrash(filepath.Join(t.TempDir(), "ccmux-1234abcd"), "")

	// Assert.
	if err == nil {
		t.Error("expected an error when the workspace had no id")
	}
}
