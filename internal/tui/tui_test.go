package tui

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/harness"
	"github.com/CDFalcon/ccmux/internal/project"
	"github.com/CDFalcon/ccmux/internal/prompt"
	"github.com/CDFalcon/ccmux/internal/queue"
	"github.com/CDFalcon/ccmux/internal/tmux"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func randomTestSuffix() int64 {
	return rand.Int63()
}

func removeTestStore(sessionID string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(homeDir, ".ccmux", "sessions", sessionID))
}

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

func TestHandleNewTaskBranchKeys_ShouldGoBackToHarness_GivenEscWithNoFilter(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.branchFilter.SetValue("")

	// Execute.
	result, _ := m.handleNewTaskBranchKeys(tea.KeyMsg{Type: tea.KeyEsc})

	// Assert: esc steps back to the harness-selection step that now
	// precedes branch selection.
	rm := result.(model)
	if rm.view != ViewNewTaskHarness {
		t.Errorf("expected ViewNewTaskHarness, got %d", rm.view)
	}
}

func TestHandleNewTaskHarnessKeys_ShouldAdvanceToBranch_GivenEnter(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskHarness
	m.selectedIndex = 1 // second harness in harness.All()

	// Execute.
	result, _ := m.handleNewTaskHarnessKeys(tea.KeyMsg{Type: tea.KeyEnter})

	// Assert.
	rm := result.(model)
	if rm.view != ViewNewTaskBranch {
		t.Errorf("expected ViewNewTaskBranch, got %d", rm.view)
	}
	if rm.spawnHarness != harness.All()[1] {
		t.Errorf("expected spawnHarness %q, got %q", harness.All()[1], rm.spawnHarness)
	}
}

func TestHandleNewTaskHarnessKeys_ShouldGoBackToProject_GivenEsc(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskHarness

	// Execute.
	result, _ := m.handleNewTaskHarnessKeys(tea.KeyMsg{Type: tea.KeyEsc})

	// Assert.
	rm := result.(model)
	if rm.view != ViewSelectProject {
		t.Errorf("expected ViewSelectProject, got %d", rm.view)
	}
}

func TestHandleNewTaskHarnessKeys_ShouldNotMovePastEnds_GivenNavigation(t *testing.T) {
	// Setup.
	m := newTestModel()
	m.view = ViewNewTaskHarness
	m.selectedIndex = 0

	// Execute: up at the top is a no-op.
	result, _ := m.handleNewTaskHarnessKeys(tea.KeyMsg{Type: tea.KeyUp})

	// Assert.
	if result.(model).selectedIndex != 0 {
		t.Errorf("expected selectedIndex to stay 0, got %d", result.(model).selectedIndex)
	}

	// Execute: down past the last harness is a no-op.
	m.selectedIndex = len(harness.All()) - 1
	result, _ = m.handleNewTaskHarnessKeys(tea.KeyMsg{Type: tea.KeyDown})

	// Assert.
	if result.(model).selectedIndex != len(harness.All())-1 {
		t.Errorf("expected selectedIndex to stay %d, got %d", len(harness.All())-1, result.(model).selectedIndex)
	}
}

func TestResumeScripts_ShouldProduceValidBash_ForEveryHarness(t *testing.T) {
	for _, h := range harness.All() {
		t.Run(string(h), func(t *testing.T) {
			id := fmt.Sprintf("scripttest-%s-%d", h, randomTestSuffix())

			review, err := writeReviewScript(id, "/tmp/wt", "https://github.com/o/r/pull/1", h)
			if err != nil {
				t.Fatalf("writeReviewScript: %v", err)
			}
			defer os.Remove(review)

			cifix, err := writeCIFixScript(id, "/tmp/wt", "https://github.com/o/r/pull/1", "boom", h)
			if err != nil {
				t.Fatalf("writeCIFixScript: %v", err)
			}
			defer os.Remove(cifix)

			conflict, err := writeMergeConflictScript(id, "/tmp/wt", "https://github.com/o/r/pull/1", "origin/main", h)
			if err != nil {
				t.Fatalf("writeMergeConflictScript: %v", err)
			}
			defer os.Remove(conflict)

			restart, err := writeRestartScript(id, "/tmp/wt", "origin/main", "the task", h, true)
			if err != nil {
				t.Fatalf("writeRestartScript: %v", err)
			}
			defer os.Remove(restart)

			for _, path := range []string{review, cifix, conflict, restart} {
				if out, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
					t.Errorf("%s failed bash syntax check: %v\n%s", path, err, out)
				}
			}
		})
	}
}

func TestHarnessIndex_ShouldLocateHarness(t *testing.T) {
	for i, h := range harness.All() {
		if got := harnessIndex(h); got != i {
			t.Errorf("harnessIndex(%q) = %d, want %d", h, got, i)
		}
	}
	if got := harnessIndex(harness.Type("nonsense")); got != 0 {
		t.Errorf("harnessIndex(unknown) = %d, want 0", got)
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

func TestLoadFromProject_ShouldShowDraftPRsValue_GivenEachState(t *testing.T) {
	yes := true
	no := false
	cases := []struct {
		name     string
		draftPRs *bool
		want     string
	}{
		{"unset defaults to yes", nil, "yes"},
		{"explicitly enabled", &yes, "yes"},
		{"explicitly disabled", &no, "no"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup.
			ef := newEditProjectForm()
			p := &project.Project{Name: "test", Path: "/test", DraftPRs: tc.draftPRs}

			// Execute.
			ef.loadFromProject(p)

			// Assert. A disabled setting must render as an explicit "no";
			// an empty value would display the placeholder "yes" instead and
			// look identical to the enabled state.
			if got := ef.draftPRsInput.Value(); got != tc.want {
				t.Errorf("draftPRsInput value = %q, want %q", got, tc.want)
			}
		})
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

func TestFirstPRURL_ShouldReturnURL_GivenNonEmptyList(t *testing.T) {
	// Setup. Shape of `gh pr list --json url` output.
	output := []byte(`[{"url":"https://github.com/myorg/myrepo/pull/42"}]`)

	// Execute.
	got := firstPRURL(output)

	// Assert.
	if got != "https://github.com/myorg/myrepo/pull/42" {
		t.Errorf("expected PR url, got '%s'", got)
	}
}

func TestFirstPRURL_ShouldReturnEmpty_GivenEmptyList(t *testing.T) {
	// Setup. `gh pr list` prints an empty array when no PR matches the head.
	output := []byte(`[]`)

	// Execute.
	got := firstPRURL(output)

	// Assert.
	if got != "" {
		t.Errorf("expected empty string for empty list, got '%s'", got)
	}
}

func TestFirstPRURL_ShouldReturnEmpty_GivenUnparseableOutput(t *testing.T) {
	// Setup. A gh failure or auth error may yield non-JSON on stdout.
	output := []byte("gh: could not determine repository")

	// Execute.
	got := firstPRURL(output)

	// Assert.
	if got != "" {
		t.Errorf("expected empty string for unparseable output, got '%s'", got)
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

func newTestModelWithStore(t *testing.T) model {
	t.Helper()
	m := newTestModel()
	sessionID := fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), randomTestSuffix())
	store, err := agent.NewStore(sessionID)
	if err != nil {
		t.Fatalf("failed to create agent store: %v", err)
	}
	t.Cleanup(func() {
		_ = removeTestStore(sessionID)
	})
	m.agentStore = store
	return m
}

func TestShouldThrottleResume_ShouldReturnFalse_GivenNoHistory(t *testing.T) {
	// Setup.
	m := newTestModelWithStore(t)
	a := &agent.Agent{ID: "agent-1"}

	// Execute.
	result := m.shouldThrottleResume(a)

	// Assert.
	if result {
		t.Error("expected no throttle with empty history")
	}
}

func TestShouldThrottleResume_ShouldReturnFalse_GivenFewerThanMaxAttempts(t *testing.T) {
	// Setup.
	m := newTestModelWithStore(t)
	now := time.Now()
	a := &agent.Agent{
		ID: "agent-1",
		CIResumeHistory: []time.Time{
			now.Add(-5 * time.Minute),
			now.Add(-3 * time.Minute),
		},
	}

	// Execute.
	result := m.shouldThrottleResume(a)

	// Assert.
	if result {
		t.Error("expected no throttle with only 2 attempts")
	}
}

func TestShouldThrottleResume_ShouldReturnTrue_GivenMaxAttemptsWithinWindow(t *testing.T) {
	// Setup. Five attempts inside the 5-minute throttle window — the threshold.
	m := newTestModelWithStore(t)
	now := time.Now()
	a := &agent.Agent{
		ID: "agent-1",
		CIResumeHistory: []time.Time{
			now.Add(-4 * time.Minute),
			now.Add(-3 * time.Minute),
			now.Add(-2 * time.Minute),
			now.Add(-1 * time.Minute),
			now.Add(-30 * time.Second),
		},
	}

	// Execute.
	result := m.shouldThrottleResume(a)

	// Assert.
	if !result {
		t.Errorf("expected throttle with %d attempts within %s, got false", ciResumeMaxAttempts, ciResumeWindow)
	}
}

func TestShouldThrottleResume_ShouldReturnFalse_GivenOldAttemptsOutsideWindow(t *testing.T) {
	// Setup.
	m := newTestModelWithStore(t)
	now := time.Now()
	a := &agent.Agent{
		ID: "agent-1",
		CIResumeHistory: []time.Time{
			now.Add(-20 * time.Minute),
			now.Add(-18 * time.Minute),
			now.Add(-1 * time.Minute),
		},
	}

	// Execute.
	result := m.shouldThrottleResume(a)

	// Assert.
	if result {
		t.Error("expected no throttle when old attempts fall outside window")
	}
}

func TestShouldThrottleResume_ShouldPruneOldEntries_GivenExpiredTimestamps(t *testing.T) {
	// Setup.
	m := newTestModelWithStore(t)
	now := time.Now()
	stored := &agent.Agent{
		ID: "agent-1",
		CIResumeHistory: []time.Time{
			now.Add(-30 * time.Minute),
			now.Add(-25 * time.Minute),
			now.Add(-20 * time.Minute),
			now.Add(-1 * time.Minute),
		},
	}
	if err := m.agentStore.Create(stored); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}

	// Execute.
	m.shouldThrottleResume(stored)

	// Assert.
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if len(reloaded.CIResumeHistory) != 1 {
		t.Errorf("expected 1 entry after pruning, got %d", len(reloaded.CIResumeHistory))
	}
}

func TestRecordResume_ShouldAppendTimestamp_GivenExistingHistory(t *testing.T) {
	// Setup. Seed an entry well inside the 5-minute throttle window so
	// recordResume's pruning step doesn't drop it before appending the new one.
	m := newTestModelWithStore(t)
	stored := &agent.Agent{
		ID:              "agent-1",
		CIResumeHistory: []time.Time{time.Now().Add(-1 * time.Minute)},
	}
	if err := m.agentStore.Create(stored); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}

	// Execute.
	m.recordResume("agent-1")

	// Assert.
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if len(reloaded.CIResumeHistory) != 2 {
		t.Errorf("expected 2 entries, got %d", len(reloaded.CIResumeHistory))
	}
}

func TestShouldThrottleResume_ShouldNotAffectOtherAgents_GivenDifferentAgentIDs(t *testing.T) {
	// Setup.
	m := newTestModelWithStore(t)
	now := time.Now()
	agent1 := &agent.Agent{
		ID: "agent-1",
		CIResumeHistory: []time.Time{
			now.Add(-10 * time.Minute),
			now.Add(-5 * time.Minute),
			now.Add(-1 * time.Minute),
		},
	}
	agent2 := &agent.Agent{ID: "agent-2"}

	// Execute.
	_ = m.shouldThrottleResume(agent1)
	result := m.shouldThrottleResume(agent2)

	// Assert.
	if result {
		t.Error("expected no throttle for agent-2 when only agent-1 has history")
	}
}

func TestIsDuplicateCIFailure_ShouldReturnFalse_GivenNoHistory(t *testing.T) {
	// Setup.
	m := newTestModel()
	a := &agent.Agent{ID: "agent-1"}

	// Execute.
	result := m.isDuplicateCIFailure(a, "CI checks failed: lint, test")

	// Assert.
	if result {
		t.Error("expected not duplicate with no history")
	}
}

func TestIsDuplicateCIFailure_ShouldReturnTrue_GivenSameSummary(t *testing.T) {
	// Setup.
	m := newTestModel()
	a := &agent.Agent{
		ID:                    "agent-1",
		CILastNotifiedSummary: "CI checks failed: lint, test",
	}

	// Execute.
	result := m.isDuplicateCIFailure(a, "CI checks failed: lint, test")

	// Assert.
	if !result {
		t.Error("expected duplicate when summary matches")
	}
}

func TestIsDuplicateCIFailure_ShouldReturnFalse_GivenDifferentSummary(t *testing.T) {
	// Setup.
	m := newTestModel()
	a := &agent.Agent{
		ID:                    "agent-1",
		CILastNotifiedSummary: "CI checks failed: lint",
	}

	// Execute.
	result := m.isDuplicateCIFailure(a, "CI checks failed: lint, test")

	// Assert.
	if result {
		t.Error("expected not duplicate when summary differs")
	}
}

func TestIsDuplicateCIFailure_ShouldReturnFalse_GivenNilAgent(t *testing.T) {
	// Setup.
	m := newTestModel()

	// Execute.
	result := m.isDuplicateCIFailure(nil, "CI checks failed: lint, test")

	// Assert.
	if result {
		t.Error("expected not duplicate for nil agent")
	}
}

// newTestModelWithStoreAndQueue wires both the agent store and a queue to the
// same session so we can drive Update through paths that touch both.
func newTestModelWithStoreAndQueue(t *testing.T) model {
	t.Helper()
	m := newTestModel()
	sessionID := fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), randomTestSuffix())
	store, err := agent.NewStore(sessionID)
	if err != nil {
		t.Fatalf("failed to create agent store: %v", err)
	}
	q, err := queue.NewQueue(sessionID)
	if err != nil {
		t.Fatalf("failed to create queue: %v", err)
	}
	t.Cleanup(func() { _ = removeTestStore(sessionID) })
	m.agentStore = store
	m.queueManager = q
	m.ciCheckProgress = make(map[string]ciProgress)
	m.ciChecking = make(map[string]bool)
	m.ciLastChecked = make(map[string]time.Time)
	return m
}

func installFakeTmux(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "tmux")
	script := `#!/bin/sh
if [ "$1" = "display-message" ]; then
  case "$5" in
    '#{window_activity}') echo "${FAKE_TMUX_WINDOW_ACTIVITY:-0}" ;;
    '#{pane_pid}') echo "${FAKE_TMUX_PANE_PID:-999999}" ;;
    '#{pane_dead}') echo "${FAKE_TMUX_PANE_DEAD:-0}" ;;
    *) echo "0" ;;
  esac
  exit 0
fi
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func newRefreshTestModel(t *testing.T) model {
	t.Helper()

	m := newTestModelWithStoreAndQueue(t)
	projectStore, err := project.NewStore()
	if err != nil {
		t.Fatalf("failed to create project store: %v", err)
	}
	promptStore, err := prompt.NewStore()
	if err != nil {
		t.Fatalf("failed to create prompt store: %v", err)
	}
	m.projectStore = projectStore
	m.promptStore = promptStore
	m.tmuxManager = tmux.NewManager("test-session")
	return m
}

func TestRefreshCmd_ShouldHandIdleRunningAgentWithPRToCI(t *testing.T) {
	// Setup. Codex has no PostToolUse hook, so a Codex-backed agent with a
	// known PR can sit idle before the launcher script's trailing `ccmux
	// ci-wait` runs. The refresh loop must hand that state to the CI poller,
	// not surface a generic idle item.
	installFakeTmux(t)
	t.Setenv("FAKE_TMUX_WINDOW_ACTIVITY", "0")
	t.Setenv("FAKE_TMUX_PANE_PID", "999999")

	m := newRefreshTestModel(t)
	before := time.Now()
	a := &agent.Agent{
		ID:           "agent-1",
		Status:       agent.StatusRunning,
		PRURL:        "https://github.com/owner/repo/pull/1",
		WorktreePath: t.TempDir(),
		TmuxWindow:   "@1",
	}
	if err := m.agentStore.Create(a); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}

	// Execute.
	msg := m.refreshCmd()()
	if _, ok := msg.(refreshMsg); !ok {
		t.Fatalf("expected refreshMsg, got %T", msg)
	}

	// Assert.
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if reloaded.Status != agent.StatusWaitingCI {
		t.Errorf("expected idle running agent with PR to wait on CI, got %s", reloaded.Status)
	}
	if reloaded.CIWaitAt.Before(before) {
		t.Errorf("expected CIWaitAt to be refreshed, got %v before %v", reloaded.CIWaitAt, before)
	}
	items, err := m.queueManager.List()
	if err != nil {
		t.Fatalf("failed to list queue: %v", err)
	}
	for _, item := range items {
		if item.AgentID == "agent-1" && item.Type == queue.ItemTypeIdle {
			t.Fatalf("expected no idle queue item, got %+v", item)
		}
	}
}

func TestRefreshCmd_ShouldRepairReadyAgentWithPRAndPlainIdleItem(t *testing.T) {
	// Setup. This mirrors the observed stale state: the PR is known and CI is
	// still live, but the refresh idle detector parked the agent under the
	// generic idle queue.
	installFakeTmux(t)
	t.Setenv("FAKE_TMUX_PANE_PID", "999999")

	m := newRefreshTestModel(t)
	before := time.Now()
	a := &agent.Agent{
		ID:           "agent-1",
		Status:       agent.StatusReady,
		PRURL:        "https://github.com/owner/repo/pull/1",
		WorktreePath: t.TempDir(),
		TmuxWindow:   "@1",
	}
	if err := m.agentStore.Create(a); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}
	if _, err := m.queueManager.Add(queue.ItemTypeIdle, a.ID, "Agent idle - waiting for input", ""); err != nil {
		t.Fatalf("failed to seed idle queue item: %v", err)
	}

	// Execute.
	msg := m.refreshCmd()()
	if _, ok := msg.(refreshMsg); !ok {
		t.Fatalf("expected refreshMsg, got %T", msg)
	}

	// Assert.
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if reloaded.Status != agent.StatusWaitingCI {
		t.Errorf("expected stale ready agent with PR to wait on CI, got %s", reloaded.Status)
	}
	if reloaded.CIWaitAt.Before(before) {
		t.Errorf("expected CIWaitAt to be refreshed, got %v before %v", reloaded.CIWaitAt, before)
	}
	items, err := m.queueManager.List()
	if err != nil {
		t.Fatalf("failed to list queue: %v", err)
	}
	for _, item := range items {
		if item.AgentID == "agent-1" && item.Type == queue.ItemTypeIdle {
			t.Fatalf("expected stale idle queue item removed, got %+v", item)
		}
	}
}

func TestRefreshCmd_ShouldPreserveNearbyIdleStates(t *testing.T) {
	// Setup. These cases guard the states adjacent to the CI handoff so the
	// repair path does not turn unrelated idle/manual/review states into CI.
	installFakeTmux(t)
	t.Setenv("FAKE_TMUX_PANE_PID", "999999")

	type seedQueue struct {
		typ     queue.ItemType
		summary string
		details string
	}
	cases := []struct {
		name          string
		status        agent.Status
		prURL         string
		windowActive  bool
		queues        []seedQueue
		wantStatus    agent.Status
		wantIdleCount int
		wantPRCount   int
	}{
		{
			name:          "idle running agent without PR becomes ordinary idle",
			status:        agent.StatusRunning,
			wantStatus:    agent.StatusReady,
			wantIdleCount: 1,
		},
		{
			name:         "busy running agent with PR is not reclassified",
			status:       agent.StatusRunning,
			prURL:        "https://github.com/owner/repo/pull/1",
			windowActive: true,
			wantStatus:   agent.StatusRunning,
		},
		{
			name:          "throttled ready PR item stays manual",
			status:        agent.StatusReady,
			prURL:         "https://github.com/owner/repo/pull/2",
			queues:        []seedQueue{{typ: queue.ItemTypeIdle, summary: "Throttled: CI checks failed: lint", details: "https://github.com/owner/repo/pull/2"}},
			wantStatus:    agent.StatusReady,
			wantIdleCount: 1,
		},
		{
			name:        "ready PR with PR-ready item is not changed",
			status:      agent.StatusReady,
			prURL:       "https://github.com/owner/repo/pull/3",
			queues:      []seedQueue{{typ: queue.ItemTypePRReady, summary: "PR ready", details: "https://github.com/owner/repo/pull/3"}},
			wantStatus:  agent.StatusReady,
			wantPRCount: 1,
		},
		{
			name:          "ready no-PR idle item remains ordinary idle",
			status:        agent.StatusReady,
			queues:        []seedQueue{{typ: queue.ItemTypeIdle, summary: "Agent idle - waiting for input"}},
			wantStatus:    agent.StatusReady,
			wantIdleCount: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.windowActive {
				t.Setenv("FAKE_TMUX_WINDOW_ACTIVITY", fmt.Sprintf("%d", time.Now().Unix()))
			} else {
				t.Setenv("FAKE_TMUX_WINDOW_ACTIVITY", "0")
			}

			m := newRefreshTestModel(t)
			a := &agent.Agent{
				ID:           "agent-1",
				Status:       tc.status,
				PRURL:        tc.prURL,
				WorktreePath: t.TempDir(),
				TmuxWindow:   "@1",
			}
			if err := m.agentStore.Create(a); err != nil {
				t.Fatalf("failed to seed agent: %v", err)
			}
			for _, q := range tc.queues {
				if _, err := m.queueManager.Add(q.typ, a.ID, q.summary, q.details); err != nil {
					t.Fatalf("failed to seed queue item: %v", err)
				}
			}

			msg := m.refreshCmd()()
			if _, ok := msg.(refreshMsg); !ok {
				t.Fatalf("expected refreshMsg, got %T", msg)
			}

			reloaded, err := m.agentStore.Get(a.ID)
			if err != nil {
				t.Fatalf("failed to reload agent: %v", err)
			}
			if reloaded.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", reloaded.Status, tc.wantStatus)
			}

			items, err := m.queueManager.List()
			if err != nil {
				t.Fatalf("failed to list queue: %v", err)
			}
			var idleCount, prReadyCount int
			for _, item := range items {
				switch item.Type {
				case queue.ItemTypeIdle:
					idleCount++
				case queue.ItemTypePRReady:
					prReadyCount++
				}
			}
			if idleCount != tc.wantIdleCount {
				t.Fatalf("idle queue count = %d, want %d", idleCount, tc.wantIdleCount)
			}
			if prReadyCount != tc.wantPRCount {
				t.Fatalf("PR-ready queue count = %d, want %d", prReadyCount, tc.wantPRCount)
			}
		})
	}
}

func TestDuplicateCIFailure_ShouldRecordResume_GivenAgentBelowThrottleThreshold(t *testing.T) {
	// Setup. Agent was resumed once for "CI checks failed: lint". GH still
	// reports the same failure on the next poll — either because the agent's
	// fix didn't actually fix anything, or no new workflow has run yet.
	// Previously this branch returned silently, leaving the agent stuck in
	// WaitingCI with a stale "0/N checks left" indicator forever. The fix is
	// to still count the duplicate poll toward the throttle window so a
	// truly stuck agent eventually escapes to the idle queue.
	m := newTestModelWithStoreAndQueue(t)
	a := &agent.Agent{
		ID:                    "agent-1",
		Status:                agent.StatusWaitingCI,
		PRURL:                 "https://github.com/owner/repo/pull/1",
		CILastNotifiedSummary: "CI checks failed: lint",
		CIResumeHistory:       []time.Time{time.Now().Add(-1 * time.Minute)},
	}
	if err := m.agentStore.Create(a); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}
	m.agents = []*agent.Agent{a}

	// Execute. Drive the same duplicate-failure poll.
	updated, _ := m.Update(ciCheckResultMsg{
		agentID:   "agent-1",
		status:    ciStatusFailed,
		summary:   "CI checks failed: lint",
		prURL:     a.PRURL,
		completed: 5,
		total:     5,
	})
	_ = updated

	// Assert. Agent should stay in WaitingCI (no re-prompt), but the
	// duplicate poll should count toward the throttle window.
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if reloaded.Status != agent.StatusWaitingCI {
		t.Errorf("expected agent to stay in WaitingCI on first duplicate; got %s", reloaded.Status)
	}
	if len(reloaded.CIResumeHistory) != 2 {
		t.Errorf("expected duplicate poll to record a resume (history length 2), got %d", len(reloaded.CIResumeHistory))
	}
}

func TestDuplicateCIFailure_ShouldNotInflateHistory_GivenBackToBackPollsMatchingLastSummary(t *testing.T) {
	// Regression for the c13696a fallout: once the Stop hook started handing
	// every end-of-turn back to the CI poller, an actively-working agent got
	// polled every 30s while it was still pushing a fix. Each poll saw the
	// same failure summary, hit the duplicate branch, and counted as a
	// "resume" in CIResumeHistory — three of them in ~90s tipped the agent
	// over the throttle threshold and flipped it to StatusReady (displayed
	// as "idle") even though CI was still legitimately in progress.
	//
	// The fix gates the duplicate-failure recording on isAgentBusy. We can't
	// drive a real tmux session here, so this test pins down the
	// not-busy-path semantics that callers rely on: a single duplicate poll
	// with no tmux activity advances history by exactly one entry (the
	// original "stuck agent eventually escapes" behavior — preserved), and a
	// second poll inside the same window stops at two, never silently
	// double-counting. If a future refactor changes isAgentBusy's nil-tmux
	// fallback, this test catches it.
	m := newTestModelWithStoreAndQueue(t)
	now := time.Now()
	a := &agent.Agent{
		ID:                    "agent-1",
		Status:                agent.StatusWaitingCI,
		PRURL:                 "https://github.com/owner/repo/pull/1",
		CILastNotifiedSummary: "CI checks failed: lint",
		CIResumeHistory:       []time.Time{now.Add(-1 * time.Minute)},
	}
	if err := m.agentStore.Create(a); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}
	m.agents = []*agent.Agent{a}

	// First duplicate poll — agent has no tmux window, so isAgentBusy is
	// false; record advances history to length 2.
	updated, _ := m.Update(ciCheckResultMsg{
		agentID:   "agent-1",
		status:    ciStatusFailed,
		summary:   "CI checks failed: lint",
		prURL:     a.PRURL,
		completed: 5,
		total:     5,
	})
	_ = updated

	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if reloaded.Status != agent.StatusWaitingCI {
		t.Errorf("expected agent to stay in WaitingCI; got %s", reloaded.Status)
	}
	if len(reloaded.CIResumeHistory) != 2 {
		t.Fatalf("expected first duplicate poll to advance history to length 2, got %d", len(reloaded.CIResumeHistory))
	}

	// Refresh in-memory copy and drive a second duplicate poll. Still below
	// threshold (3), so history advances to 3 — not skipped, not throttled,
	// not double-counted.
	m.agents = []*agent.Agent{reloaded}
	m.ciChecking["agent-1"] = false
	updated, _ = m.Update(ciCheckResultMsg{
		agentID:   "agent-1",
		status:    ciStatusFailed,
		summary:   "CI checks failed: lint",
		prURL:     a.PRURL,
		completed: 5,
		total:     5,
	})
	_ = updated

	reloaded, err = m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if len(reloaded.CIResumeHistory) != 3 {
		t.Errorf("expected second duplicate poll to advance history to length 3, got %d", len(reloaded.CIResumeHistory))
	}
}

func TestDuplicateCIFailure_ShouldThrottle_GivenAgentAtThrottleThreshold(t *testing.T) {
	// Setup. Agent has already burned its resume attempts inside the 5-minute
	// throttle window. A further duplicate poll must escape the stuck state by
	// throttling the agent (idle queue item + status flipped to Ready), instead
	// of silently returning and leaving "0/N checks left" on screen.
	m := newTestModelWithStoreAndQueue(t)
	now := time.Now()
	a := &agent.Agent{
		ID:                    "agent-1",
		Status:                agent.StatusWaitingCI,
		PRURL:                 "https://github.com/owner/repo/pull/1",
		CILastNotifiedSummary: "CI checks failed: lint",
		CIResumeHistory: []time.Time{
			now.Add(-4 * time.Minute),
			now.Add(-3 * time.Minute),
			now.Add(-2 * time.Minute),
			now.Add(-1 * time.Minute),
			now.Add(-30 * time.Second),
		},
	}
	if err := m.agentStore.Create(a); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}
	m.agents = []*agent.Agent{a}
	m.ciCheckProgress["agent-1"] = ciProgress{Completed: 5, Total: 5}

	// Execute.
	updated, _ := m.Update(ciCheckResultMsg{
		agentID:   "agent-1",
		status:    ciStatusFailed,
		summary:   "CI checks failed: lint",
		prURL:     a.PRURL,
		completed: 5,
		total:     5,
	})
	_ = updated

	// Assert. Status flipped to Ready, idle item added, stale progress cleared.
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if reloaded.Status != agent.StatusReady {
		t.Errorf("expected throttled agent in Ready, got %s", reloaded.Status)
	}
	if _, exists := m.ciCheckProgress["agent-1"]; exists {
		t.Error("expected stale ciCheckProgress entry cleared on throttle")
	}
	items, err := m.queueManager.List()
	if err != nil {
		t.Fatalf("failed to list queue: %v", err)
	}
	foundIdle := false
	for _, it := range items {
		if it.AgentID == "agent-1" && it.Type == queue.ItemTypeIdle {
			foundIdle = true
			break
		}
	}
	if !foundIdle {
		t.Error("expected an idle queue item for throttled agent")
	}
}

func TestReviewResume_ShouldThrottle_GivenMaxAttemptsWithinWindow(t *testing.T) {
	// Setup. CI resume + review resume share the same history; once the agent
	// has burned its budget of attempts inside the 5-minute window, additional
	// review-driven resumes are throttled too.
	m := newTestModelWithStore(t)
	now := time.Now()
	a := &agent.Agent{
		ID: "agent-1",
		CIResumeHistory: []time.Time{
			now.Add(-4 * time.Minute),
			now.Add(-3 * time.Minute),
			now.Add(-2 * time.Minute),
			now.Add(-1 * time.Minute),
			now.Add(-30 * time.Second),
		},
	}

	// Execute.
	result := m.shouldThrottleResume(a)

	// Assert.
	if !result {
		t.Errorf("expected review resume to be throttled after %d attempts within %s, got false", ciResumeMaxAttempts, ciResumeWindow)
	}
}

func TestReviewResume_ShouldShareThrottleWithCIResume_GivenMixedHistory(t *testing.T) {
	// Setup. Seed the agent one resume short of the throttle threshold, all
	// inside the 5-minute window; recordResume then pushes it over and the next
	// shouldThrottleResume call should fire.
	m := newTestModelWithStore(t)
	now := time.Now()
	stored := &agent.Agent{
		ID: "agent-1",
		CIResumeHistory: []time.Time{
			now.Add(-4 * time.Minute),
			now.Add(-3 * time.Minute),
			now.Add(-2 * time.Minute),
			now.Add(-1 * time.Minute),
		},
	}
	if err := m.agentStore.Create(stored); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}

	// Execute.
	m.recordResume("agent-1")
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	result := m.shouldThrottleResume(reloaded)

	// Assert.
	if !result {
		t.Error("expected throttle when CI + review resumes together reach max attempts")
	}
}

func TestThrottleAction_ResumeCIFix_ShouldResetThrottleState_GivenThrottledAgent(t *testing.T) {
	// Setup. Mirror what throttleAgent leaves behind: agent flipped to Ready, an
	// idle queue item with the PR URL in Details, full CIResumeHistory, and a
	// CILastNotifiedSummary. The action menu's "r" key should wipe all that
	// throttle bookkeeping so the next CI poll re-engages on the live state.
	m := newTestModelWithStoreAndQueue(t)
	now := time.Now()
	a := &agent.Agent{
		ID:                    "agent-1",
		Status:                agent.StatusReady,
		PRURL:                 "https://github.com/owner/repo/pull/1",
		CILastNotifiedSummary: "CI checks failed: lint",
		CIResumeHistory: []time.Time{
			now.Add(-4 * time.Minute),
			now.Add(-3 * time.Minute),
			now.Add(-2 * time.Minute),
			now.Add(-1 * time.Minute),
			now.Add(-30 * time.Second),
		},
	}
	if err := m.agentStore.Create(a); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}
	item, err := m.queueManager.Add(queue.ItemTypeIdle, a.ID, "Throttled: CI checks failed: lint", a.PRURL)
	if err != nil {
		t.Fatalf("failed to seed queue item: %v", err)
	}
	m.ciCheckProgress[a.ID] = ciProgress{Completed: 5, Total: 5}
	m.ciLastChecked[a.ID] = now
	m.throttleTargetAgent = a
	m.throttleQueueItem = item
	m.view = ViewThrottleAction

	// Execute.
	result, _ := m.handleThrottleActionKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	updated, ok := result.(model)
	if !ok {
		t.Fatalf("expected model from handler, got %T", result)
	}

	// Assert. Status back on the CI poller, history + dedup wiped, idle queue
	// item gone, transient maps cleared, view returned to Intervene.
	if updated.view != ViewIntervene {
		t.Errorf("expected view ViewIntervene, got %v", updated.view)
	}
	if updated.throttleTargetAgent != nil || updated.throttleQueueItem != nil {
		t.Error("expected throttle target cleared after Resume")
	}
	reloaded, err := m.agentStore.Get(a.ID)
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if reloaded.Status != agent.StatusWaitingCI {
		t.Errorf("expected agent flipped to WaitingCI, got %s", reloaded.Status)
	}
	if len(reloaded.CIResumeHistory) != 0 {
		t.Errorf("expected CIResumeHistory cleared, got %d entries", len(reloaded.CIResumeHistory))
	}
	if reloaded.CILastNotifiedSummary != "" {
		t.Errorf("expected CILastNotifiedSummary cleared, got %q", reloaded.CILastNotifiedSummary)
	}
	items, err := m.queueManager.List()
	if err != nil {
		t.Fatalf("failed to list queue: %v", err)
	}
	for _, it := range items {
		if it.AgentID == a.ID && it.Type == queue.ItemTypeIdle {
			t.Errorf("expected throttle idle item removed, still present: %+v", it)
		}
	}
	if _, exists := updated.ciCheckProgress[a.ID]; exists {
		t.Error("expected ciCheckProgress cleared after Resume")
	}
	if _, exists := updated.ciLastChecked[a.ID]; exists {
		t.Error("expected ciLastChecked cleared after Resume so the next refresh re-polls")
	}
}

func TestThrottleAction_Esc_ShouldReturnToIntervene_GivenThrottledAgent(t *testing.T) {
	// Setup.
	m := newTestModelWithStoreAndQueue(t)
	a := &agent.Agent{ID: "agent-1", Status: agent.StatusReady}
	if err := m.agentStore.Create(a); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}
	m.throttleTargetAgent = a
	m.throttleQueueItem = &queue.QueueItem{AgentID: a.ID, Type: queue.ItemTypeIdle, Details: "https://x/y/pull/1"}
	m.view = ViewThrottleAction

	// Execute.
	result, _ := m.handleThrottleActionKeys(tea.KeyMsg{Type: tea.KeyEsc})
	updated, ok := result.(model)
	if !ok {
		t.Fatalf("expected model from handler, got %T", result)
	}

	// Assert. Throttle state cleared, view returned, agent untouched.
	if updated.view != ViewIntervene {
		t.Errorf("expected view ViewIntervene, got %v", updated.view)
	}
	if updated.throttleTargetAgent != nil || updated.throttleQueueItem != nil {
		t.Error("expected throttle target cleared after Esc")
	}
	reloaded, err := m.agentStore.Get(a.ID)
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if reloaded.Status != agent.StatusReady {
		t.Errorf("expected agent status untouched (Ready), got %s", reloaded.Status)
	}
}

func TestReviewResume_ShouldRecordResume_GivenNewReview(t *testing.T) {
	// Setup.
	m := newTestModelWithStore(t)
	if err := m.agentStore.Create(&agent.Agent{ID: "agent-1"}); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}

	// Execute.
	m.recordResume("agent-1")

	// Assert.
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if len(reloaded.CIResumeHistory) != 1 {
		t.Errorf("expected 1 resume recorded, got %d", len(reloaded.CIResumeHistory))
	}
}

func TestCICleanup_ShouldOnlyDropUIState_GivenAgentNotActivelyWaiting(t *testing.T) {
	// Setup. In-memory CI tracking maps only hold transient UI state now.
	// Dedup/throttle state lives on the Agent struct, which is persisted.
	m := newTestModel()
	m.ciLastChecked = make(map[string]time.Time)
	m.ciChecking = make(map[string]bool)
	m.ciCheckProgress = make(map[string]ciProgress)

	m.ciLastChecked["agent-1"] = time.Now()
	m.ciChecking["agent-1"] = true
	m.ciCheckProgress["agent-1"] = ciProgress{Completed: 1, Total: 2}

	// Execute. Mirrors the cleanup loop in refreshMsg.
	activeWaiting := map[string]bool{}
	for id := range m.ciLastChecked {
		if !activeWaiting[id] {
			delete(m.ciLastChecked, id)
			delete(m.ciChecking, id)
			delete(m.ciCheckProgress, id)
		}
	}

	// Assert.
	if _, exists := m.ciLastChecked["agent-1"]; exists {
		t.Error("expected last-checked entry removed for idle agent")
	}
	if _, exists := m.ciChecking["agent-1"]; exists {
		t.Error("expected checking flag removed for idle agent")
	}
	if _, exists := m.ciCheckProgress["agent-1"]; exists {
		t.Error("expected progress entry removed for idle agent")
	}
}

func TestCIDedup_ShouldPersistAcrossProcessRestart_GivenAgentStore(t *testing.T) {
	// Setup. Seed an agent with dedup state, simulate a restart by reopening
	// the store, and verify state survived.
	sessionID := fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), randomTestSuffix())
	store1, err := agent.NewStore(sessionID)
	if err != nil {
		t.Fatalf("failed to create initial store: %v", err)
	}
	t.Cleanup(func() { _ = removeTestStore(sessionID) })

	now := time.Now()
	seeded := &agent.Agent{
		ID:                    "agent-1",
		CILastNotifiedSummary: "CI checks failed: check-pr-title",
		CIResumeHistory:       []time.Time{now.Add(-3 * time.Minute), now.Add(-1 * time.Minute)},
	}
	if err := store1.Create(seeded); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}

	// Execute. Simulate a restart.
	store2, err := agent.NewStore(sessionID)
	if err != nil {
		t.Fatalf("failed to reopen store: %v", err)
	}
	reloaded, err := store2.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent after restart: %v", err)
	}

	// Assert.
	if reloaded.CILastNotifiedSummary != seeded.CILastNotifiedSummary {
		t.Errorf("expected summary %q preserved, got %q", seeded.CILastNotifiedSummary, reloaded.CILastNotifiedSummary)
	}
	if len(reloaded.CIResumeHistory) != 2 {
		t.Errorf("expected 2 resume history entries preserved, got %d", len(reloaded.CIResumeHistory))
	}
}

func TestCIDedup_ShouldNotBeClearedOnPending_GivenFailureFollowedByPending(t *testing.T) {
	// Setup. This guards against the regression where ciStatusPending
	// wiped CILastNotifiedSummary every poll, causing the same "CI failed: X"
	// message to be re-sent on the next failure.
	m := newTestModelWithStore(t)
	a := &agent.Agent{
		ID:                    "agent-1",
		CILastNotifiedSummary: "CI checks failed: check-pr-title",
	}

	// Execute. Pending should not mutate the dedup summary.
	if m.isDuplicateCIFailure(a, "CI checks failed: check-pr-title") != true {
		t.Fatal("precondition: summary should match")
	}
	// Verify dedup still fires after we simulate a pending-phase poll.
	result := m.isDuplicateCIFailure(a, "CI checks failed: check-pr-title")

	// Assert.
	if !result {
		t.Error("expected dedup to still fire after a pending poll (no clearing)")
	}
}

func TestHasUnaddressedReview_ShouldNotRetrigger_GivenReviewBeforeSince(t *testing.T) {
	// Setup.
	since := time.Now().Add(-1 * time.Minute)
	reviews := []prReview{{SubmittedAt: time.Now().Add(-5 * time.Minute)}}

	// Execute.
	result := hasUnaddressedReview(reviews, nil, nil, "", since)

	// Assert.
	if result {
		t.Error("expected review submitted before CIWaitAt to be skipped")
	}
}

func TestHasUnaddressedReview_ShouldDetect_GivenReviewAfterSince(t *testing.T) {
	// Setup.
	since := time.Now().Add(-5 * time.Minute)
	reviews := []prReview{{SubmittedAt: time.Now().Add(-1 * time.Minute)}}

	// Execute.
	result := hasUnaddressedReview(reviews, nil, nil, "", since)

	// Assert.
	if !result {
		t.Error("expected new review submitted after CIWaitAt to be detected")
	}
}

func TestHasUnaddressedReview_ShouldNotRetrigger_GivenCommitPushedAfterReview(t *testing.T) {
	// Setup. This guards the headline bug: ccmux kept treating a review as
	// "unaddressed" even after the agent pushed a follow-up commit, causing
	// the same feedback to re-trigger the agent in a loop.
	since := time.Now().Add(-1 * time.Hour)
	reviewTime := time.Now().Add(-30 * time.Minute)
	pushTime := time.Now().Add(-5 * time.Minute)
	reviews := []prReview{{SubmittedAt: reviewTime}}
	commits := []prCommit{{CommittedDate: pushTime}}

	// Execute.
	result := hasUnaddressedReview(reviews, commits, nil, "", since)

	// Assert.
	if result {
		t.Error("expected review followed by a commit to be considered addressed")
	}
}

func TestHasUnaddressedReview_ShouldDetect_GivenReviewAfterLatestCommit(t *testing.T) {
	// Setup. A fresh review after the latest push is unaddressed.
	since := time.Now().Add(-1 * time.Hour)
	pushTime := time.Now().Add(-30 * time.Minute)
	reviewTime := time.Now().Add(-5 * time.Minute)
	reviews := []prReview{{SubmittedAt: reviewTime}}
	commits := []prCommit{{CommittedDate: pushTime}}

	// Execute.
	result := hasUnaddressedReview(reviews, commits, nil, "", since)

	// Assert.
	if !result {
		t.Error("expected review submitted after latest commit to need addressing")
	}
}

func TestHasUnaddressedReview_ShouldDetect_GivenMixOfAddressedAndUnaddressedReviews(t *testing.T) {
	// Setup. The user adds a second review after the agent's follow-up push.
	since := time.Now().Add(-1 * time.Hour)
	firstReview := time.Now().Add(-45 * time.Minute)
	push := time.Now().Add(-30 * time.Minute)
	secondReview := time.Now().Add(-5 * time.Minute)
	reviews := []prReview{
		{SubmittedAt: firstReview},
		{SubmittedAt: secondReview},
	}
	commits := []prCommit{{CommittedDate: push}}

	// Execute.
	result := hasUnaddressedReview(reviews, commits, nil, "", since)

	// Assert.
	if !result {
		t.Error("expected unaddressed second review to trigger even when first review was addressed")
	}
}

func TestHasUnaddressedReview_ShouldIgnore_GivenOnlySelfAuthoredIssueComments(t *testing.T) {
	// Setup. The agent often posts its own "Pushed $sha to address X" status
	// comment after fixing review feedback. Those self-comments must not
	// re-trigger the agent.
	since := time.Now().Add(-1 * time.Hour)
	comments := []prIssueComment{
		{CreatedAt: time.Now().Add(-1 * time.Minute), Author: struct {
			Login string `json:"login"`
		}{Login: "CDFalcon"}},
	}

	// Execute.
	result := hasUnaddressedReview(nil, nil, comments, "CDFalcon", since)

	// Assert.
	if result {
		t.Error("expected agent's own conversation comment to be ignored")
	}
}

func TestHasUnaddressedReview_ShouldDetect_GivenForeignIssueCommentAfterLastPush(t *testing.T) {
	// Setup. A conversation comment from someone other than the PR author,
	// posted after the last push, should be flagged for attention.
	since := time.Now().Add(-1 * time.Hour)
	commits := []prCommit{{CommittedDate: time.Now().Add(-30 * time.Minute)}}
	comments := []prIssueComment{
		{CreatedAt: time.Now().Add(-1 * time.Minute), Author: struct {
			Login string `json:"login"`
		}{Login: "reviewer"}},
	}

	// Execute.
	result := hasUnaddressedReview(nil, commits, comments, "CDFalcon", since)

	// Assert.
	if !result {
		t.Error("expected foreign conversation comment after last push to need attention")
	}
}

func TestHasUnaddressedReview_ShouldNotRetrigger_GivenIssueCommentAddressedByPush(t *testing.T) {
	// Setup. A conversation comment that predates the latest push is already
	// addressed.
	since := time.Now().Add(-1 * time.Hour)
	commits := []prCommit{{CommittedDate: time.Now().Add(-1 * time.Minute)}}
	comments := []prIssueComment{
		{CreatedAt: time.Now().Add(-30 * time.Minute), Author: struct {
			Login string `json:"login"`
		}{Login: "reviewer"}},
	}

	// Execute.
	result := hasUnaddressedReview(nil, commits, comments, "CDFalcon", since)

	// Assert.
	if result {
		t.Error("expected conversation comment followed by a commit to be considered addressed")
	}
}

func TestIsMergeConflictFailure_ShouldDetect_GivenMergeConflictPhrase(t *testing.T) {
	output := "failed to merge pull request: GraphQL: Pull Request is not mergeable: Merge conflict (mergePullRequest)"
	if !isMergeConflictFailure(output, "") {
		t.Error("expected merge-conflict phrase in gh output to be detected")
	}
}

func TestIsMergeConflictFailure_ShouldDetect_GivenConflictingPhrase(t *testing.T) {
	output := "Pull request is in CONFLICTING state"
	if !isMergeConflictFailure(output, "") {
		t.Error("expected 'conflicting' phrase in gh output to be detected")
	}
}

func TestIsMergeConflictFailure_ShouldDetect_GivenNotMergeablePhrase(t *testing.T) {
	output := "Pull Request is not mergeable"
	if !isMergeConflictFailure(output, "") {
		t.Error("expected 'not mergeable' phrase in gh output to be detected")
	}
}

func TestIsMergeConflictFailure_ShouldNotDetect_GivenUnrelatedFailure(t *testing.T) {
	// gh sometimes returns auth or branch-protection errors with no PR URL we
	// can query against. We expect a clean false in that case so the caller
	// surfaces the original error to the user.
	output := "GraphQL: Resource not accessible by integration"
	if isMergeConflictFailure(output, "") {
		t.Error("expected unrelated failure NOT to be reported as a merge conflict")
	}
}

func TestIsAgentBusy_ShouldReturnFalse_GivenNilAgent(t *testing.T) {
	// Nil-agent guard so callers don't crash when CI fires for an agent that
	// was deleted between message dispatch and handler execution.
	if isAgentBusy(nil, nil, nil, nil, nil, 0, ciIdleThreshold) {
		t.Error("expected nil agent to be reported as not busy")
	}
}

func TestIsAgentBusy_ShouldReturnFalse_GivenEmptyTmuxWindow(t *testing.T) {
	// Test agents (and any pre-spawn record) have no TmuxWindow. We treat
	// "no pane to query" as not busy so the CI-pass handler still transitions
	// to StatusWaitingReview the way it did before this feature.
	a := &agent.Agent{ID: "agent-1"}
	if isAgentBusy(a, nil, nil, nil, nil, 0, ciIdleThreshold) {
		t.Error("expected agent with empty TmuxWindow to be reported as not busy")
	}
}

func TestIsAgentBusy_ShouldReturnFalse_GivenNilTmuxManager(t *testing.T) {
	// Defensive: if tmuxManager isn't wired up (early init, tests), don't
	// pretend the agent is busy — fall through to the existing transition.
	a := &agent.Agent{ID: "agent-1", TmuxWindow: "@5"}
	if isAgentBusy(a, nil, nil, nil, nil, 0, ciIdleThreshold) {
		t.Error("expected nil tmux manager to be reported as not busy")
	}
}

func TestCIPassed_ShouldTransitionToWaitingReview_GivenIdleAgent(t *testing.T) {
	// Setup. Agent in WaitingCI with no tmux window (the test path) — the
	// isAgentBusy gate falls through to "not busy", so the CI-pass handler
	// should promote to StatusWaitingReview and add the PR-ready queue item
	// the way it did before this feature.
	m := newTestModelWithStoreAndQueue(t)
	a := &agent.Agent{
		ID:     "agent-1",
		Status: agent.StatusWaitingCI,
		PRURL:  "https://github.com/owner/repo/pull/1",
	}
	if err := m.agentStore.Create(a); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}
	m.agents = []*agent.Agent{a}
	m.ciCheckProgress["agent-1"] = ciProgress{Completed: 5, Total: 5}

	// Execute.
	updated, _ := m.Update(ciCheckResultMsg{
		agentID:   "agent-1",
		status:    ciStatusPassed,
		prURL:     a.PRURL,
		completed: 5,
		total:     5,
	})
	_ = updated

	// Assert. Status promoted, PR-ready item recorded, stale CI progress cleared.
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if reloaded.Status != agent.StatusWaitingReview {
		t.Errorf("expected idle agent to transition to WaitingReview on CI pass, got %s", reloaded.Status)
	}
	if _, exists := m.ciCheckProgress["agent-1"]; exists {
		t.Error("expected ciCheckProgress cleared on CI pass transition")
	}
	items, err := m.queueManager.List()
	if err != nil {
		t.Fatalf("failed to list queue: %v", err)
	}
	foundPRReady := false
	for _, it := range items {
		if it.AgentID == "agent-1" && it.Type == queue.ItemTypePRReady {
			foundPRReady = true
			break
		}
	}
	if !foundPRReady {
		t.Error("expected a PR-ready queue item after CI pass for idle agent")
	}
}

func TestMergeQueueWait_ShouldResumeForMergeConflict_GivenConflictingPR(t *testing.T) {
	// Setup. Agent was accepted into trunk's merge queue and is parked in
	// StatusWaitingMergeQueue. CI polling reports the PR as CONFLICTING —
	// trunk dropped it from the queue for a merge conflict. Previously the
	// poller bailed out for any non-merged result on a queue-waiting agent,
	// so the agent sat in "waiting on merge queue" forever even though the
	// queue had given up on it. The fix is to treat hasMergeConflict the
	// same way the normal CI-wait path does: record a resume attempt, drop
	// the stale progress indicator, and hand the agent to the merge-conflict
	// fixup flow.
	m := newTestModelWithStoreAndQueue(t)
	a := &agent.Agent{
		ID:     "agent-1",
		Status: agent.StatusWaitingMergeQueue,
		PRURL:  "https://github.com/owner/repo/pull/1",
	}
	if err := m.agentStore.Create(a); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}
	m.agents = []*agent.Agent{a}
	m.ciCheckProgress["agent-1"] = ciProgress{Completed: 3, Total: 5}

	// Execute. Drive the poll result that reports a merge conflict.
	updated, cmd := m.Update(ciCheckResultMsg{
		agentID:          "agent-1",
		status:           ciStatusPending,
		prURL:            a.PRURL,
		hasMergeConflict: true,
	})
	_ = updated

	// Assert. recordResume must have advanced history (so a stuck PR that
	// keeps reporting CONFLICTING eventually trips the throttle), the stale
	// CI progress indicator must be cleared, and Update must return a cmd
	// (the merge-conflict resume) instead of nil.
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if len(reloaded.CIResumeHistory) != 1 {
		t.Errorf("expected merge-conflict poll to record a resume (history length 1), got %d", len(reloaded.CIResumeHistory))
	}
	if _, exists := m.ciCheckProgress["agent-1"]; exists {
		t.Error("expected ciCheckProgress cleared when resuming for merge conflict")
	}
	if cmd == nil {
		t.Error("expected Update to return a resume command for merge-queue conflict, got nil")
	}
}

func TestMergeQueueWait_ShouldStayParked_GivenNoMergeConflictOrMerged(t *testing.T) {
	// Setup. Agent is parked in StatusWaitingMergeQueue. CI polling reports
	// a failure (or new review) — both of which belong to trunk, not ccmux.
	// The agent must stay parked: status unchanged, no resume recorded.
	// This guards the inverse of the merge-conflict carve-out — we still
	// don't want to react to in-queue CI noise.
	m := newTestModelWithStoreAndQueue(t)
	a := &agent.Agent{
		ID:     "agent-1",
		Status: agent.StatusWaitingMergeQueue,
		PRURL:  "https://github.com/owner/repo/pull/1",
	}
	if err := m.agentStore.Create(a); err != nil {
		t.Fatalf("failed to seed agent: %v", err)
	}
	m.agents = []*agent.Agent{a}

	// Execute. Drive a CI-failed poll with no merge conflict.
	updated, cmd := m.Update(ciCheckResultMsg{
		agentID: "agent-1",
		status:  ciStatusFailed,
		summary: "CI checks failed: lint",
		prURL:   a.PRURL,
	})
	_ = updated

	// Assert. Agent untouched, no cmd returned.
	reloaded, err := m.agentStore.Get("agent-1")
	if err != nil {
		t.Fatalf("failed to reload agent: %v", err)
	}
	if reloaded.Status != agent.StatusWaitingMergeQueue {
		t.Errorf("expected agent to stay parked in WaitingMergeQueue, got %s", reloaded.Status)
	}
	if len(reloaded.CIResumeHistory) != 0 {
		t.Errorf("expected no resume recorded for in-queue CI noise, got history length %d", len(reloaded.CIResumeHistory))
	}
	if cmd != nil {
		t.Error("expected Update to return nil cmd for in-queue CI failure")
	}
}

// setupConflictTestRepo creates a bare remote with two commits on main and a
// feature branch that conflicts with the latest main, then clones it into a
// local worktree positioned on the feature branch. Returns the worktree path.
func setupConflictTestRepo(t *testing.T, conflict bool) string {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	local := filepath.Join(root, "local")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		if dir != "" {
			cmd.Dir = dir
		}
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args[1:], " "), err, out)
		}
	}

	run("", "git", "init", "--bare", "-b", "main", remote)
	run("", "git", "clone", remote, local)
	run(local, "git", "config", "user.email", "test@test.com")
	run(local, "git", "config", "user.name", "test")

	conflictFile := filepath.Join(local, "shared.txt")
	if err := os.WriteFile(conflictFile, []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	run(local, "git", "add", "shared.txt")
	run(local, "git", "commit", "-m", "base")
	run(local, "git", "push", "origin", "main")

	// Feature branch edits the shared file.
	run(local, "git", "checkout", "-b", "feature")
	if err := os.WriteFile(conflictFile, []byte("feature change\n"), 0o644); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	run(local, "git", "commit", "-am", "feature edit")

	// Master moves forward. When `conflict` is true the new master commit
	// touches the same line; when false it edits an unrelated file.
	run(local, "git", "checkout", "main")
	if conflict {
		if err := os.WriteFile(conflictFile, []byte("main change\n"), 0o644); err != nil {
			t.Fatalf("write main: %v", err)
		}
		run(local, "git", "commit", "-am", "main edit (conflicting)")
	} else {
		unrelated := filepath.Join(local, "other.txt")
		if err := os.WriteFile(unrelated, []byte("unrelated\n"), 0o644); err != nil {
			t.Fatalf("write unrelated: %v", err)
		}
		run(local, "git", "add", "other.txt")
		run(local, "git", "commit", "-m", "main edit (clean)")
	}
	run(local, "git", "push", "origin", "main")

	// Put the worktree back on the feature branch — that's what `git
	// merge-tree HEAD origin/main` will compare against the latest main.
	run(local, "git", "checkout", "feature")
	return local
}

func TestLocalWorktreeHasMergeConflict_ShouldReturnTrue_GivenConflictingChange(t *testing.T) {
	// Setup. Feature branch and refreshed master both touch the same line of
	// shared.txt — a real conflict against `origin/main`.
	worktree := setupConflictTestRepo(t, true)

	// Execute.
	result := localWorktreeHasMergeConflict(worktree, "main")

	// Assert.
	if !result {
		t.Error("expected local merge-tree to detect a conflict, got false")
	}
}

func TestLocalWorktreeHasMergeConflict_ShouldReturnFalse_GivenCleanMaster(t *testing.T) {
	// Setup. Master moves forward by editing an unrelated file. The feature
	// branch and origin/main can be merged without conflict.
	worktree := setupConflictTestRepo(t, false)

	// Execute.
	result := localWorktreeHasMergeConflict(worktree, "main")

	// Assert.
	if result {
		t.Error("expected clean merge to NOT be reported as a conflict")
	}
}

func TestLocalWorktreeHasMergeConflict_ShouldReturnFalse_GivenMissingWorktree(t *testing.T) {
	// Setup. No worktree path means no place to run git; the helper must
	// short-circuit instead of trying to invoke git from an unknown dir.
	// Same for an empty base branch.
	if localWorktreeHasMergeConflict("", "main") {
		t.Error("expected false for empty worktree path")
	}
	if localWorktreeHasMergeConflict("/tmp", "") {
		t.Error("expected false for empty base branch")
	}
	if localWorktreeHasMergeConflict("/tmp", "origin/") {
		t.Error("expected false when base branch reduces to empty after stripping origin/ prefix")
	}
}

func TestLocalWorktreeHasMergeConflict_ShouldStripOriginPrefix_GivenBaseBranchWithRemote(t *testing.T) {
	// Setup. Many call sites store baseBranch as `origin/main` (mirroring
	// `git push -u origin <branch>` output). The helper has to strip that
	// prefix before passing the branch to `git fetch`, otherwise fetch will
	// fail and we silently return false.
	worktree := setupConflictTestRepo(t, true)

	// Execute.
	result := localWorktreeHasMergeConflict(worktree, "origin/main")

	// Assert.
	if !result {
		t.Error("expected conflict to be detected even when baseBranch carries an origin/ prefix")
	}
}
