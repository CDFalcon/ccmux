package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "ccmux-agent-test")
	if err != nil {
		t.Fatal(err)
	}

	s := &Store{
		filePath: filepath.Join(tmpDir, "agents.json"),
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return s, cleanup
}

func TestCreate_ShouldStoreAgent_GivenValidAgent(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	agent := &Agent{
		ID:           "test-1",
		Task:         "Test task",
		WorktreePath: "/tmp/test",
		BranchName:   "ccmux/test-1",
		BaseBranch:   "origin/master",
		TmuxWindow:     "%0",
		Status:       StatusRunning,
	}

	// Execute.
	err := store.Create(agent)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, err := store.Get("test-1")
	if err != nil {
		t.Fatalf("failed to retrieve agent: %v", err)
	}
	if retrieved.Task != "Test task" {
		t.Errorf("expected task 'Test task', got '%s'", retrieved.Task)
	}
}

func TestCreate_ShouldFail_GivenDuplicateID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	agent := &Agent{ID: "test-1", Task: "Task 1"}
	store.Create(agent)

	// Execute.
	err := store.Create(&Agent{ID: "test-1", Task: "Task 2"})

	// Assert.
	if err == nil {
		t.Error("expected error for duplicate ID, got nil")
	}
}

func TestList_ShouldReturnAllAgents_GivenMultipleAgents(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	store.Create(&Agent{ID: "agent-1", Task: "Task 1"})
	store.Create(&Agent{ID: "agent-2", Task: "Task 2"})

	// Execute.
	agents, err := store.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}

func TestUpdate_ShouldModifyAgent_GivenValidID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	store.Create(&Agent{ID: "test-1", Task: "Original task", Status: StatusRunning})

	// Execute.
	err := store.Update("test-1", func(a *Agent) {
		a.Status = StatusReady
		a.Task = "Updated task"
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	agent, _ := store.Get("test-1")
	if agent.Status != StatusReady {
		t.Errorf("expected status %s, got %s", StatusReady, agent.Status)
	}
	if agent.Task != "Updated task" {
		t.Errorf("expected task 'Updated task', got '%s'", agent.Task)
	}
}

func TestDelete_ShouldRemoveAgent_GivenValidID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	store.Create(&Agent{ID: "test-1", Task: "Task"})

	// Execute.
	err := store.Delete("test-1")

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	agents, _ := store.List()
	if len(agents) != 0 {
		t.Errorf("expected 0 agents after deletion, got %d", len(agents))
	}
}

func TestGet_ShouldFail_GivenNonexistentID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	_, err := store.Get("nonexistent")

	// Assert.
	if err == nil {
		t.Error("expected error for nonexistent agent, got nil")
	}
}

func TestUpdate_ShouldFail_GivenNonexistentID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	err := store.Update("nonexistent", func(a *Agent) {
		a.Task = "updated"
	})

	// Assert.
	if err == nil {
		t.Error("expected error for nonexistent agent, got nil")
	}
}

func TestDelete_ShouldFail_GivenNonexistentID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	err := store.Delete("nonexistent")

	// Assert.
	if err == nil {
		t.Error("expected error for nonexistent agent, got nil")
	}
}

func TestCreate_ShouldSetTimestamps_GivenNewAgent(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	err := store.Create(&Agent{ID: "ts-test", Task: "Task"})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get("ts-test")
	if retrieved.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if retrieved.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestUpdate_ShouldUpdateTimestamp_GivenValidUpdate(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	store.Create(&Agent{ID: "ts-test", Task: "Task", Status: StatusRunning})
	original, _ := store.Get("ts-test")
	originalUpdated := original.UpdatedAt

	// Execute.
	err := store.Update("ts-test", func(a *Agent) {
		a.Status = StatusReady
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated, _ := store.Get("ts-test")
	if !updated.UpdatedAt.After(originalUpdated) && !updated.UpdatedAt.Equal(originalUpdated) {
		t.Error("expected UpdatedAt to be updated")
	}
}

func TestList_ShouldReturnEmpty_GivenNoAgents(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	agents, err := store.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestStatusConstants_ShouldHaveExpectedValues(t *testing.T) {
	// Assert.
	if StatusSpawning != "spawning" {
		t.Errorf("expected 'spawning', got '%s'", StatusSpawning)
	}
	if StatusRunning != "running" {
		t.Errorf("expected 'running', got '%s'", StatusRunning)
	}
	if StatusReady != "ready" {
		t.Errorf("expected 'ready', got '%s'", StatusReady)
	}
	if StatusKilling != "killing" {
		t.Errorf("expected 'killing', got '%s'", StatusKilling)
	}
	if StatusMerged != "merged" {
		t.Errorf("expected 'merged', got '%s'", StatusMerged)
	}
	if StatusFailed != "failed" {
		t.Errorf("expected 'failed', got '%s'", StatusFailed)
	}
}
