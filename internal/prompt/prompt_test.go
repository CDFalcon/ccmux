package prompt

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "ccmux-prompt-test")
	if err != nil {
		t.Fatal(err)
	}

	s := &Store{
		filePath: filepath.Join(tmpDir, "prompts.json"),
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return s, cleanup
}

func TestAdd_ShouldStorePrompt_GivenValidPrompt(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	p := &Prompt{
		Name:    "test-prompt",
		Content: "You are a helpful assistant.",
	}

	// Execute.
	err := store.Add(p)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID == "" {
		t.Error("expected ID to be generated")
	}
	retrieved, err := store.Get(p.ID)
	if err != nil {
		t.Fatalf("failed to retrieve prompt: %v", err)
	}
	if retrieved.Name != "test-prompt" {
		t.Errorf("expected name 'test-prompt', got '%s'", retrieved.Name)
	}
	if retrieved.Content != "You are a helpful assistant." {
		t.Errorf("expected content to match, got '%s'", retrieved.Content)
	}
	if retrieved.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestAdd_ShouldSetTimestamps_GivenNewPrompt(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	p := &Prompt{Name: "ts-test", Content: "content"}

	// Execute.
	err := store.Add(p)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if p.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestList_ShouldReturnAllPrompts_GivenMultiplePrompts(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Prompt{Name: "prompt-a", Content: "a"})
	store.Add(&Prompt{Name: "prompt-b", Content: "b"})

	// Execute.
	prompts, err := store.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prompts) != 2 {
		t.Errorf("expected 2 prompts, got %d", len(prompts))
	}
}

func TestList_ShouldReturnSortedByName_GivenMultiplePrompts(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	store.Add(&Prompt{Name: "zebra", Content: "z"})
	store.Add(&Prompt{Name: "alpha", Content: "a"})

	// Execute.
	prompts, err := store.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompts[0].Name != "alpha" {
		t.Errorf("expected first prompt to be 'alpha', got '%s'", prompts[0].Name)
	}
}

func TestList_ShouldReturnEmpty_GivenNoPrompts(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	prompts, err := store.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prompts) != 0 {
		t.Errorf("expected 0 prompts, got %d", len(prompts))
	}
}

func TestRemove_ShouldDeletePrompt_GivenValidID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	p := &Prompt{Name: "to-remove", Content: "content"}
	store.Add(p)

	// Execute.
	err := store.Remove(p.ID)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prompts, _ := store.List()
	if len(prompts) != 0 {
		t.Errorf("expected 0 prompts after removal, got %d", len(prompts))
	}
}

func TestRemove_ShouldFail_GivenNonExistentID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	err := store.Remove("nonexistent")

	// Assert.
	if err == nil {
		t.Error("expected error for non-existent prompt, got nil")
	}
}

func TestUpdate_ShouldModifyPrompt_GivenValidID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	p := &Prompt{Name: "updatable", Content: "old content"}
	store.Add(p)

	// Execute.
	err := store.Update(p.ID, func(pr *Prompt) {
		pr.Name = "updated"
		pr.Content = "new content"
		pr.IsDefault = true
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get(p.ID)
	if retrieved.Name != "updated" {
		t.Errorf("expected name 'updated', got '%s'", retrieved.Name)
	}
	if retrieved.Content != "new content" {
		t.Errorf("expected content 'new content', got '%s'", retrieved.Content)
	}
	if !retrieved.IsDefault {
		t.Error("expected IsDefault to be true")
	}
}

func TestUpdate_ShouldFail_GivenNonExistentID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	err := store.Update("ghost", func(p *Prompt) {
		p.Name = "nope"
	})

	// Assert.
	if err == nil {
		t.Error("expected error for non-existent prompt, got nil")
	}
}

func TestUpdate_ShouldUpdateTimestamp_GivenModification(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	p := &Prompt{Name: "ts-update", Content: "content"}
	store.Add(p)
	originalUpdated := p.UpdatedAt

	// Execute.
	err := store.Update(p.ID, func(pr *Prompt) {
		pr.Content = "modified"
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get(p.ID)
	if !retrieved.UpdatedAt.After(originalUpdated) || retrieved.UpdatedAt.Equal(originalUpdated) {
		t.Error("expected UpdatedAt to be later than original")
	}
}

func TestAppliesToProject_ShouldReturnTrue_GivenNoProjectNames(t *testing.T) {
	// Setup.
	p := &Prompt{Name: "global", Content: "content"}

	// Execute.
	result := p.AppliesToProject("any-project")

	// Assert.
	if !result {
		t.Error("expected prompt with no project names to apply to any project")
	}
}

func TestAppliesToProject_ShouldReturnTrue_GivenMatchingProject(t *testing.T) {
	// Setup.
	p := &Prompt{Name: "scoped", Content: "content", ProjectNames: []string{"proj-a", "proj-b"}}

	// Execute.
	result := p.AppliesToProject("proj-b")

	// Assert.
	if !result {
		t.Error("expected prompt to apply to matching project")
	}
}

func TestAppliesToProject_ShouldReturnFalse_GivenNonMatchingProject(t *testing.T) {
	// Setup.
	p := &Prompt{Name: "scoped", Content: "content", ProjectNames: []string{"proj-a"}}

	// Execute.
	result := p.AppliesToProject("proj-c")

	// Assert.
	if result {
		t.Error("expected prompt to not apply to non-matching project")
	}
}

func TestAdd_ShouldPersistProjectNames_GivenScopedPrompt(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	p := &Prompt{Name: "scoped", Content: "content", ProjectNames: []string{"proj-a", "proj-b"}}

	// Execute.
	err := store.Add(p)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	retrieved, _ := store.Get(p.ID)
	if len(retrieved.ProjectNames) != 2 {
		t.Fatalf("expected 2 project names, got %d", len(retrieved.ProjectNames))
	}
	if retrieved.ProjectNames[0] != "proj-a" || retrieved.ProjectNames[1] != "proj-b" {
		t.Errorf("expected project names [proj-a, proj-b], got %v", retrieved.ProjectNames)
	}
}

func TestGet_ShouldFail_GivenNonExistentID(t *testing.T) {
	// Setup.
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Execute.
	_, err := store.Get("nonexistent")

	// Assert.
	if err == nil {
		t.Error("expected error for non-existent prompt, got nil")
	}
}
