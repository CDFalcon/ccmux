package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupTestStore(t *testing.T) (*Store, string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "ccmux-project-test")
	if err != nil {
		t.Fatal(err)
	}

	s := &Store{
		filePath: filepath.Join(tmpDir, "projects.json"),
	}

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return s, repoDir, cleanup
}

func TestAdd_ShouldStoreProject_GivenValidProject(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	project := &Project{
		Name: "test-project",
		Path: repoDir,
	}

	// Execute.
	err := store.Add(project)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, err := store.Get("test-project")
	if err != nil {
		t.Fatalf("failed to retrieve project: %v", err)
	}
	if retrieved.Name != "test-project" {
		t.Errorf("expected name 'test-project', got '%s'", retrieved.Name)
	}
}

func TestAdd_ShouldFail_GivenNonGitRepo(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	tmpDir, _ := os.MkdirTemp("", "non-git")
	defer os.RemoveAll(tmpDir)

	project := &Project{
		Name: "bad-project",
		Path: tmpDir,
	}

	// Execute.
	err := store.Add(project)

	// Assert.
	if err == nil {
		t.Error("expected error for non-git repo, got nil")
	}
}

func TestAdd_ShouldFail_GivenDuplicateName(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "dup", Path: repoDir})

	// Execute.
	err := store.Add(&Project{Name: "dup", Path: repoDir})

	// Assert.
	if err == nil {
		t.Error("expected error for duplicate name, got nil")
	}
}

func TestList_ShouldReturnAllProjects_GivenMultipleProjects(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "proj-a", Path: repoDir})
	store.Add(&Project{Name: "proj-b", Path: repoDir})

	// Execute.
	projects, err := store.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects) != 2 {
		t.Errorf("expected 2 projects, got %d", len(projects))
	}
}

func TestList_ShouldReturnSortedByName_GivenMultipleProjects(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "zebra", Path: repoDir})
	store.Add(&Project{Name: "alpha", Path: repoDir})

	// Execute.
	projects, err := store.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if projects[0].Name != "alpha" {
		t.Errorf("expected first project to be 'alpha', got '%s'", projects[0].Name)
	}
}

func TestRemove_ShouldDeleteProject_GivenValidName(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "to-remove", Path: repoDir})

	// Execute.
	err := store.Remove("to-remove")

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	projects, _ := store.List()
	if len(projects) != 0 {
		t.Errorf("expected 0 projects after removal, got %d", len(projects))
	}
}

func TestGet_ShouldFail_GivenNonexistentName(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	_, err := store.Get("nonexistent")

	// Assert.
	if err == nil {
		t.Error("expected error for nonexistent project, got nil")
	}
}

func TestRemove_ShouldFail_GivenNonexistentName(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	err := store.Remove("nonexistent")

	// Assert.
	if err == nil {
		t.Error("expected error for nonexistent project, got nil")
	}
}

func TestAdd_ShouldStoreAbsolutePath_GivenRelativePath(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	project := &Project{
		Name: "abs-test",
		Path: repoDir,
	}

	// Execute.
	err := store.Add(project)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("abs-test")
	if retrieved.Path == "" {
		t.Error("expected path to be set")
	}
}

func TestList_ShouldReturnEmpty_GivenNoProjects(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	projects, err := store.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}
}
