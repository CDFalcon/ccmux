package project

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/CDFalcon/ccmux/internal/harness"
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

func TestList_ShouldReturnInInsertionOrder_GivenMultipleProjects(t *testing.T) {
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
	if projects[0].Name != "zebra" {
		t.Errorf("expected first project to be 'zebra' (insertion order), got '%s'", projects[0].Name)
	}
	if projects[1].Name != "alpha" {
		t.Errorf("expected second project to be 'alpha', got '%s'", projects[1].Name)
	}
}

func TestMove_ShouldShiftDown_GivenDeltaPlusOne(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "first", Path: repoDir})
	store.Add(&Project{Name: "second", Path: repoDir})
	store.Add(&Project{Name: "third", Path: repoDir})

	// Execute.
	err := store.Move("first", 1)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	projects, _ := store.List()
	names := []string{projects[0].Name, projects[1].Name, projects[2].Name}
	want := []string{"second", "first", "third"}
	if names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Errorf("expected order %v, got %v", want, names)
	}
}

func TestMove_ShouldShiftUp_GivenDeltaMinusOne(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "first", Path: repoDir})
	store.Add(&Project{Name: "second", Path: repoDir})
	store.Add(&Project{Name: "third", Path: repoDir})

	// Execute.
	err := store.Move("third", -1)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	projects, _ := store.List()
	names := []string{projects[0].Name, projects[1].Name, projects[2].Name}
	want := []string{"first", "third", "second"}
	if names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Errorf("expected order %v, got %v", want, names)
	}
}

func TestMove_ShouldNoOp_GivenBoundary(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "first", Path: repoDir})
	store.Add(&Project{Name: "second", Path: repoDir})

	// Execute.
	errUp := store.Move("first", -1)
	errDown := store.Move("second", 1)

	// Assert.
	if errUp != nil {
		t.Fatalf("unexpected error moving top item up: %v", errUp)
	}
	if errDown != nil {
		t.Fatalf("unexpected error moving bottom item down: %v", errDown)
	}
	projects, _ := store.List()
	if projects[0].Name != "first" || projects[1].Name != "second" {
		t.Errorf("expected order unchanged at boundaries, got [%s, %s]", projects[0].Name, projects[1].Name)
	}
}

func TestMove_ShouldFail_GivenInvalidDelta(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "p", Path: repoDir})

	// Execute.
	err := store.Move("p", 2)

	// Assert.
	if err == nil {
		t.Error("expected error for delta=2, got nil")
	}
}

func TestMove_ShouldFail_GivenUnknownProject(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	err := store.Move("ghost", 1)

	// Assert.
	if err == nil {
		t.Error("expected error for unknown project, got nil")
	}
}

func TestMove_ShouldPersist_AcrossReloads(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "a", Path: repoDir})
	store.Add(&Project{Name: "b", Path: repoDir})
	store.Add(&Project{Name: "c", Path: repoDir})
	store.Move("c", -1)
	store.Move("c", -1)

	// Execute (new store pointing at the same file simulates a restart).
	store2 := &Store{filePath: store.filePath}
	projects, err := store2.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"c", "a", "b"}
	for i, n := range want {
		if projects[i].Name != n {
			t.Errorf("position %d: expected %q, got %q", i, n, projects[i].Name)
		}
	}
}

func TestRemove_ShouldDropFromOrder(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "a", Path: repoDir})
	store.Add(&Project{Name: "b", Path: repoDir})
	store.Add(&Project{Name: "c", Path: repoDir})

	// Execute.
	if err := store.Remove("b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assert.
	projects, _ := store.List()
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	if projects[0].Name != "a" || projects[1].Name != "c" {
		t.Errorf("expected order [a, c], got [%s, %s]", projects[0].Name, projects[1].Name)
	}
}

func TestMigrationV6ToV7_ShouldSeedOrderAlphabetically(t *testing.T) {
	// Setup.
	v6Data := `{
		"version": 6,
		"projects": {
			"zebra": {"name": "zebra", "path": "/home/user/zebra"},
			"alpha": {"name": "alpha", "path": "/home/user/alpha"},
			"mango": {"name": "mango", "path": "/home/user/mango"}
		}
	}`

	// Execute.
	result, err := migrations.Migrate([]byte(v6Data), 6, 7)

	// Assert.
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	var store storeData
	if err := json.Unmarshal(result, &store); err != nil {
		t.Fatalf("failed to parse migrated data: %v", err)
	}
	if store.Version != 7 {
		t.Errorf("expected version 7, got %d", store.Version)
	}
	want := []string{"alpha", "mango", "zebra"}
	if len(store.Order) != len(want) {
		t.Fatalf("expected order length %d, got %d (%v)", len(want), len(store.Order), store.Order)
	}
	for i, n := range want {
		if store.Order[i] != n {
			t.Errorf("position %d: expected %q, got %q", i, n, store.Order[i])
		}
	}
}

func TestMigrationV6ToV7_ShouldPreserveExistingOrder(t *testing.T) {
	// Setup: simulate a hand-edited file that already has an order array
	// (e.g. someone re-upgraded after a downgrade). The migration must not
	// clobber a non-empty Order.
	v6Data := `{
		"version": 6,
		"projects": {
			"alpha": {"name": "alpha", "path": "/home/user/alpha"},
			"zebra": {"name": "zebra", "path": "/home/user/zebra"}
		},
		"order": ["zebra", "alpha"]
	}`

	// Execute.
	result, err := migrations.Migrate([]byte(v6Data), 6, 7)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Assert.
	var store storeData
	if err := json.Unmarshal(result, &store); err != nil {
		t.Fatalf("failed to parse migrated data: %v", err)
	}
	if len(store.Order) != 2 || store.Order[0] != "zebra" || store.Order[1] != "alpha" {
		t.Errorf("expected order [zebra, alpha], got %v", store.Order)
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
	// Setup. Fast-worktree projects only need to be a git repo at Add
	// time — rift init runs as a separate setup step, so the store
	// itself does not require the `.rift` marker to be present.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()

	project := &Project{
		Name:             "fast-project",
		Path:             repoDir,
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

func TestAdd_ShouldFail_GivenFastWorktreesOnNonGitPath(t *testing.T) {
	// Setup.
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	tmpDir, _ := os.MkdirTemp("", "not-a-repo")
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
		t.Error("expected error when fast-worktree project path is not a git repo")
	}
}

func TestIsRiftInitialized_ShouldReturnTrue_GivenDirWithRiftMarker(t *testing.T) {
	// Setup.
	tmpDir, _ := os.MkdirTemp("", "rift-marker-test")
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, ".rift"), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	// Execute.
	result := IsRiftInitialized(tmpDir)

	// Assert.
	if !result {
		t.Error("expected true for directory with .rift marker")
	}
}

func TestIsRiftInitialized_ShouldReturnFalse_GivenDirWithoutRiftMarker(t *testing.T) {
	// Setup.
	tmpDir, _ := os.MkdirTemp("", "no-rift-test")
	defer os.RemoveAll(tmpDir)

	// Execute.
	result := IsRiftInitialized(tmpDir)

	// Assert.
	if result {
		t.Error("expected false for directory without .rift marker")
	}
}

func TestUpdate_ShouldToggleFastWorktrees_GivenGitRepo(t *testing.T) {
	// Setup. Toggling UseFastWorktrees on an existing git repo is
	// permitted — rift init runs separately via the TUI's setup flow.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "toggleable", Path: repoDir})

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

func TestRiftInit_ShouldFail_GivenNoRiftInstalled(t *testing.T) {
	// Setup.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", "/nonexistent")
	defer os.Setenv("PATH", origPath)

	// Execute.
	_, err := RiftInit("/some/path", nil)

	// Assert.
	if err == nil {
		t.Error("expected error when rift is not installed")
	}
}

// EffectivePath used to swap between the project path and a separate
// fast-worktree root when the proj backend was in use. With rift the
// repo is initialised in place, so the helper always returns Path —
// these tests guard that invariant. They also cover the fast-worktrees
// flag explicitly so any reintroduction of a divergent root would fail.
func TestEffectivePath_ShouldReturnPath_GivenFastWorktreesEnabled(t *testing.T) {
	p := &Project{Name: "test", Path: "/home/user/repo", UseFastWorktrees: true}
	if got := p.EffectivePath(); got != "/home/user/repo" {
		t.Errorf("expected '/home/user/repo', got '%s'", got)
	}
}

func TestEffectivePath_ShouldReturnPath_GivenFastWorktreesDisabled(t *testing.T) {
	p := &Project{Name: "test", Path: "/home/user/repo", UseFastWorktrees: false}
	if got := p.EffectivePath(); got != "/home/user/repo" {
		t.Errorf("expected '/home/user/repo', got '%s'", got)
	}
}

func TestUpdate_ShouldTogglingFastWorktreesOff_PreservesPath(t *testing.T) {
	// Setup. EffectivePath() always returns Path now, but exercising the
	// store round-trip still guards the Update validation path against
	// regressions where flipping the flag accidentally clears Path.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "revertable", Path: repoDir, UseFastWorktrees: true})

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
		t.Errorf("expected effective path '%s', got '%s'", repoDir, retrieved.EffectivePath())
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

// The v3→v4 migration sets `fast_worktree_path` for projects with
// `use_fast_worktrees: true`. That field has since been removed from the
// Project struct (v9→v10 drops it again), so we have to inspect the
// migration's intermediate JSON output directly rather than round-tripping
// through json.Unmarshal into Project.
func TestMigrationV3ToV4_ShouldSetFastWorktreePathInJSON_GivenFastWorktreeProject(t *testing.T) {
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
	var raw struct {
		Version  int                        `json:"version"`
		Projects map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(result, &raw); err != nil {
		t.Fatalf("failed to parse migrated data: %v", err)
	}
	if raw.Version != 4 {
		t.Errorf("expected version 4, got %d", raw.Version)
	}
	var fastProj map[string]interface{}
	json.Unmarshal(raw.Projects["fast-proj"], &fastProj)
	if got, _ := fastProj["fast_worktree_path"].(string); got != "/proj/projects/myrepo" {
		t.Errorf("expected fast_worktree_path '/proj/projects/myrepo', got %v", fastProj["fast_worktree_path"])
	}
	var normalProj map[string]interface{}
	json.Unmarshal(raw.Projects["normal-proj"], &normalProj)
	if _, exists := normalProj["fast_worktree_path"]; exists {
		t.Errorf("expected fast_worktree_path absent for normal project, got %v", normalProj["fast_worktree_path"])
	}
}

// v9→v10 switches the fast-worktree backend from proj to rift and drops
// `fast_worktree_path` from the persisted shape. Verify the field is
// scrubbed for both proj-era and post-proj projects.
func TestMigrationV9ToV10_ShouldDropFastWorktreePath(t *testing.T) {
	// Setup.
	v9Data := `{
		"version": 9,
		"projects": {
			"fast-proj": {
				"name": "fast-proj",
				"path": "/home/user/myrepo",
				"use_fast_worktrees": true,
				"fast_worktree_path": "/proj/projects/myrepo"
			},
			"normal-proj": {
				"name": "normal-proj",
				"path": "/home/user/repo"
			}
		}
	}`

	// Execute.
	result, err := migrations.Migrate([]byte(v9Data), 9, 10)

	// Assert.
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	var raw struct {
		Version  int                        `json:"version"`
		Projects map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(result, &raw); err != nil {
		t.Fatalf("failed to parse migrated data: %v", err)
	}
	if raw.Version != 10 {
		t.Errorf("expected version 10, got %d", raw.Version)
	}
	for _, name := range []string{"fast-proj", "normal-proj"} {
		var proj map[string]interface{}
		json.Unmarshal(raw.Projects[name], &proj)
		if _, exists := proj["fast_worktree_path"]; exists {
			t.Errorf("%s: expected fast_worktree_path to be dropped, got %v", name, proj["fast_worktree_path"])
		}
	}
	// fast-proj should keep use_fast_worktrees: rift inherits the flag, the
	// user just has to run rift init the next time they spawn an agent.
	var fastProj map[string]interface{}
	json.Unmarshal(raw.Projects["fast-proj"], &fastProj)
	if got, _ := fastProj["use_fast_worktrees"].(bool); !got {
		t.Errorf("expected use_fast_worktrees preserved for fast-proj, got %v", fastProj["use_fast_worktrees"])
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

func TestEffectiveDraftPRs_ShouldDefaultToTrue_GivenUnset(t *testing.T) {
	// Setup.
	p := &Project{Name: "p", Path: "/repo"}

	// Execute & Assert.
	if !p.EffectiveDraftPRs() {
		t.Error("expected EffectiveDraftPRs to default to true when DraftPRs is unset")
	}
}

func TestEffectiveDraftPRs_ShouldReflectExplicitValue_GivenSet(t *testing.T) {
	// Setup.
	yes, no := true, false

	// Execute & Assert.
	if !(&Project{DraftPRs: &yes}).EffectiveDraftPRs() {
		t.Error("expected EffectiveDraftPRs to be true when DraftPRs is explicitly true")
	}
	if (&Project{DraftPRs: &no}).EffectiveDraftPRs() {
		t.Error("expected EffectiveDraftPRs to be false when DraftPRs is explicitly false")
	}
}

func TestUpdate_ShouldPersistDraftPRs_GivenFalse(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Project{Name: "no-draft", Path: repoDir})

	// Execute.
	err := store.Update("no-draft", func(p *Project) {
		no := false
		p.DraftPRs = &no
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("no-draft")
	if retrieved.DraftPRs == nil || *retrieved.DraftPRs {
		t.Error("expected DraftPRs to persist as false")
	}
	if retrieved.EffectiveDraftPRs() {
		t.Error("expected EffectiveDraftPRs to be false after persisting false")
	}
}

func TestAdd_ShouldOmitDraftPRs_GivenUnset(t *testing.T) {
	// Setup.
	store, repoDir, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	store.Add(&Project{Name: "default-draft", Path: repoDir})

	// Assert.
	raw, _ := os.ReadFile(store.filePath)
	var data map[string]interface{}
	json.Unmarshal(raw, &data)
	projects := data["projects"].(map[string]interface{})
	proj := projects["default-draft"].(map[string]interface{})
	if _, exists := proj["draft_prs"]; exists {
		t.Error("expected draft_prs to be omitted from JSON when unset")
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

func TestMigrationV7ToV8_ShouldPreserveProjectsAndDefaultHarnessEmpty(t *testing.T) {
	// Setup.
	v7Data := `{
		"version": 7,
		"projects": {
			"my-proj": {"name": "my-proj", "path": "/home/user/repo"}
		},
		"order": ["my-proj"]
	}`

	// Execute.
	result, err := migrations.Migrate([]byte(v7Data), 7, 8)

	// Assert.
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	var store storeData
	if err := json.Unmarshal(result, &store); err != nil {
		t.Fatalf("failed to parse migrated data: %v", err)
	}
	proj := store.Projects["my-proj"]
	if proj == nil {
		t.Fatal("expected my-proj to survive the migration")
	}
	if proj.DefaultHarness != "" {
		t.Errorf("expected empty default harness, got %q", proj.DefaultHarness)
	}
	if proj.EffectiveHarness() != harness.Default {
		t.Errorf("expected EffectiveHarness to fall back to default, got %q", proj.EffectiveHarness())
	}
}

func TestEffectiveHarness_ShouldReflectStoredValue(t *testing.T) {
	cases := map[string]harness.Type{
		"":       harness.Claude,
		"claude": harness.Claude,
		"codex":  harness.Codex,
		"bogus":  harness.Claude,
	}
	for stored, want := range cases {
		p := &Project{Name: "p", DefaultHarness: stored}
		if got := p.EffectiveHarness(); got != want {
			t.Errorf("DefaultHarness=%q: EffectiveHarness()=%q, want %q", stored, got, want)
		}
	}
}
