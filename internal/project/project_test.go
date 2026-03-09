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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %s: %v", args, string(out), err)
	}
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

func TestUpdate_ShouldModifyProject_GivenValidName(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "updatable", Path: repoDir})

	// Execute.
	err := store.Update("updatable", func(p *Project) {
		p.DefaultBaseBranch = "origin/main"
		p.CIWaitMinutes = 10
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("updatable")
	if retrieved.DefaultBaseBranch != "origin/main" {
		t.Errorf("expected base branch 'origin/main', got '%s'", retrieved.DefaultBaseBranch)
	}
	if retrieved.CIWaitMinutes != 10 {
		t.Errorf("expected CI wait 10, got %d", retrieved.CIWaitMinutes)
	}
}

func TestUpdate_ShouldFail_GivenNonExistentProject(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	err := store.Update("ghost", func(p *Project) {
		p.CIWaitMinutes = 5
	})

	// Assert.
	if err == nil {
		t.Error("expected error for non-existent project, got nil")
	}
}

func TestEffectiveCIWaitMinutes_ShouldReturnDefault_GivenZeroValue(t *testing.T) {
	// Setup.
	p := &Project{Name: "test", CIWaitMinutes: 0}

	// Execute.
	result := p.EffectiveCIWaitMinutes()

	// Assert.
	if result != DefaultCIWaitMinutes {
		t.Errorf("expected %d, got %d", DefaultCIWaitMinutes, result)
	}
}

func TestEffectiveCIWaitMinutes_ShouldReturnCustom_GivenPositiveValue(t *testing.T) {
	// Setup.
	p := &Project{Name: "test", CIWaitMinutes: 10}

	// Execute.
	result := p.EffectiveCIWaitMinutes()

	// Assert.
	if result != 10 {
		t.Errorf("expected 10, got %d", result)
	}
}

func TestEffectiveBaseBranch_ShouldReturnDefault_GivenEmptyValue(t *testing.T) {
	// Setup.
	p := &Project{Name: "test"}

	// Execute.
	result := p.EffectiveBaseBranch()

	// Assert.
	if result != "origin/master" {
		t.Errorf("expected 'origin/master', got '%s'", result)
	}
}

func TestEffectiveBaseBranch_ShouldReturnCustom_GivenNonEmptyValue(t *testing.T) {
	// Setup.
	p := &Project{Name: "test", DefaultBaseBranch: "origin/main"}

	// Execute.
	result := p.EffectiveBaseBranch()

	// Assert.
	if result != "origin/main" {
		t.Errorf("expected 'origin/main', got '%s'", result)
	}
}

func TestAdd_ShouldStoreProject_GivenFastWorktreesEnabled(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	tmpDir, _ := os.MkdirTemp("", "proj-dir")
	defer os.RemoveAll(tmpDir)
	repoDir := filepath.Join(tmpDir, ".repo")
	os.MkdirAll(repoDir, 0755)

	project := &Project{
		Name:             "fast-project",
		Path:             tmpDir,
		UseFastWorktrees: true,
	}

	// Execute.
	err := store.Add(project)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, err := store.Get("fast-project")
	if err != nil {
		t.Fatalf("failed to retrieve project: %v", err)
	}
	if !retrieved.UseFastWorktrees {
		t.Error("expected UseFastWorktrees to be true")
	}
}

func TestAdd_ShouldFail_GivenFastWorktreesWithNoProjDir(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	tmpDir, _ := os.MkdirTemp("", "no-proj")
	defer os.RemoveAll(tmpDir)

	project := &Project{
		Name:             "bad-fast-project",
		Path:             tmpDir,
		UseFastWorktrees: true,
	}

	// Execute.
	err := store.Add(project)

	// Assert.
	if err == nil {
		t.Error("expected error for missing .repo directory, got nil")
	}
}

func TestIsProjDirectory_ShouldReturnTrue_GivenDirWithRepo(t *testing.T) {
	// Setup.
	tmpDir, _ := os.MkdirTemp("", "proj-test")
	defer os.RemoveAll(tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".repo"), 0755)

	// Execute.
	result := IsProjDirectory(tmpDir)

	// Assert.
	if !result {
		t.Error("expected true for directory with .repo")
	}
}

func TestIsProjDirectory_ShouldReturnFalse_GivenDirWithoutRepo(t *testing.T) {
	// Setup.
	tmpDir, _ := os.MkdirTemp("", "no-proj-test")
	defer os.RemoveAll(tmpDir)

	// Execute.
	result := IsProjDirectory(tmpDir)

	// Assert.
	if result {
		t.Error("expected false for directory without .repo")
	}
}

func TestFindProjTemplateDir_ShouldReturnPath_GivenTemplateExists(t *testing.T) {
	// Setup.
	tmpDir, _ := os.MkdirTemp("", "proj-template-test")
	defer os.RemoveAll(tmpDir)
	templateDir := filepath.Join(tmpDir, "00-master")
	os.MkdirAll(templateDir, 0755)

	// Execute.
	result := FindProjTemplateDir(tmpDir)

	// Assert.
	if result != templateDir {
		t.Errorf("expected '%s', got '%s'", templateDir, result)
	}
}

func TestFindProjTemplateDir_ShouldReturnEmpty_GivenNoTemplate(t *testing.T) {
	// Setup.
	tmpDir, _ := os.MkdirTemp("", "no-template-test")
	defer os.RemoveAll(tmpDir)

	// Execute.
	result := FindProjTemplateDir(tmpDir)

	// Assert.
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestUpdate_ShouldToggleFastWorktrees_GivenProjDirectory(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	projDir := t.TempDir()
	os.MkdirAll(filepath.Join(projDir, ".repo"), 0755)
	store.Add(&Project{Name: "toggleable", Path: projDir, UseFastWorktrees: true})

	// Execute.
	err := store.Update("toggleable", func(p *Project) {
		p.UseFastWorktrees = true
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("toggleable")
	if !retrieved.UseFastWorktrees {
		t.Error("expected UseFastWorktrees to be true after update")
	}
}

func TestUpdate_ShouldFail_GivenFastWorktreesWithNoProjDir(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "not-proj", Path: repoDir})

	// Execute.
	err := store.Update("not-proj", func(p *Project) {
		p.UseFastWorktrees = true
	})

	// Assert.
	if err == nil {
		t.Error("expected error for missing .repo directory, got nil")
	}
}

func TestUpdate_ShouldFail_GivenNonGitRepoPath(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "will-break", Path: repoDir})
	badDir := t.TempDir()

	// Execute.
	err := store.Update("will-break", func(p *Project) {
		p.Path = badDir
	})

	// Assert.
	if err == nil {
		t.Error("expected error for non-git repo path, got nil")
	}
}

func TestDetectDefaultBranch_ShouldReturnMaster_GivenMasterBranch(t *testing.T) {
	// Setup.
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "checkout", "-b", "master")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")

	// Execute.
	branch := DetectDefaultBranch(dir)

	// Assert.
	if branch != "master" {
		t.Errorf("expected 'master', got '%s'", branch)
	}
}

func TestDetectDefaultBranch_ShouldReturnMain_GivenMainBranch(t *testing.T) {
	// Setup.
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")

	// Execute.
	branch := DetectDefaultBranch(dir)

	// Assert.
	if branch != "main" {
		t.Errorf("expected 'main', got '%s'", branch)
	}
}

func TestProjImport_ShouldFail_GivenNoProjInstalled(t *testing.T) {
	// Setup.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", "/nonexistent")
	defer os.Setenv("PATH", origPath)

	// Execute.
	_, err := ProjImport("/some/path", nil)

	// Assert.
	if err == nil {
		t.Error("expected error when proj is not installed")
	}
}


