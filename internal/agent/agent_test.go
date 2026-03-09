package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestList_ShouldReturnAgentsSortedByCreatedAt_GivenMultipleAgents(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	now := time.Now()
	data := &storeData{
		Version: CurrentSchemaVersion,
		Agents: map[string]*Agent{
			"agent-c": {ID: "agent-c", Task: "Third", CreatedAt: now.Add(2 * time.Second)},
			"agent-a": {ID: "agent-a", Task: "First", CreatedAt: now},
			"agent-b": {ID: "agent-b", Task: "Second", CreatedAt: now.Add(1 * time.Second)},
		},
	}
	store.save(data)

	// Execute.
	agents, err := store.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(agents))
	}
	expectedOrder := []string{"agent-a", "agent-b", "agent-c"}
	for i, expected := range expectedOrder {
		if agents[i].ID != expected {
			t.Errorf("expected agents[%d].ID = %s, got %s", i, expected, agents[i].ID)
		}
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
