package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/CDFalcon/ccmux/internal/project"
	"github.com/CDFalcon/ccmux/internal/prompt"
)

func newTestModel() model {
	branchInput := textinput.New()
	branchInput.Placeholder = "origin/master"
	branchInput.Width = 50
	branchInput.CharLimit = 100

	branchFilter := textinput.New()
	branchFilter.Placeholder = "Type to search branches..."
	branchFilter.Width = 50
	branchFilter.CharLimit = 100

	taskInput := newFixedTextarea("Describe the task...", 60)

	worktreeNameInput := textinput.New()
	worktreeNameInput.Placeholder = "e.g. fix-auth-bug (optional)"
	worktreeNameInput.Width = 50
	worktreeNameInput.CharLimit = 50

	progress := new(int64)

	return model{
		view:              ViewNewTaskBranch,
		branchInput:       branchInput,
		branchFilter:      branchFilter,
		taskInput:         taskInput,
		worktreeNameInput: worktreeNameInput,
		downloadProgress:  progress,
		projSetupBuffers:  make(map[string]*projImportBuffer),
	}
}

func TestBranchEntries_ShouldIncludeDefaultAndManual_GivenNoFilter(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.branchOptions = []string{"main", "develop"}

	// Execute.
	entries := m.branchEntries()

	// Assert.
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}
	if entries[0].value != "origin/master" {
		t.Errorf("expected first entry 'origin/master', got '%s'", entries[0].value)
	}
	if !entries[1].isManual {
		t.Error("expected second entry to be manual")
	}
	if entries[2].value != "main" {
		t.Errorf("expected third entry 'main', got '%s'", entries[2].value)
	}
}

func TestBranchEntries_ShouldShowFilteredResults_GivenFilter(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.branchOptions = []string{"main", "develop"}
	m.branchFilter.SetValue("dev")
	m.filteredBranches = []string{"develop"}

	// Execute.
	entries := m.branchEntries()

	// Assert.
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (default + manual + 1 filtered), got %d", len(entries))
	}
	if entries[2].value != "develop" {
		t.Errorf("expected filtered entry 'develop', got '%s'", entries[2].value)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldDefaultToOriginMaster_GivenEmptyInput(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskBranchInput
	m.branchInput.SetValue("")

	// Execute.
	result, _ := m.handleNewTaskBranchInputKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.spawnBranch != "origin/master" {
		t.Errorf("expected 'origin/master', got '%s'", rm.spawnBranch)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldUseCustomBranch_GivenInput(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskBranchInput
	m.branchInput.SetValue("my-custom-branch")

	// Execute.
	result, _ := m.handleNewTaskBranchInputKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.spawnBranch != "my-custom-branch" {
		t.Errorf("expected 'my-custom-branch', got '%s'", rm.spawnBranch)
	}
}

func TestHandleNewTaskBranchInputKeys_ShouldGoBack_GivenEsc(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskBranchInput
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

func TestHandleNewTaskBranchKeys_ShouldNavigateDown_GivenDownKey(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.branchOptions = []string{"main", "develop"}
	m.selectedIndex = 0

	// Execute.
	result, _ := m.handleNewTaskBranchKeys(tea.KeyMsg{Type: tea.KeyDown})

	// Assert.
	rm := result.(model)
	if rm.selectedIndex != 1 {
		t.Errorf("expected selectedIndex 1, got %d", rm.selectedIndex)
	}
}

func TestHandleNewTaskBranchKeys_ShouldNavigateUp_GivenUpKey(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.branchOptions = []string{"main", "develop"}
	m.selectedIndex = 2

	// Execute.
	result, _ := m.handleNewTaskBranchKeys(tea.KeyMsg{Type: tea.KeyUp})

	// Assert.
	rm := result.(model)
	if rm.selectedIndex != 1 {
		t.Errorf("expected selectedIndex 1, got %d", rm.selectedIndex)
	}
}

func TestHandleNewTaskBranchKeys_ShouldSelectDefault_GivenEnterOnFirst(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.branchOptions = []string{"main"}
	m.selectedIndex = 0

	// Execute.
	result, _ := m.handleNewTaskBranchKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.spawnBranch != "origin/master" {
		t.Errorf("expected 'origin/master', got '%s'", rm.spawnBranch)
	}
	if rm.view != ViewNewTaskInput {
		t.Errorf("expected ViewNewTaskInput, got %d", rm.view)
	}
}

func TestHandleNewTaskBranchKeys_ShouldGoToManualInput_GivenEnterOnSecond(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.branchOptions = []string{"main"}
	m.selectedIndex = 1

	// Execute.
	result, _ := m.handleNewTaskBranchKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.view != ViewNewTaskBranchInput {
		t.Errorf("expected ViewNewTaskBranchInput, got %d", rm.view)
	}
}

func TestHandleNewTaskBranchKeys_ShouldClearFilter_GivenEscWithFilter(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.branchFilter.SetValue("something")
	m.filteredBranches = []string{"something"}

	// Execute.
	result, _ := m.handleNewTaskBranchKeys(tea.KeyMsg{Type: tea.KeyEsc})

	// Assert.
	rm := result.(model)
	if rm.branchFilter.Value() != "" {
		t.Errorf("expected filter cleared, got '%s'", rm.branchFilter.Value())
	}
	if rm.view != ViewNewTaskBranch {
		t.Errorf("expected to stay on ViewNewTaskBranch, got %d", rm.view)
	}
}

func TestHandleNewTaskBranchKeys_ShouldGoBack_GivenEscWithNoFilter(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.branchFilter.SetValue("")

	// Execute.
	result, _ := m.handleNewTaskBranchKeys(tea.KeyMsg{Type: tea.KeyEsc})

	// Assert.
	rm := result.(model)
	if rm.view != ViewSelectProject {
		t.Errorf("expected ViewSelectProject, got %d", rm.view)
	}
}

func TestHandleHelpKeys_ShouldReturnToPreviousView_GivenEsc(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewHelp
	m.previousView = ViewReview

	// Execute.
	result, _ := m.handleHelpKeys(tea.KeyMsg{Type: tea.KeyEsc})

	// Assert.
	rm := result.(model)
	if rm.view != ViewReview {
		t.Errorf("expected ViewReview, got %d", rm.view)
	}
}

func TestHelpFooter_ShouldIncludeF1Help_GivenAnyView(t *testing.T) {
	// Setup/Execute.
	mainFooter := helpFooter(ViewMain)
	inputFooter := helpFooter(ViewNewTaskInput)

	// Assert.
	if !strings.Contains(mainFooter, "[F1] help") {
		t.Errorf("expected footer to contain '[F1] help', got '%s'", mainFooter)
	}
	if !strings.Contains(inputFooter, "[F1] help") {
		t.Errorf("expected footer to contain '[F1] help', got '%s'", inputFooter)
	}
}

func TestHandleKeyPress_ShouldShowHelp_GivenF1OnInputView(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskInput

	// Execute.
	result, _ := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyF1})

	// Assert.
	rm := result.(model)
	if rm.view != ViewHelp {
		t.Errorf("expected ViewHelp, got %d", rm.view)
	}
}

func TestHandleKeyPress_ShouldNotShowHelp_GivenHOnAnyView(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewMain

	// Execute.
	result, _ := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})

	// Assert.
	rm := result.(model)
	if rm.view == ViewHelp {
		t.Error("expected 'h' key NOT to open help")
	}
}

func TestHandleKeyPress_ShouldShowHelp_GivenF1OnNonInputView(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewMain

	// Execute.
	result, _ := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyF1})

	// Assert.
	rm := result.(model)
	if rm.view != ViewHelp {
		t.Errorf("expected ViewHelp, got %d", rm.view)
	}
}

func TestHelpFooter_ShouldMatchExpectedFormat_GivenSelectProjectView(t *testing.T) {
	// Setup/Execute.
	footer := helpFooter(ViewSelectProject)

	// Assert.
	expected := "[↑/↓/j/k] select  [/] search  [enter] choose  [esc] back  [F1] help"
	if footer != expected {
		t.Errorf("expected '%s', got '%s'", expected, footer)
	}
}

func TestHandleAddProjectPathKeys_ShouldCreateProject_GivenEnterWithPathNoProjInstalled(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewAddProjectPath
	m.newProjectName = "test-proj"
	m.projectForm = newProjectForm()
	m.projectForm.pathInput.SetValue("/some/path")

	// Execute.
	result, _ := m.handleAddProjectPathKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.newProjectPath != "/some/path" {
		t.Errorf("expected path '/some/path', got '%s'", rm.newProjectPath)
	}
}

func TestHandleAddProjectFastWTKeys_ShouldGoBack_GivenEsc(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewAddProjectFastWT
	m.projectForm = newProjectForm()

	// Execute.
	result, _ := m.handleAddProjectFastWTKeys(tea.KeyMsg{Type: tea.KeyEsc})

	// Assert.
	rm := result.(model)
	if rm.view != ViewAddProjectPath {
		t.Errorf("expected ViewAddProjectPath, got %d", rm.view)
	}
}

func TestHandleAddProjectFastWTKeys_ShouldGoToManageProjects_GivenYes(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewAddProjectFastWT
	m.newProjectName = "test"
	m.newProjectPath = "/some/path"

	// Execute.
	result, cmd := m.handleAddProjectFastWTKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	// Assert.
	rm := result.(model)
	if rm.view != ViewManageProjects {
		t.Errorf("expected ViewManageProjects, got %d", rm.view)
	}
	if _, ok := rm.projSetupBuffers["test"]; !ok {
		t.Error("expected projSetupBuffers to contain buffer for 'test'")
	}
	if cmd == nil {
		t.Error("expected a command to be returned")
	}
}

func TestHandleAddProjectFastWTKeys_ShouldGoToManageProjects_GivenNo(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewAddProjectFastWT
	m.newProjectName = "test"
	m.newProjectPath = "/some/path"

	// Execute.
	result, _ := m.handleAddProjectFastWTKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	// Assert.
	rm := result.(model)
	if rm.view != ViewManageProjects {
		t.Errorf("expected ViewManageProjects, got %d", rm.view)
	}
}

func TestHelpFooter_ShouldIncludeYesNo_GivenFastWTView(t *testing.T) {
	// Setup/Execute.
	footer := helpFooter(ViewAddProjectFastWT)

	// Assert.
	if !strings.Contains(footer, "[y]es") {
		t.Errorf("expected footer to contain '[y]es', got '%s'", footer)
	}
	if !strings.Contains(footer, "[n]o") {
		t.Errorf("expected footer to contain '[n]o', got '%s'", footer)
	}
}

func TestHandleProjImportingKeys_ShouldGoBack_GivenEsc(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewProjImporting
	m.projSetupName = "test"
	m.projSetupBuffers["test"] = &projImportBuffer{}

	// Execute.
	result, _ := m.handleProjImportingKeys(tea.KeyMsg{Type: tea.KeyEsc})

	// Assert.
	rm := result.(model)
	if rm.view != ViewManageProjects {
		t.Errorf("expected ViewManageProjects, got %d", rm.view)
	}
	if rm.projSetupName != "" {
		t.Errorf("expected projSetupName cleared, got '%s'", rm.projSetupName)
	}
}

func TestHandleProjImportingKeys_ShouldIgnore_GivenOtherKeys(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewProjImporting
	m.projSetupName = "test"

	// Execute.
	result, _ := m.handleProjImportingKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})

	// Assert.
	rm := result.(model)
	if rm.view != ViewProjImporting {
		t.Errorf("expected ViewProjImporting, got %d", rm.view)
	}
	if rm.projSetupName != "test" {
		t.Errorf("expected projSetupName to remain 'test', got '%s'", rm.projSetupName)
	}
}

func TestProjImportBuffer_ShouldReturnLastN_GivenMoreLines(t *testing.T) {
	// Setup.
	buf := &projImportBuffer{}
	for i := 0; i < 10; i++ {
		buf.addLine(fmt.Sprintf("line %d", i))
	}

	// Execute.
	lines := buf.lastN(5)

	// Assert.
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}
	if lines[0] != "line 5" {
		t.Errorf("expected 'line 5', got '%s'", lines[0])
	}
	if lines[4] != "line 9" {
		t.Errorf("expected 'line 9', got '%s'", lines[4])
	}
}

func TestProjImportBuffer_ShouldReturnAll_GivenFewerLines(t *testing.T) {
	// Setup.
	buf := &projImportBuffer{}
	buf.addLine("only line")

	// Execute.
	lines := buf.lastN(5)

	// Assert.
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0] != "only line" {
		t.Errorf("expected 'only line', got '%s'", lines[0])
	}
}

func TestProjImportBuffer_ShouldReturnEmpty_GivenReset(t *testing.T) {
	// Setup.
	buf := &projImportBuffer{}
	buf.addLine("something")
	buf.reset()

	// Execute.
	lines := buf.lastN(5)

	// Assert.
	if len(lines) != 0 {
		t.Errorf("expected 0 lines after reset, got %d", len(lines))
	}
}

func TestHelpFooter_ShouldIncludeEscBack_GivenProjImportingView(t *testing.T) {
	// Setup/Execute.
	footer := helpFooter(ViewProjImporting)

	// Assert.
	if !strings.Contains(footer, "[esc] back") {
		t.Errorf("expected footer to contain '[esc] back', got '%s'", footer)
	}
}

func TestHandleSelectProjectKeys_ShouldRejectSettingUpProject_GivenEnter(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewSelectProject
	m.projects = []*project.Project{
		{Name: "test", Path: "/test", SetupStatus: project.SetupStatusSettingUp},
	}
	m.selectedIndex = 0

	// Execute.
	result, _ := m.handleSelectProjectKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.err == nil {
		t.Error("expected error when selecting a setting-up project")
	}
	if rm.view != ViewSelectProject {
		t.Errorf("expected to stay on ViewSelectProject, got %d", rm.view)
	}
}

func TestHandleManageProjectsKeys_ShouldShowImportLog_GivenEnterOnSettingUpProject(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewManageProjects
	m.projects = []*project.Project{
		{Name: "test", Path: "/test", SetupStatus: project.SetupStatusSettingUp},
	}
	m.selectedIndex = 0
	m.projSetupBuffers["test"] = &projImportBuffer{}

	// Execute.
	result, _ := m.handleManageProjectsKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.view != ViewProjImporting {
		t.Errorf("expected ViewProjImporting, got %d", rm.view)
	}
	if rm.projSetupName != "test" {
		t.Errorf("expected projSetupName 'test', got '%s'", rm.projSetupName)
	}
}

func TestHandleManageProjectsKeys_ShouldEditProject_GivenEnterOnReadyProject(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewManageProjects
	m.projects = []*project.Project{
		{Name: "test", Path: "/test"},
	}
	m.selectedIndex = 0
	m.editProjectForm = newEditProjectForm()

	// Execute.
	result, _ := m.handleManageProjectsKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.view != ViewEditProject {
		t.Errorf("expected ViewEditProject, got %d", rm.view)
	}
}

func TestParsePRURL_ShouldReturnOwnerRepoNumber_GivenValidURL(t *testing.T) {
	// Setup.
	url := "https://github.com/myorg/myrepo/pull/42"

	// Execute.
	owner, repo, prNumber, err := parsePRURL(url)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "myorg" {
		t.Errorf("expected owner 'myorg', got '%s'", owner)
	}
	if repo != "myrepo" {
		t.Errorf("expected repo 'myrepo', got '%s'", repo)
	}
	if prNumber != "42" {
		t.Errorf("expected prNumber '42', got '%s'", prNumber)
	}
}

func TestParsePRURL_ShouldReturnError_GivenInvalidURL(t *testing.T) {
	// Setup.
	url := "not-a-url"

	// Execute.
	_, _, _, err := parsePRURL(url)

	// Assert.
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestEvaluateCIChecks_ShouldReturnPassed_GivenAllSuccess(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS"},
	}

	// Execute.
	status, failed, _, _ := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusPassed {
		t.Errorf("expected ciStatusPassed, got %d", status)
	}
	if len(failed) != 0 {
		t.Errorf("expected no failures, got %v", failed)
	}
}

func TestEvaluateCIChecks_ShouldReturnPassed_GivenSkippedAndNeutral(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "optional", Status: "COMPLETED", Conclusion: "SKIPPED"},
		{Name: "info", Status: "COMPLETED", Conclusion: "NEUTRAL"},
	}

	// Execute.
	status, failed, _, _ := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusPassed {
		t.Errorf("expected ciStatusPassed, got %d", status)
	}
	if len(failed) != 0 {
		t.Errorf("expected no failures, got %v", failed)
	}
}

func TestEvaluateCIChecks_ShouldReturnFailed_GivenAnyFailure(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "lint", Status: "COMPLETED", Conclusion: "FAILURE"},
		{Name: "test", Status: "COMPLETED", Conclusion: "ERROR"},
	}

	// Execute.
	status, failed, _, _ := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusFailed {
		t.Errorf("expected ciStatusFailed, got %d", status)
	}
	if len(failed) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(failed))
	}
	if failed[0] != "lint" || failed[1] != "test" {
		t.Errorf("expected ['lint', 'test'], got %v", failed)
	}
}

func TestEvaluateCIChecks_ShouldReturnPending_GivenAnyPendingAndNoFailures(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "test", Status: "IN_PROGRESS", Conclusion: ""},
	}

	// Execute.
	status, failed, _, _ := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusPending {
		t.Errorf("expected ciStatusPending, got %d", status)
	}
	if len(failed) != 0 {
		t.Errorf("expected no failures, got %v", failed)
	}
}

func TestEvaluateCIChecks_ShouldReturnPending_GivenNoChecks(t *testing.T) {
	// Setup.
	checks := []prCheckResult{}

	// Execute.
	status, _, _, _ := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusPending {
		t.Errorf("expected ciStatusPending, got %d", status)
	}
}

func TestEvaluateCIChecks_ShouldReturnFailed_GivenFailureAndPending(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{Name: "build", Status: "COMPLETED", Conclusion: "FAILURE"},
		{Name: "test", Status: "QUEUED", Conclusion: ""},
	}

	// Execute.
	status, failed, _, _ := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusFailed {
		t.Errorf("expected ciStatusFailed (failure takes precedence over pending), got %d", status)
	}
	if len(failed) != 1 || failed[0] != "build" {
		t.Errorf("expected ['build'], got %v", failed)
	}
}

func TestEvaluateCIChecks_ShouldReturnCorrectProgress_GivenMixedStatuses(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "test", Status: "IN_PROGRESS", Conclusion: ""},
		{Name: "deploy", Status: "QUEUED", Conclusion: ""},
		{Name: "e2e", Status: "COMPLETED", Conclusion: "SUCCESS"},
	}

	// Execute.
	_, _, completed, total := evaluateCIChecks(checks)

	// Assert.
	if completed != 3 {
		t.Errorf("expected 3 completed, got %d", completed)
	}
	if total != 5 {
		t.Errorf("expected 5 total, got %d", total)
	}
}

func TestEvaluateCIChecks_ShouldReturnPassed_GivenStaleFailureAndNewerSuccess(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{Name: "check-pr-title", Status: "COMPLETED", Conclusion: "FAILURE", StartedAt: "2026-03-11T17:00:00Z"},
		{Name: "check-pr-title", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: "2026-03-11T17:05:00Z"},
		{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: "2026-03-11T17:00:00Z"},
	}

	// Execute.
	status, failed, completed, total := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusPassed {
		t.Errorf("expected ciStatusPassed, got %d", status)
	}
	if len(failed) != 0 {
		t.Errorf("expected no failures, got %v", failed)
	}
	if completed != 2 {
		t.Errorf("expected 2 completed, got %d", completed)
	}
	if total != 2 {
		t.Errorf("expected 2 total (deduplicated), got %d", total)
	}
}

func TestEvaluateCIChecks_ShouldReturnFailed_GivenNewerFailureAfterOldSuccess(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: "2026-03-11T17:00:00Z"},
		{Name: "lint", Status: "COMPLETED", Conclusion: "FAILURE", StartedAt: "2026-03-11T17:10:00Z"},
	}

	// Execute.
	status, failed, _, _ := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusFailed {
		t.Errorf("expected ciStatusFailed, got %d", status)
	}
	if len(failed) != 1 || failed[0] != "lint" {
		t.Errorf("expected ['lint'], got %v", failed)
	}
}

func TestEvaluateCIChecks_ShouldReturnPending_GivenStaleFailureAndNewerRerun(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{Name: "test", Status: "COMPLETED", Conclusion: "FAILURE", StartedAt: "2026-03-11T17:00:00Z"},
		{Name: "test", Status: "IN_PROGRESS", Conclusion: "", StartedAt: "2026-03-11T17:05:00Z"},
	}

	// Execute.
	status, failed, _, _ := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusPending {
		t.Errorf("expected ciStatusPending (re-run in progress should override stale failure), got %d", status)
	}
	if len(failed) != 0 {
		t.Errorf("expected no failures, got %v", failed)
	}
}

func TestDeduplicateChecks_ShouldKeepLatestByStartedAt(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{Name: "a", Status: "COMPLETED", Conclusion: "FAILURE", StartedAt: "2026-03-11T17:00:00Z"},
		{Name: "a", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: "2026-03-11T17:05:00Z"},
		{Name: "a", Status: "COMPLETED", Conclusion: "FAILURE", StartedAt: "2026-03-11T17:01:00Z"},
		{Name: "b", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: "2026-03-11T17:00:00Z"},
	}

	// Execute.
	result := deduplicateChecks(checks)

	// Assert.
	if len(result) != 2 {
		t.Fatalf("expected 2 deduplicated checks, got %d", len(result))
	}
	resultMap := make(map[string]prCheckResult)
	for _, c := range result {
		resultMap[c.Name] = c
	}
	if resultMap["a"].Conclusion != "SUCCESS" {
		t.Errorf("expected 'a' to keep SUCCESS (latest), got %s", resultMap["a"].Conclusion)
	}
	if resultMap["b"].Conclusion != "SUCCESS" {
		t.Errorf("expected 'b' to remain SUCCESS, got %s", resultMap["b"].Conclusion)
	}
}

func TestNormalizeChecks_ShouldConvertStatusContextToCheckRunFormat(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{TypeName: "StatusContext", Context: "ci/buildkite", State: "SUCCESS"},
		{TypeName: "StatusContext", Context: "ci/lint", State: "PENDING"},
		{TypeName: "CheckRun", Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
	}

	// Execute.
	result := normalizeChecks(checks)

	// Assert.
	if result[0].Name != "ci/buildkite" {
		t.Errorf("expected Name to be set from Context, got %q", result[0].Name)
	}
	if result[0].Status != "COMPLETED" {
		t.Errorf("expected COMPLETED for success state, got %q", result[0].Status)
	}
	if result[0].Conclusion != "SUCCESS" {
		t.Errorf("expected SUCCESS conclusion, got %q", result[0].Conclusion)
	}
	if result[1].Name != "ci/lint" {
		t.Errorf("expected Name to be set from Context, got %q", result[1].Name)
	}
	if result[1].Status != "IN_PROGRESS" {
		t.Errorf("expected IN_PROGRESS for pending state, got %q", result[1].Status)
	}
	if result[2].Name != "build" {
		t.Errorf("expected CheckRun to be unchanged, got %q", result[2].Name)
	}
	if result[2].Status != "COMPLETED" {
		t.Errorf("expected CheckRun status unchanged, got %q", result[2].Status)
	}
}

func TestEvaluateCIChecks_ShouldReturnPassed_GivenMixedCheckRunAndStatusContext(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{TypeName: "CheckRun", Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{TypeName: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{TypeName: "StatusContext", Context: "ci/buildkite/deploy", State: "SUCCESS"},
		{TypeName: "StatusContext", Context: "ci/buildkite/lint", State: "SUCCESS"},
	}

	// Execute.
	status, failed, completed, total := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusPassed {
		t.Errorf("expected ciStatusPassed, got %d", status)
	}
	if len(failed) != 0 {
		t.Errorf("expected no failures, got %v", failed)
	}
	if completed != 4 {
		t.Errorf("expected 4 completed, got %d", completed)
	}
	if total != 4 {
		t.Errorf("expected 4 total, got %d", total)
	}
}

func TestEvaluateCIChecks_ShouldReturnFailed_GivenStatusContextFailure(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{TypeName: "CheckRun", Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{TypeName: "StatusContext", Context: "ci/deploy", State: "FAILURE"},
	}

	// Execute.
	status, failed, _, _ := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusFailed {
		t.Errorf("expected ciStatusFailed, got %d", status)
	}
	if len(failed) != 1 || failed[0] != "ci/deploy" {
		t.Errorf("expected ['ci/deploy'], got %v", failed)
	}
}

func TestEvaluateCIChecks_ShouldReturnPending_GivenStatusContextPending(t *testing.T) {
	// Setup.
	checks := []prCheckResult{
		{TypeName: "CheckRun", Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{TypeName: "StatusContext", Context: "ci/deploy", State: "PENDING"},
	}

	// Execute.
	status, failed, completed, total := evaluateCIChecks(checks)

	// Assert.
	if status != ciStatusPending {
		t.Errorf("expected ciStatusPending, got %d", status)
	}
	if len(failed) != 0 {
		t.Errorf("expected no failures, got %v", failed)
	}
	if completed != 1 {
		t.Errorf("expected 1 completed, got %d", completed)
	}
	if total != 2 {
		t.Errorf("expected 2 total, got %d", total)
	}
}

func TestHandleNewTaskInputKeys_ShouldSkipPromptSelection_GivenNoPrompts(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskInput
	m.taskInput.SetValue("do something")
	m.prompts = nil

	// Execute.
	result, _ := m.handleNewTaskInputKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.view != ViewNewTaskWorktreeName {
		t.Errorf("expected ViewNewTaskWorktreeName, got %d", rm.view)
	}
}

func TestHandleNewTaskInputKeys_ShouldSkipPromptSelection_GivenNoMatchingPrompts(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskInput
	m.taskInput.SetValue("do something")
	m.selectedProj = &project.Project{Name: "my-proj"}
	m.prompts = []*prompt.Prompt{
		{ID: "1", Name: "other", ProjectNames: []string{"other-proj"}},
	}

	// Execute.
	result, _ := m.handleNewTaskInputKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.view != ViewNewTaskWorktreeName {
		t.Errorf("expected ViewNewTaskWorktreeName, got %d", rm.view)
	}
}

func TestHandleNewTaskInputKeys_ShouldShowPromptSelection_GivenMatchingPrompts(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskInput
	m.taskInput.SetValue("do something")
	m.prompts = []*prompt.Prompt{
		{ID: "1", Name: "global prompt"},
	}

	// Execute.
	result, _ := m.handleNewTaskInputKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.view != ViewNewTaskSelectPrompts {
		t.Errorf("expected ViewNewTaskSelectPrompts, got %d", rm.view)
	}
}

func TestHandleNewTaskWorktreeNameKeys_ShouldGoBackToTaskInput_GivenEscWithNoPrompts(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskWorktreeName
	m.spawnFilteredPrompts = nil

	// Execute.
	result, _ := m.handleNewTaskWorktreeNameKeys(tea.KeyMsg{Type: tea.KeyEsc})

	// Assert.
	rm := result.(model)
	if rm.view != ViewNewTaskInput {
		t.Errorf("expected ViewNewTaskInput, got %d", rm.view)
	}
}

func TestHandleNewTaskWorktreeNameKeys_ShouldGoBackToPromptSelection_GivenEscWithPrompts(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskWorktreeName
	m.spawnFilteredPrompts = []*prompt.Prompt{
		{ID: "1", Name: "test"},
	}

	// Execute.
	result, _ := m.handleNewTaskWorktreeNameKeys(tea.KeyMsg{Type: tea.KeyEsc})

	// Assert.
	rm := result.(model)
	if rm.view != ViewNewTaskSelectPrompts {
		t.Errorf("expected ViewNewTaskSelectPrompts, got %d", rm.view)
	}
}
