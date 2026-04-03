package project

import (
	"encoding/json"
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
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("updatable")
	if retrieved.DefaultBaseBranch != "origin/main" {
		t.Errorf("expected base branch 'origin/main', got '%s'", retrieved.DefaultBaseBranch)
	}
}

func TestUpdate_ShouldFail_GivenNonExistentProject(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	err := store.Update("ghost", func(p *Project) {
		p.DefaultBaseBranch = "origin/main"
	})

	// Assert.
	if err == nil {
		t.Error("expected error for non-existent project, got nil")
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

func TestEffectivePath_ShouldReturnFastWorktreePath_GivenFastWorktreesEnabled(t *testing.T) {
	// Setup.
	p := &Project{
		Name:             "test",
		Path:             "/home/user/repo",
		FastWorktreePath: "/proj/projects/repo",
		UseFastWorktrees: true,
	}

	// Execute.
	result := p.EffectivePath()

	// Assert.
	if result != "/proj/projects/repo" {
		t.Errorf("expected '/proj/projects/repo', got '%s'", result)
	}
}

func TestEffectivePath_ShouldReturnBasePath_GivenFastWorktreesDisabled(t *testing.T) {
	// Setup.
	p := &Project{
		Name:             "test",
		Path:             "/home/user/repo",
		FastWorktreePath: "/proj/projects/repo",
		UseFastWorktrees: false,
	}

	// Execute.
	result := p.EffectivePath()

	// Assert.
	if result != "/home/user/repo" {
		t.Errorf("expected '/home/user/repo', got '%s'", result)
	}
}

func TestEffectivePath_ShouldReturnBasePath_GivenNoFastWorktreePath(t *testing.T) {
	// Setup.
	p := &Project{
		Name:             "test",
		Path:             "/home/user/repo",
		UseFastWorktrees: true,
	}

	// Execute.
	result := p.EffectivePath()

	// Assert.
	if result != "/home/user/repo" {
		t.Errorf("expected '/home/user/repo', got '%s'", result)
	}
}

func TestAdd_ShouldStoreBothPaths_GivenFastWorktreesEnabled(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	projDir := t.TempDir()
	os.MkdirAll(filepath.Join(projDir, ".repo"), 0755)

	repoDir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	cmd.Run()

	project := &Project{
		Name:             "dual-path",
		Path:             repoDir,
		FastWorktreePath: projDir,
		UseFastWorktrees: true,
	}

	// Execute.
	err := store.Add(project)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("dual-path")
	if retrieved.Path != repoDir {
		t.Errorf("expected base path '%s', got '%s'", repoDir, retrieved.Path)
	}
	if retrieved.FastWorktreePath != projDir {
		t.Errorf("expected fast worktree path '%s', got '%s'", projDir, retrieved.FastWorktreePath)
	}
}

func TestUpdate_ShouldRevertToBasePath_GivenFastWorktreesDisabled(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	projDir := t.TempDir()
	os.MkdirAll(filepath.Join(projDir, ".repo"), 0755)

	store.Add(&Project{
		Name:             "revertable",
		Path:             repoDir,
		FastWorktreePath: projDir,
		UseFastWorktrees: true,
	})

	// Execute.
	err := store.Update("revertable", func(p *Project) {
		p.UseFastWorktrees = false
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("revertable")
	if retrieved.EffectivePath() != repoDir {
		t.Errorf("expected effective path to revert to '%s', got '%s'", repoDir, retrieved.EffectivePath())
	}
}

func TestIsSettingUp_ShouldReturnTrue_GivenSettingUpStatus(t *testing.T) {
	// Setup.
	p := &Project{Name: "test", SetupStatus: SetupStatusSettingUp}

	// Execute.
	result := p.IsSettingUp()

	// Assert.
	if !result {
		t.Error("expected IsSettingUp to return true")
	}
}

func TestIsSettingUp_ShouldReturnFalse_GivenEmptyStatus(t *testing.T) {
	// Setup.
	p := &Project{Name: "test"}

	// Execute.
	result := p.IsSettingUp()

	// Assert.
	if result {
		t.Error("expected IsSettingUp to return false")
	}
}

func TestSetupStatus_ShouldPersist_GivenStore(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "setup-test", Path: repoDir})

	// Execute.
	err := store.Update("setup-test", func(p *Project) {
		p.SetupStatus = SetupStatusSettingUp
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("setup-test")
	if !retrieved.IsSettingUp() {
		t.Error("expected project to be in setting up state")
	}
}

func TestAdd_ShouldSucceed_GivenFastWorktreesWithSettingUpStatus(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()

	p := &Project{
		Name:             "importing-project",
		Path:             repoDir,
		UseFastWorktrees: true,
		SetupStatus:      SetupStatusSettingUp,
	}

	// Execute.
	err := store.Add(p)

	// Assert.
	if err != nil {
		t.Fatalf("expected no error for setting_up project, got: %v", err)
	}
	retrieved, _ := store.Get("importing-project")
	if !retrieved.UseFastWorktrees {
		t.Error("expected UseFastWorktrees to be true")
	}
	if !retrieved.IsSettingUp() {
		t.Error("expected project to be in setting up state")
	}
}

func TestUpdate_ShouldSucceed_GivenFastWorktreesWithSettingUpStatus(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "will-import", Path: repoDir})

	// Execute.
	err := store.Update("will-import", func(p *Project) {
		p.UseFastWorktrees = true
		p.SetupStatus = SetupStatusSettingUp
	})

	// Assert.
	if err != nil {
		t.Fatalf("expected no error for setting_up project, got: %v", err)
	}
	retrieved, _ := store.Get("will-import")
	if !retrieved.UseFastWorktrees {
		t.Error("expected UseFastWorktrees to be true")
	}
	if !retrieved.IsSettingUp() {
		t.Error("expected project to be in setting up state")
	}
}

func TestUpdate_ShouldPersistScripts_GivenStartupAndTeardownScripts(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "scripted", Path: repoDir})

	// Execute.
	err := store.Update("scripted", func(p *Project) {
		p.StartupScript = "/path/to/startup.sh"
		p.TeardownScript = "/path/to/teardown.sh"
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("scripted")
	if retrieved.StartupScript != "/path/to/startup.sh" {
		t.Errorf("expected startup script '/path/to/startup.sh', got '%s'", retrieved.StartupScript)
	}
	if retrieved.TeardownScript != "/path/to/teardown.sh" {
		t.Errorf("expected teardown script '/path/to/teardown.sh', got '%s'", retrieved.TeardownScript)
	}
}

func TestAdd_ShouldOmitScripts_GivenNoScriptsSet(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	store.Add(&Project{Name: "no-scripts", Path: repoDir})

	// Assert.
	raw, _ := os.ReadFile(store.filePath)
	var data map[string]interface{}
	json.Unmarshal(raw, &data)
	projects := data["projects"].(map[string]interface{})
	proj := projects["no-scripts"].(map[string]interface{})
	if _, exists := proj["startup_script"]; exists {
		t.Error("expected startup_script to be omitted from JSON")
	}
	if _, exists := proj["teardown_script"]; exists {
		t.Error("expected teardown_script to be omitted from JSON")
	}
}

func TestMigrationV4ToV5_ShouldPreserveExistingFields(t *testing.T) {
	// Setup.
	v4Data := `{
		"version": 4,
		"projects": {
			"my-proj": {
				"name": "my-proj",
				"path": "/home/user/repo",
				"default_base_branch": "origin/main"
			}
		}
	}`

	// Execute.
	result, err := migrations.Migrate([]byte(v4Data), 4, 5)

	// Assert.
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	var store storeData
	if err := json.Unmarshal(result, &store); err != nil {
		t.Fatalf("failed to parse migrated data: %v", err)
	}
	proj := store.Projects["my-proj"]
	if proj.Path != "/home/user/repo" {
		t.Errorf("expected path '/home/user/repo', got '%s'", proj.Path)
	}
	if proj.DefaultBaseBranch != "origin/main" {
		t.Errorf("expected base branch 'origin/main', got '%s'", proj.DefaultBaseBranch)
	}
	if proj.StartupScript != "" {
		t.Errorf("expected empty startup script, got '%s'", proj.StartupScript)
	}
	if proj.TeardownScript != "" {
		t.Errorf("expected empty teardown script, got '%s'", proj.TeardownScript)
	}
}

func TestMigrationV3ToV4_ShouldSetFastWorktreePath_GivenFastWorktreeProject(t *testing.T) {
	// Setup.
	v3Data := `{
		"version": 3,
		"projects": {
			"fast-proj": {
				"name": "fast-proj",
				"path": "/proj/projects/myrepo",
				"use_fast_worktrees": true
			},
			"normal-proj": {
				"name": "normal-proj",
				"path": "/home/user/repo"
			}
		}
	}`

	// Execute.
	result, err := migrations.Migrate([]byte(v3Data), 3, 4)

	// Assert.
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	var store storeData
	if err := json.Unmarshal(result, &store); err != nil {
		t.Fatalf("failed to parse migrated data: %v", err)
	}
	if store.Version != 4 {
		t.Errorf("expected version 4, got %d", store.Version)
	}
	fastProj := store.Projects["fast-proj"]
	if fastProj.FastWorktreePath != "/proj/projects/myrepo" {
		t.Errorf("expected fast worktree path '/proj/projects/myrepo', got '%s'", fastProj.FastWorktreePath)
	}
	normalProj := store.Projects["normal-proj"]
	if normalProj.FastWorktreePath != "" {
		t.Errorf("expected empty fast worktree path for normal project, got '%s'", normalProj.FastWorktreePath)
	}
}

func TestUpdate_ShouldPersistMergeWhenAccepted_GivenTrue(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "mergeable", Path: repoDir})

	// Execute.
	err := store.Update("mergeable", func(p *Project) {
		p.MergeWhenAccepted = true
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("mergeable")
	if !retrieved.MergeWhenAccepted {
		t.Error("expected MergeWhenAccepted to be true")
	}
}

func TestAdd_ShouldOmitMergeWhenAccepted_GivenFalse(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	store.Add(&Project{Name: "no-merge", Path: repoDir})

	// Assert.
	raw, _ := os.ReadFile(store.filePath)
	var data map[string]interface{}
	json.Unmarshal(raw, &data)
	projects := data["projects"].(map[string]interface{})
	proj := projects["no-merge"].(map[string]interface{})
	if _, exists := proj["merge_when_accepted"]; exists {
		t.Error("expected merge_when_accepted to be omitted from JSON")
	}
}

func TestMigrationV5ToV6_ShouldPreserveExistingFields(t *testing.T) {
	// Setup.
	v5Data := `{
		"version": 5,
		"projects": {
			"my-proj": {
				"name": "my-proj",
				"path": "/home/user/repo",
				"default_base_branch": "origin/main",
				"startup_script": "/path/to/startup.sh"
			}
		}
	}`

	// Execute.
	result, err := migrations.Migrate([]byte(v5Data), 5, 6)

	// Assert.
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	var store storeData
	if err := json.Unmarshal(result, &store); err != nil {
		t.Fatalf("failed to parse migrated data: %v", err)
	}
	proj := store.Projects["my-proj"]
	if proj.Path != "/home/user/repo" {
		t.Errorf("expected path '/home/user/repo', got '%s'", proj.Path)
	}
	if proj.StartupScript != "/path/to/startup.sh" {
		t.Errorf("expected startup script '/path/to/startup.sh', got '%s'", proj.StartupScript)
	}
	if proj.MergeWhenAccepted {
		t.Error("expected MergeWhenAccepted to default to false")
	}
}
