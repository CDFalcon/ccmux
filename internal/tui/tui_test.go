package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func newTestModelWithBranches(allEntries []branchEntry) model {
	branchInput := textinput.New()
	branchInput.Placeholder = "origin/master"
	branchInput.Width = 50
	branchInput.CharLimit = 100

	taskInput := newAutoGrowTextarea("Describe the task...", 60)

	return model{
		view:             ViewNewTaskBranchInput,
		branchInput:      branchInput,
		taskInput:        taskInput,
		branchAllEntries: allEntries,
	}
}

func TestUpdateBranchFilter_ShouldReturnAllEntries_GivenEmptyQuery(t *testing.T) {
	// Setup.
	entries := []branchEntry{
		{tag: "(local)", name: "main"},
		{tag: "(local)", name: "feature-auth"},
		{tag: "(remote)", name: "origin/main"},
	}
	m := newTestModelWithBranches(entries)
	m.branchInput.SetValue("")

	// Execute.
	m.updateBranchFilter()

	// Assert.
	if len(m.branchFilteredEntries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(m.branchFilteredEntries))
	}
	if m.branchSearchIndex != 0 {
		t.Errorf("expected search index 0, got %d", m.branchSearchIndex)
	}
}

func TestUpdateBranchFilter_ShouldFilterByFuzzyMatch_GivenQuery(t *testing.T) {
	// Setup.
	entries := []branchEntry{
		{tag: "(local)", name: "main"},
		{tag: "(local)", name: "feature-auth"},
		{tag: "(remote)", name: "origin/main"},
		{tag: "(remote)", name: "origin/feature-deploy"},
	}
	m := newTestModelWithBranches(entries)
	m.branchInput.SetValue("auth")

	// Execute.
	m.updateBranchFilter()

	// Assert.
	if len(m.branchFilteredEntries) != 1 {
		t.Fatalf("expected 1 match, got %d", len(m.branchFilteredEntries))
	}
	if m.branchFilteredEntries[0].name != "feature-auth" {
		t.Errorf("expected 'feature-auth', got '%s'", m.branchFilteredEntries[0].name)
	}
	if m.branchFilteredEntries[0].tag != "(local)" {
		t.Errorf("expected '(local)' tag, got '%s'", m.branchFilteredEntries[0].tag)
	}
	if len(m.branchFilteredEntries[0].matchedIndexes) == 0 {
		t.Error("expected matchedIndexes to be populated")
	}
}

func TestUpdateBranchFilter_ShouldReturnEmpty_GivenNoMatches(t *testing.T) {
	// Setup.
	entries := []branchEntry{
		{tag: "(local)", name: "main"},
		{tag: "(remote)", name: "origin/main"},
	}
	m := newTestModelWithBranches(entries)
	m.branchInput.SetValue("zzzznotabranch")

	// Execute.
	m.updateBranchFilter()

	// Assert.
	if len(m.branchFilteredEntries) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(m.branchFilteredEntries))
	}
}

func TestUpdateBranchFilter_ShouldResetIndex_GivenIndexBeyondResults(t *testing.T) {
	// Setup.
	entries := []branchEntry{
		{tag: "(local)", name: "main"},
		{tag: "(local)", name: "feature-auth"},
		{tag: "(local)", name: "feature-deploy"},
	}
	m := newTestModelWithBranches(entries)
	m.branchSearchIndex = 2
	m.branchInput.SetValue("auth")

	// Execute.
	m.updateBranchFilter()

	// Assert.
	if m.branchSearchIndex != 0 {
		t.Errorf("expected search index reset to 0, got %d", m.branchSearchIndex)
	}
}

func TestUpdateBranchFilter_ShouldMatchMultipleBranches_GivenBroadQuery(t *testing.T) {
	// Setup.
	entries := []branchEntry{
		{tag: "(local)", name: "main"},
		{tag: "(local)", name: "feature-auth"},
		{tag: "(remote)", name: "origin/main"},
		{tag: "(remote)", name: "origin/feature-auth"},
	}
	m := newTestModelWithBranches(entries)
	m.branchInput.SetValue("main")

	// Execute.
	m.updateBranchFilter()

	// Assert.
	if len(m.branchFilteredEntries) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(m.branchFilteredEntries))
	}
	names := make([]string, len(m.branchFilteredEntries))
	for i, e := range m.branchFilteredEntries {
		names[i] = e.name
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "main") || !strings.Contains(joined, "origin/main") {
		t.Errorf("expected both 'main' and 'origin/main' in results, got: %s", joined)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldNavigateDown_GivenDownKey(t *testing.T) {
	// Setup.
	m := newTestModelWithBranches([]branchEntry{
		{tag: "(local)", name: "main"},
		{tag: "(local)", name: "develop"},
		{tag: "(remote)", name: "origin/main"},
	})
	m.branchFilteredEntries = make([]branchEntry, len(m.branchAllEntries))
	copy(m.branchFilteredEntries, m.branchAllEntries)
	m.branchSearchIndex = 0

	// Execute.
	result, _ := m.handleNewTaskBranchInputKeys(tea.KeyMsg{Type: tea.KeyDown})

	// Assert.
	rm := result.(model)
	if rm.branchSearchIndex != 1 {
		t.Errorf("expected search index 1, got %d", rm.branchSearchIndex)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldNavigateUp_GivenUpKey(t *testing.T) {
	// Setup.
	m := newTestModelWithBranches([]branchEntry{
		{tag: "(local)", name: "main"},
		{tag: "(local)", name: "develop"},
	})
	m.branchFilteredEntries = make([]branchEntry, len(m.branchAllEntries))
	copy(m.branchFilteredEntries, m.branchAllEntries)
	m.branchSearchIndex = 1

	// Execute.
	result, _ := m.handleNewTaskBranchInputKeys(tea.KeyMsg{Type: tea.KeyUp})

	// Assert.
	rm := result.(model)
	if rm.branchSearchIndex != 0 {
		t.Errorf("expected search index 0, got %d", rm.branchSearchIndex)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldNotGoBelowZero_GivenUpAtTop(t *testing.T) {
	// Setup.
	m := newTestModelWithBranches([]branchEntry{
		{tag: "(local)", name: "main"},
	})
	m.branchFilteredEntries = make([]branchEntry, len(m.branchAllEntries))
	copy(m.branchFilteredEntries, m.branchAllEntries)
	m.branchSearchIndex = 0

	// Execute.
	result, _ := m.handleNewTaskBranchInputKeys(tea.KeyMsg{Type: tea.KeyUp})

	// Assert.
	rm := result.(model)
	if rm.branchSearchIndex != 0 {
		t.Errorf("expected search index 0, got %d", rm.branchSearchIndex)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldNotExceedMax_GivenDownAtBottom(t *testing.T) {
	// Setup.
	m := newTestModelWithBranches([]branchEntry{
		{tag: "(local)", name: "main"},
		{tag: "(local)", name: "develop"},
	})
	m.branchFilteredEntries = make([]branchEntry, len(m.branchAllEntries))
	copy(m.branchFilteredEntries, m.branchAllEntries)
	m.branchSearchIndex = 1

	// Execute.
	result, _ := m.handleNewTaskBranchInputKeys(tea.KeyMsg{Type: tea.KeyDown})

	// Assert.
	rm := result.(model)
	if rm.branchSearchIndex != 1 {
		t.Errorf("expected search index 1, got %d", rm.branchSearchIndex)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldSelectBranch_GivenEnterWithResults(t *testing.T) {
	// Setup.
	m := newTestModelWithBranches([]branchEntry{
		{tag: "(local)", name: "main"},
		{tag: "(remote)", name: "origin/develop"},
	})
	m.branchFilteredEntries = make([]branchEntry, len(m.branchAllEntries))
	copy(m.branchFilteredEntries, m.branchAllEntries)
	m.branchSearchIndex = 1

	// Execute.
	result, _ := m.handleNewTaskBranchInputKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.spawnBranch != "origin/develop" {
		t.Errorf("expected 'origin/develop', got '%s'", rm.spawnBranch)
	}
	if rm.view != ViewNewTaskInput {
		t.Errorf("expected ViewNewTaskInput, got %d", rm.view)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldFallbackToInput_GivenEnterWithNoResults(t *testing.T) {
	// Setup.
	m := newTestModelWithBranches(nil)
	m.branchFilteredEntries = nil
	m.branchInput.SetValue("my-custom-branch")

	// Execute.
	result, _ := m.handleNewTaskBranchInputKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.spawnBranch != "my-custom-branch" {
		t.Errorf("expected 'my-custom-branch', got '%s'", rm.spawnBranch)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldDefaultToOriginMaster_GivenEnterWithEmptyInput(t *testing.T) {
	// Setup.
	m := newTestModelWithBranches(nil)
	m.branchFilteredEntries = nil
	m.branchInput.SetValue("")

	// Execute.
	result, _ := m.handleNewTaskBranchInputKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.spawnBranch != "origin/master" {
		t.Errorf("expected 'origin/master', got '%s'", rm.spawnBranch)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldGoBack_GivenEsc(t *testing.T) {
	// Setup.
	m := newTestModelWithBranches([]branchEntry{
		{tag: "(local)", name: "main"},
	})
	m.branchInput.SetValue("something")

	// Execute.
	result, _ := m.handleNewTaskBranchInputKeys(tea.KeyMsg{Type: tea.KeyEsc})

	// Assert.
	rm := result.(model)
	if rm.view != ViewNewTaskBranch {
		t.Errorf("expected ViewNewTaskBranch, got %d", rm.view)
	}
	if rm.branchInput.Value() != "" {
		t.Errorf("expected branch input cleared, got '%s'", rm.branchInput.Value())
	}
}

func TestRenderMatchedName_ShouldReturnUnchanged_GivenNoMatches(t *testing.T) {
	// Setup.
	name := "feature-auth"

	// Execute.
	result := renderMatchedName(name, nil)

	// Assert.
	if result != name {
		t.Errorf("expected unchanged name '%s', got '%s'", name, result)
	}
}

func TestRenderMatchedName_ShouldContainAllCharacters_GivenMatches(t *testing.T) {
	// Setup.
	name := "feature"
	indexes := []int{0, 1, 2}

	// Execute.
	result := renderMatchedName(name, indexes)

	// Assert.
	if !strings.Contains(result, "fea") {
		t.Error("expected result to contain matched text 'fea'")
	}
	if !strings.Contains(result, "ture") {
		t.Error("expected result to contain unmatched text 'ture'")
	}
}

func TestRenderMatchedName_ShouldReturnUnchanged_GivenEmptyIndexes(t *testing.T) {
	// Setup.
	name := "main"

	// Execute.
	result := renderMatchedName(name, []int{})

	// Assert.
	if result != name {
		t.Errorf("expected unchanged name '%s', got '%s'", name, result)
	}
}
