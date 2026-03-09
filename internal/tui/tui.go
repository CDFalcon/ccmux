// Package tui implements the orchestrator terminal UI.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/project"
	"github.com/CDFalcon/ccmux/internal/queue"
	"github.com/CDFalcon/ccmux/internal/tmux"
	"github.com/CDFalcon/ccmux/internal/updater"
	"github.com/CDFalcon/ccmux/internal/version"
)

type model struct {
	view         ViewState
	previousView ViewState
	agents       []*agent.Agent
	queueItems    []*queue.QueueItem
	projects      []*project.Project
	selectedIndex int
	selectedAgent *agent.Agent
	selectedProj  *project.Project
	err           error

	// Task spawn inputs
	taskInput             textarea.Model
	branchInput           textinput.Model
	branchOptions         []string
	branchFilter     textinput.Model
	filteredBranches []string
	spawnBranch      string

	// Project form inputs
	projectForm     projectFormModel
	editProjectForm editProjectFormModel
	newProjectName  string
	newProjectPath  string

	// Intervention input
	interveneInput textarea.Model
	interveneAgent *agent.Agent

	// Project import state
	projImporting    bool
	projImportLines  *projImportBuffer
	projImportCancel func()

	// Update state
	updateChecking    bool
	updateAvailable   bool
	updateVersion     string
	updateDownloading bool
	updateComplete    bool
	updateError       string
	changelogEntries  []updater.ChangelogEntry
	changelogLoading  bool

	// Ctrl+C confirmation
	ctrlCPressed bool

	// Animation state
	spinnerFrame    int
	marqueeOffset   int
	prevWindowNames map[string]string

	// CI wait tracking
	ciResumeTriggered map[string]bool

	// Resource monitoring
	agentResources map[string]*AgentResources
	totalMemKB     int64
	clkTck         int64
	prevCPUTicks   map[int]int64

	// Download progress
	downloadProgress *int64
	restartRequested bool

	agentStore   *agent.Store
	queueManager *queue.Queue
	projectStore *project.Store
	tmuxManager  *tmux.Manager
	sessionID    string
}

type projImportBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (b *projImportBuffer) addLine(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
}

func (b *projImportBuffer) lastN(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lines) <= n {
		result := make([]string, len(b.lines))
		copy(result, b.lines)
		return result
	}
	result := make([]string, n)
	copy(result, b.lines[len(b.lines)-n:])
	return result
}

func (b *projImportBuffer) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = nil
}

type branchEntry struct {
	tag      string
	name     string
	value    string
	isManual bool
}

type projectFormModel struct {
	nameInput  textinput.Model
	pathInput  textinput.Model
	focusIndex int // 0=name, 1=path
}

type editProjectFormModel struct {
	pathInput       textinput.Model
	baseBranchInput textinput.Model
	ciWaitInput     textinput.Model
	fastWTInput     textinput.Model
	focusIndex      int // 0=path, 1=baseBranch, 2=ciWait, 3=fastWT
}

func newProjectForm() projectFormModel {
	nameInput := textinput.New()
	nameInput.Placeholder = "my-project"
	nameInput.Width = 50
	nameInput.CharLimit = 50

	pathInput := textinput.New()
	pathInput.Placeholder = "/home/user/projects/my-project"
	pathInput.Width = 50
	pathInput.CharLimit = 200

	return projectFormModel{
		nameInput:  nameInput,
		pathInput:  pathInput,
		focusIndex: 0,
	}
}

func (pf *projectFormModel) blurAll() {
	pf.nameInput.Blur()
	pf.pathInput.Blur()
}

func (pf *projectFormModel) reset() {
	pf.nameInput.SetValue("")
	pf.pathInput.SetValue("")
	pf.focusIndex = 0
	pf.blurAll()
	pf.nameInput.Focus()
}

func newEditProjectForm() editProjectFormModel {
	pathInput := textinput.New()
	pathInput.Placeholder = "/home/user/projects/my-project"
	pathInput.Width = 50
	pathInput.CharLimit = 200

	baseBranchInput := textinput.New()
	baseBranchInput.Placeholder = "origin/master"
	baseBranchInput.Width = 50
	baseBranchInput.CharLimit = 100

	ciWaitInput := textinput.New()
	ciWaitInput.Placeholder = "5"
	ciWaitInput.Width = 10
	ciWaitInput.CharLimit = 5

	fastWTInput := textinput.New()
	fastWTInput.Placeholder = "no"
	fastWTInput.Width = 10
	fastWTInput.CharLimit = 5

	return editProjectFormModel{
		pathInput:       pathInput,
		baseBranchInput: baseBranchInput,
		ciWaitInput:     ciWaitInput,
		fastWTInput:     fastWTInput,
		focusIndex:      0,
	}
}

func (ef *editProjectFormModel) blurAll() {
	ef.pathInput.Blur()
	ef.baseBranchInput.Blur()
	ef.ciWaitInput.Blur()
	ef.fastWTInput.Blur()
}

func (ef *editProjectFormModel) focusCurrent() {
	ef.blurAll()
	switch ef.focusIndex {
	case 0:
		ef.pathInput.Focus()
	case 1:
		ef.baseBranchInput.Focus()
	case 2:
		ef.ciWaitInput.Focus()
	case 3:
		ef.fastWTInput.Focus()
	}
}

func (ef *editProjectFormModel) loadFromProject(p *project.Project) {
	ef.pathInput.SetValue(p.Path)
	ef.baseBranchInput.SetValue(p.DefaultBaseBranch)
	if p.CIWaitMinutes > 0 {
		ef.ciWaitInput.SetValue(fmt.Sprintf("%d", p.CIWaitMinutes))
	} else {
		ef.ciWaitInput.SetValue("")
	}
	if p.UseFastWorktrees {
		ef.fastWTInput.SetValue("yes")
	} else {
		ef.fastWTInput.SetValue("")
	}
	ef.focusIndex = 0
	ef.focusCurrent()
}

func findProjectPath(name string) string {
	if name == "" {
		return ""
	}

	homeDir, _ := os.UserHomeDir()
	searchDirs := []string{
		homeDir,
		homeDir + "/projects",
		homeDir + "/code",
		homeDir + "/src",
		homeDir + "/dev",
		homeDir + "/work",
		homeDir + "/repos",
	}

	for _, dir := range searchDirs {
		candidate := dir + "/" + name
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			// Check if it's a git repo
			if _, err := os.Stat(candidate + "/.git"); err == nil {
				return candidate
			}
		}
	}

	return ""
}

func getLocalBranches(repoPath string) []string {
	cmd := exec.Command("git", "-C", repoPath, "branch", "--format=%(refname:short)")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			branches = append(branches, line)
		}
	}
	return branches
}

func (m model) branchEntries() []branchEntry {
	var entries []branchEntry
	defaultBranch := "origin/master"
	if m.selectedProj != nil && m.selectedProj.DefaultBaseBranch != "" {
		defaultBranch = m.selectedProj.DefaultBaseBranch
	}
	entries = append(entries, branchEntry{tag: "(default)", name: defaultBranch, value: defaultBranch})
	entries = append(entries, branchEntry{name: "Manually specify branch...", isManual: true})

	if m.branchFilter.Value() != "" {
		for _, name := range m.filteredBranches {
			tag := "(local)"
			if strings.Contains(name, "/") {
				tag = "(remote)"
			}
			entries = append(entries, branchEntry{tag: tag, name: name, value: name})
		}
	} else {
		for _, name := range m.branchOptions {
			entries = append(entries, branchEntry{tag: "(local)", name: name, value: name})
		}
	}

	return entries
}

func (m *model) fuzzyFilterBranches() {
	query := m.branchFilter.Value()
	if query == "" {
		m.filteredBranches = nil
		return
	}

	allBranches := make([]string, len(m.branchOptions))
	copy(allBranches, m.branchOptions)

	if m.selectedProj != nil {
		remoteCmd := exec.Command("git", "-C", m.selectedProj.Path, "branch", "-r", "--format=%(refname:short)")
		if remoteOutput, err := remoteCmd.Output(); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(remoteOutput)), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.Contains(line, "HEAD") {
					allBranches = append(allBranches, line)
				}
			}
		}
	}

	matches := fuzzy.Find(query, allBranches)
	m.filteredBranches = make([]string, len(matches))
	for i, match := range matches {
		m.filteredBranches[i] = allBranches[match.Index]
	}
}

type tickMsg time.Time
type spinnerTickMsg time.Time
type refreshMsg struct {
	agents       []*agent.Agent
	queueItems   []*queue.QueueItem
	projects     []*project.Project
	resources    map[string]*AgentResources
	prevCPUTicks map[int]int64
}
type errMsg struct{ err error }
type successMsg struct{ msg string }
type clearMessageMsg struct{}
type clearCtrlCMsg struct{}
type spawnStartedMsg struct{}
type updateCheckTickMsg struct{}
type updateCheckResultMsg struct {
	version   string
	available bool
	err       error
}
type updateCompleteMsg struct {
	err error
}
type changelogFetchedMsg struct {
	entries []updater.ChangelogEntry
	err     error
}

func newFixedTextarea(placeholder string, width int) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.EndOfBufferCharacter = ' '
	ta.SetWidth(width)
	ta.SetHeight(5)
	ta.CharLimit = 0
	ta.KeyMap.InsertNewline.SetEnabled(false)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	return ta
}

func initialModel(agentStore *agent.Store, queueManager *queue.Queue, projectStore *project.Store, tmuxManager *tmux.Manager, sessionID string) model {
	taskInput := newFixedTextarea("Describe the task...", 60)
	branchInput := textinput.New()
	branchInput.Placeholder = "origin/master"
	branchInput.Width = 50
	branchInput.CharLimit = 100

	branchFilter := textinput.New()
	branchFilter.Placeholder = "Type to search branches..."
	branchFilter.Width = 50
	branchFilter.CharLimit = 100

	interveneInput := newFixedTextarea("Type message to send to agent...", 60)

	progress := new(int64)

	return model{
		view:              ViewMain,
		taskInput:         taskInput,
		branchInput:       branchInput,
		branchFilter:      branchFilter,
		interveneInput:    interveneInput,
		projectForm:       newProjectForm(),
		editProjectForm:   newEditProjectForm(),
		ciResumeTriggered: make(map[string]bool),
		prevWindowNames:   make(map[string]string),
		totalMemKB:        getTotalMemoryKB(),
		clkTck:            getClockTicks(),
		prevCPUTicks:      make(map[int]int64),
		downloadProgress:  progress,
		projImportLines:   &projImportBuffer{},
		agentStore:        agentStore,
		queueManager:      queueManager,
		projectStore:      projectStore,
		tmuxManager:       tmuxManager,
		sessionID:         sessionID,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		spinnerTickCmd(),
		m.refreshCmd(),
		checkForUpdateCmd(),
		updateCheckTickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

func (m model) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		agents, _ := m.agentStore.List()
		projects, _ := m.projectStore.List()

		now := time.Now()
		const idleThreshold = 10 * time.Second
		const readyGracePeriod = 15 * time.Second

		queueItems, _ := m.queueManager.List()

		idleItemByAgent := make(map[string]*queue.QueueItem)
		prReadyByAgent := make(map[string]bool)
		for _, item := range queueItems {
			if item.Type == queue.ItemTypeIdle {
				idleItemByAgent[item.AgentID] = item
			} else if item.Type == queue.ItemTypePRReady {
				prReadyByAgent[item.AgentID] = true
			}
		}

		changed := false
		for _, a := range agents {
			if a.TmuxWindow == "" {
				continue
			}

			if a.Status == agent.StatusRunning {
				activity, err := m.tmuxManager.GetWindowActivity(a.TmuxWindow)
				if err != nil {
					continue
				}

				isIdle := now.Sub(activity) > idleThreshold
				_, hasIdleItem := idleItemByAgent[a.ID]

				if isIdle && !hasIdleItem {
					m.queueManager.Add(queue.ItemTypeIdle, a.ID, "Agent idle - waiting for input", "")
					changed = true
				} else if !isIdle && hasIdleItem {
					m.queueManager.Remove(idleItemByAgent[a.ID].ID)
					changed = true
				}
			} else if a.Status == agent.StatusReady {
				if prReadyByAgent[a.ID] {
					continue
				}
				if now.Sub(a.UpdatedAt) < readyGracePeriod {
					continue
				}
				activity, err := m.tmuxManager.GetWindowActivity(a.TmuxWindow)
				if err != nil {
					continue
				}
				if now.Sub(activity) < idleThreshold {
					m.agentStore.Update(a.ID, func(ag *agent.Agent) {
						ag.Status = agent.StatusRunning
					})
					m.queueManager.RemoveByAgent(a.ID)
					changed = true
				}
			}
		}

		if changed {
			agents, _ = m.agentStore.List()
			queueItems, _ = m.queueManager.List()
		}

		resources, newCPUTicks := queryAllAgentResources(
			agents, m.tmuxManager, m.totalMemKB, m.clkTck, m.prevCPUTicks,
		)

		return refreshMsg{
			agents:       agents,
			queueItems:   queueItems,
			projects:     projects,
			resources:    resources,
			prevCPUTicks: newCPUTicks,
		}
	}
}

func clearMessageCmd() tea.Cmd {
	return tea.Tick(30*time.Second, func(t time.Time) tea.Msg {
		return clearMessageMsg{}
	})
}

func clearCtrlCCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return clearCtrlCMsg{}
	})
}

func checkForUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		latest, available, err := updater.CheckForUpdate()
		return updateCheckResultMsg{version: latest, available: available, err: err}
	}
}

func updateCheckTickCmd() tea.Cmd {
	return tea.Tick(5*time.Minute, func(t time.Time) tea.Msg {
		return updateCheckTickMsg{}
	})
}

func fetchChangelogCmd(currentVersion, latestVersion string) tea.Cmd {
	return func() tea.Msg {
		entries, err := updater.FetchChangelog(currentVersion, latestVersion)
		return changelogFetchedMsg{entries: entries, err: err}
	}
}

func downloadUpdateCmd(targetVersion string, progress *int64) tea.Cmd {
	return func() tea.Msg {
		err := updater.DownloadUpdateWithProgress(targetVersion, func(pct int) {
			atomic.StoreInt64(progress, int64(pct))
		})
		return updateCompleteMsg{err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tickMsg:
		return m, tea.Batch(tickCmd(), m.refreshCmd())

	case spinnerTickMsg:
		shouldAnimate := m.updateChecking || m.updateDownloading || m.changelogLoading || m.projImporting
		if !shouldAnimate {
			for _, a := range m.agents {
				if a.Status == agent.StatusSpawning || a.Status == agent.StatusRunning || a.Status == agent.StatusKilling || a.Status == agent.StatusCleaningUp || a.Status == agent.StatusWaitingCI {
					shouldAnimate = true
					break
				}
			}
		}
		if shouldAnimate {
			m.spinnerFrame = (m.spinnerFrame + 1) % SpinnerFrameCount
			m.marqueeOffset++
			m.updateWindowNames()
		}
		return m, spinnerTickCmd()

	case refreshMsg:
		m.agents = msg.agents
		m.queueItems = msg.queueItems
		m.projects = msg.projects
		m.agentResources = msg.resources
		m.prevCPUTicks = msg.prevCPUTicks

		projectMap := make(map[string]*project.Project)
		for _, p := range m.projects {
			projectMap[p.Name] = p
		}

		activeWaiting := make(map[string]bool)
		var cmds []tea.Cmd
		for _, a := range m.agents {
			if a.Status == agent.StatusWaitingCI {
				activeWaiting[a.ID] = true
				waitMinutes := project.DefaultCIWaitMinutes
				if p, ok := projectMap[a.ProjectName]; ok {
					waitMinutes = p.EffectiveCIWaitMinutes()
				}
				if time.Since(a.UpdatedAt) >= time.Duration(waitMinutes)*time.Minute && !m.ciResumeTriggered[a.ID] {
					m.ciResumeTriggered[a.ID] = true
					cmds = append(cmds, m.resumeCICheckCmd(a))
				}
			}
		}
		for id := range m.ciResumeTriggered {
			if !activeWaiting[id] {
				delete(m.ciResumeTriggered, id)
			}
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case spawnStartedMsg:
		return m, nil

	case updateCheckTickMsg:
		if !m.updateChecking && !m.updateDownloading {
			m.updateChecking = true
			return m, tea.Batch(checkForUpdateCmd(), updateCheckTickCmd())
		}
		return m, updateCheckTickCmd()

	case updateCheckResultMsg:
		m.updateChecking = false
		if msg.err != nil {
			m.updateError = fmt.Sprintf("Update check failed: %s", msg.err.Error())
			return m, nil
		}
		m.updateVersion = msg.version
		m.updateAvailable = msg.available
		if msg.available {
			m.updateDownloading = true
			atomic.StoreInt64(m.downloadProgress, 0)
			m.changelogLoading = true
			return m, tea.Batch(
				fetchChangelogCmd(version.Version, msg.version),
				downloadUpdateCmd(msg.version, m.downloadProgress),
			)
		}
		return m, nil

	case changelogFetchedMsg:
		m.changelogLoading = false
		if msg.err == nil {
			m.changelogEntries = msg.entries
		}
		return m, nil

	case updateCompleteMsg:
		m.updateDownloading = false
		if msg.err != nil {
			m.updateError = fmt.Sprintf("Update failed: %s", msg.err.Error())
			return m, nil
		}
		m.updateComplete = true
		return m, nil

	case errMsg:
		m.err = msg.err
		if m.projImporting {
			m.projImporting = false
			m.projImportLines.reset()
			m.projImportCancel = nil
			m.view = ViewManageProjects
		}
		return m, tea.Batch(clearMessageCmd(), m.refreshCmd())

	case successMsg:
		m.err = nil
		if m.projImporting {
			m.projImporting = false
			m.projImportLines.reset()
			m.projImportCancel = nil
			m.view = ViewManageProjects
		}
		return m, m.refreshCmd()

	case clearMessageMsg:
		m.err = nil
		return m, nil

	case clearCtrlCMsg:
		m.ctrlCPressed = false
		return m, nil

	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	}

	// Update text inputs for blink cursor
	if m.view == ViewNewTaskBranch {
		var cmd tea.Cmd
		m.branchFilter, cmd = m.branchFilter.Update(msg)
		cmds = append(cmds, cmd)
	}
	if m.view == ViewNewTaskBranchInput {
		var cmd tea.Cmd
		m.branchInput, cmd = m.branchInput.Update(msg)
		cmds = append(cmds, cmd)
	}
	if m.view == ViewNewTaskInput {
		var cmd tea.Cmd
		m.taskInput, cmd = m.taskInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	if m.view == ViewAddProjectName {
		var cmd tea.Cmd
		m.projectForm.nameInput, cmd = m.projectForm.nameInput.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.view == ViewAddProjectPath {
		var cmd tea.Cmd
		m.projectForm.pathInput, cmd = m.projectForm.pathInput.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.view == ViewEditProject {
		var cmd tea.Cmd
		switch m.editProjectForm.focusIndex {
		case 0:
			m.editProjectForm.pathInput, cmd = m.editProjectForm.pathInput.Update(msg)
		case 1:
			m.editProjectForm.baseBranchInput, cmd = m.editProjectForm.baseBranchInput.Update(msg)
		case 2:
			m.editProjectForm.ciWaitInput, cmd = m.editProjectForm.ciWaitInput.Update(msg)
		case 3:
			m.editProjectForm.fastWTInput, cmd = m.editProjectForm.fastWTInput.Update(msg)
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.view == ViewInterveneInput {
		var cmd tea.Cmd
		m.interveneInput, cmd = m.interveneInput.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	if len(cmds) > 0 {
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global two-stage detach with Ctrl+C
	if msg.String() == "ctrl+c" {
		if m.ctrlCPressed {
			return m, m.detachCmd()
		}
		m.ctrlCPressed = true
		return m, clearCtrlCCmd()
	}

	// Any other key clears the Ctrl+C state
	if m.ctrlCPressed {
		m.ctrlCPressed = false
	}

	if m.view != ViewHelp {
		if msg.Type == tea.KeyF1 {
			m.previousView = m.view
			m.view = ViewHelp
			return m, nil
		}
	}

	switch m.view {
	case ViewMain:
		return m.handleMainKeys(msg)
	case ViewSelectProject:
		return m.handleSelectProjectKeys(msg)
	case ViewNewTaskBranch:
		return m.handleNewTaskBranchKeys(msg)
	case ViewNewTaskBranchInput:
		return m.handleNewTaskBranchInputKeys(msg)
	case ViewNewTaskInput:
		return m.handleNewTaskInputKeys(msg)
	case ViewIntervene:
		return m.handleInterveneKeys(msg)
	case ViewInterveneInput:
		return m.handleInterveneInputKeys(msg)
	case ViewReview:
		return m.handleReviewKeys(msg)
	case ViewConfirmMerge:
		return m.handleConfirmMergeKeys(msg)
	case ViewConfirmKill:
		return m.handleConfirmKillKeys(msg)
	case ViewManageProjects:
		return m.handleManageProjectsKeys(msg)
	case ViewAddProjectName:
		return m.handleAddProjectNameKeys(msg)
	case ViewAddProjectPath:
		return m.handleAddProjectPathKeys(msg)
	case ViewAddProjectFastWT:
		return m.handleAddProjectFastWTKeys(msg)
	case ViewProjImporting:
		return m.handleProjImportingKeys(msg)
	case ViewEditProject:
		return m.handleEditProjectKeys(msg)
	case ViewConfirmRemoveProject:
		return m.handleConfirmRemoveProjectKeys(msg)
	case ViewConfirmKillSession:
		return m.handleConfirmKillSessionKeys(msg)
	case ViewAgentInfo:
		return m.handleAgentInfoKeys(msg)
	case ViewUpdate:
		return m.handleUpdateKeys(msg)
	case ViewHelp:
		return m.handleHelpKeys(msg)
	}
	return m, nil
}

func (m model) handleMainKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		if len(m.queueItems) == 0 {
			m.err = fmt.Errorf("no items in queue")
			return m, clearMessageCmd()
		}
		item := m.queueItems[len(m.queueItems)-1]
		switch item.Type {
		case queue.ItemTypeIdle, queue.ItemTypeQuestion:
			for _, a := range m.agents {
				if a.ID == item.AgentID {
					return m, m.quickRespondToAgentCmd(a)
				}
			}
		case queue.ItemTypePRReady:
			m.view = ViewReview
			m.selectedIndex = 0
			return m, nil
		}
	case "n":
		if len(m.projects) == 0 {
			m.err = fmt.Errorf("no projects registered. Press [p] to add one")
			return m, clearMessageCmd()
		}
		m.view = ViewSelectProject
		m.selectedIndex = 0
	case "k":
		m.view = ViewConfirmKill
		m.selectedIndex = 0
	case "i":
		m.view = ViewAgentInfo
		m.selectedIndex = 0
	case "K":
		m.view = ViewConfirmKillSession
	case "p":
		m.view = ViewManageProjects
		m.selectedIndex = 0
	case "u":
		m.view = ViewUpdate
		m.updateChecking = true
		m.updateAvailable = false
		m.updateVersion = ""
		m.updateDownloading = false
		m.updateComplete = false
		m.updateError = ""
		m.changelogEntries = nil
		m.changelogLoading = false
		m.selectedIndex = 0
		atomic.StoreInt64(m.downloadProgress, 0)
		return m, checkForUpdateCmd()
	}
	return m, nil
}

func (m model) handleSelectProjectKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewMain
		m.selectedProj = nil
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if m.selectedIndex < len(m.projects)-1 {
			m.selectedIndex++
		}
	case "enter":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.projects) {
			m.selectedProj = m.projects[m.selectedIndex]
			m.branchOptions = getLocalBranches(m.selectedProj.Path)
			m.branchFilter.SetValue("")
			m.filteredBranches = nil
			m.view = ViewNewTaskBranch
			m.selectedIndex = 0
			m.branchFilter.Focus()
			return m, textinput.Blink
		}
	}
	return m, nil
}

func (m model) handleNewTaskBranchKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	entries := m.branchEntries()
	totalItems := len(entries)

	switch msg.String() {
	case "esc":
		if m.branchFilter.Value() != "" {
			m.branchFilter.SetValue("")
			m.filteredBranches = nil
			m.selectedIndex = 0
			return m, nil
		}
		m.view = ViewSelectProject
		m.selectedIndex = 0
		return m, nil
	case "up":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
		return m, nil
	case "down":
		if m.selectedIndex < totalItems-1 {
			m.selectedIndex++
		}
		return m, nil
	case "enter":
		if m.selectedIndex >= 0 && m.selectedIndex < totalItems {
			entry := entries[m.selectedIndex]
			if entry.isManual {
				m.view = ViewNewTaskBranchInput
				m.branchInput.SetValue("")
				m.branchInput.Focus()
				return m, textinput.Blink
			}
			m.spawnBranch = entry.value
			m.view = ViewNewTaskInput
			m.taskInput.SetValue("")
			m.taskInput.SetHeight(5)
			m.selectedIndex = 0
			m.branchFilter.SetValue("")
			m.filteredBranches = nil
			cmd := m.taskInput.Focus()
			return m, cmd
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.branchFilter, cmd = m.branchFilter.Update(msg)
	m.fuzzyFilterBranches()
	m.selectedIndex = 0
	return m, cmd
}

func (m model) handleNewTaskBranchInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewNewTaskBranch
		m.branchInput.SetValue("")
		return m, nil
	case "enter":
		branch := m.branchInput.Value()
		if branch == "" {
			branch = "origin/master"
			if m.selectedProj != nil && m.selectedProj.DefaultBaseBranch != "" {
				branch = m.selectedProj.DefaultBaseBranch
			}
		}
		m.spawnBranch = branch
		m.view = ViewNewTaskInput
		m.taskInput.SetValue("")
		m.taskInput.SetHeight(5)
		m.selectedIndex = 0
		cmd := m.taskInput.Focus()
		return m, cmd
	}

	var cmd tea.Cmd
	m.branchInput, cmd = m.branchInput.Update(msg)
	return m, cmd
}

func (m model) handleNewTaskInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewNewTaskBranch
		m.selectedIndex = 0
		m.taskInput.SetValue("")
		m.taskInput.Blur()
		return m, nil
	case "enter":
		task := m.taskInput.Value()
		if task == "" {
			return m, nil
		}
		proj := m.selectedProj
		branch := m.spawnBranch
		m.view = ViewMain
		m.taskInput.SetValue("")
		m.selectedProj = nil
		m.spawnBranch = ""
		return m, m.spawnAgentCmd(task, proj, branch)
	}

	var cmd tea.Cmd
	m.taskInput, cmd = m.taskInput.Update(msg)
	return m, cmd
}

func (m model) handleInterveneKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := filterQueueByType(m.queueItems, queue.ItemTypeQuestion, queue.ItemTypeIdle)

	switch msg.String() {
	case "esc":
		m.view = ViewMain
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if m.selectedIndex < len(items)-1 {
			m.selectedIndex++
		}
	case "enter":
		if m.selectedIndex >= 0 && m.selectedIndex < len(items) {
			selected := items[m.selectedIndex]
			for _, a := range m.agents {
				if a.ID == selected.AgentID {
					m.interveneAgent = a
					m.view = ViewInterveneInput
					m.interveneInput.SetValue("")
					m.interveneInput.SetHeight(1)
					cmd := m.interveneInput.Focus()
					return m, cmd
				}
			}
		}
	}
	return m, nil
}

func (m model) handleReviewKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := filterQueueByType(m.queueItems, queue.ItemTypePRReady)

	findAgent := func() (*agent.Agent, *queue.QueueItem) {
		if m.selectedIndex < 0 || m.selectedIndex >= len(items) {
			return nil, nil
		}
		selected := items[m.selectedIndex]
		for _, a := range m.agents {
			if a.ID == selected.AgentID {
				return a, selected
			}
		}
		return nil, nil
	}

	switch msg.String() {
	case "esc":
		m.view = ViewMain
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if m.selectedIndex < len(items)-1 {
			m.selectedIndex++
		}
	case "a":
		if a, _ := findAgent(); a != nil {
			m.view = ViewMain
			m.agentStore.Update(a.ID, func(ag *agent.Agent) {
				ag.Status = agent.StatusCleaningUp
			})
			return m, m.acceptPRCmd(a)
		}
	case "c":
		if a, item := findAgent(); a != nil {
			m.view = ViewMain
			return m, m.commentPRCmd(a, item.Details)
		}
	case "r":
		if a, item := findAgent(); a != nil {
			m.view = ViewMain
			m.agentStore.Update(a.ID, func(ag *agent.Agent) {
				ag.Status = agent.StatusCleaningUp
			})
			return m, m.rejectPRCmd(a, item.Details)
		}
	case "b":
		if m.selectedIndex >= 0 && m.selectedIndex < len(items) {
			selected := items[m.selectedIndex]
			if selected.Details != "" {
				exec.Command("xdg-open", selected.Details).Start()
			}
		}
	}
	return m, nil
}

func (m model) handleConfirmMergeKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if m.selectedAgent != nil {
			agentToCleanup := m.selectedAgent
			m.selectedAgent = nil
			m.view = ViewMain
			return m, m.cleanupAgentCmd(agentToCleanup)
		}
	case "n", "esc":
		m.selectedAgent = nil
		m.view = ViewMain
	}
	return m, nil
}

func (m model) handleConfirmKillKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewMain
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if m.selectedIndex < len(m.agents)-1 {
			m.selectedIndex++
		}
	case "enter":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.agents) {
			selected := m.agents[m.selectedIndex]
			m.view = ViewMain
			return m, m.killAgentCmd(selected)
		}
	}
	return m, nil
}

func (m model) handleManageProjectsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewMain
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if m.selectedIndex < len(m.projects)-1 {
			m.selectedIndex++
		}
	case "a":
		m.view = ViewAddProjectName
		m.projectForm.reset()
		m.newProjectName = ""
		m.newProjectPath = ""
		m.projectForm.nameInput.Focus()
		return m, textinput.Blink
	case "enter":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.projects) {
			m.selectedProj = m.projects[m.selectedIndex]
			m.view = ViewEditProject
			m.editProjectForm.loadFromProject(m.selectedProj)
			return m, textinput.Blink
		}
	case "d":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.projects) {
			m.selectedProj = m.projects[m.selectedIndex]
			m.view = ViewConfirmRemoveProject
		}
	}
	return m, nil
}

func (m model) handleEditProjectKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editProjectForm.blurAll()
		m.view = ViewManageProjects
		m.selectedProj = nil
		return m, nil
	case "tab":
		m.editProjectForm.focusIndex = (m.editProjectForm.focusIndex + 1) % 4
		m.editProjectForm.focusCurrent()
		return m, textinput.Blink
	case "shift+tab":
		m.editProjectForm.focusIndex = (m.editProjectForm.focusIndex + 3) % 4
		m.editProjectForm.focusCurrent()
		return m, textinput.Blink
	case "enter":
		if m.selectedProj == nil {
			return m, nil
		}
		path := m.editProjectForm.pathInput.Value()
		baseBranch := m.editProjectForm.baseBranchInput.Value()
		ciWaitStr := m.editProjectForm.ciWaitInput.Value()
		ciWait := 0
		if ciWaitStr != "" {
			parsed, err := strconv.Atoi(ciWaitStr)
			if err != nil || parsed < 0 {
				m.err = fmt.Errorf("CI wait must be a positive number")
				return m, clearMessageCmd()
			}
			ciWait = parsed
		}
		fastWTStr := strings.ToLower(strings.TrimSpace(m.editProjectForm.fastWTInput.Value()))
		useFastWT := fastWTStr == "yes" || fastWTStr == "true" || fastWTStr == "y"
		projName := m.selectedProj.Name
		m.editProjectForm.blurAll()
		m.selectedProj = nil
		if useFastWT {
			m.view = ViewProjImporting
			m.projImporting = true
			m.projImportLines.reset()
			m.newProjectName = projName
			m.newProjectPath = path
		} else {
			m.view = ViewManageProjects
			m.projImporting = false
		}
		return m, m.updateProjectCmd(projName, path, baseBranch, ciWait, useFastWT)
	}

	var cmd tea.Cmd
	switch m.editProjectForm.focusIndex {
	case 0:
		m.editProjectForm.pathInput, cmd = m.editProjectForm.pathInput.Update(msg)
	case 1:
		m.editProjectForm.baseBranchInput, cmd = m.editProjectForm.baseBranchInput.Update(msg)
	case 2:
		m.editProjectForm.ciWaitInput, cmd = m.editProjectForm.ciWaitInput.Update(msg)
	case 3:
		m.editProjectForm.fastWTInput, cmd = m.editProjectForm.fastWTInput.Update(msg)
	}
	return m, cmd
}

func (m model) handleAddProjectNameKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewManageProjects
		m.projectForm.reset()
		return m, nil
	case "enter":
		name := m.projectForm.nameInput.Value()
		if name == "" {
			return m, nil
		}
		m.newProjectName = name
		m.view = ViewAddProjectPath
		// Auto-detect path
		if found := findProjectPath(name); found != "" {
			m.projectForm.pathInput.SetValue(found)
		} else {
			m.projectForm.pathInput.SetValue("")
		}
		m.projectForm.pathInput.Focus()
		return m, textinput.Blink
	}

	var cmd tea.Cmd
	m.projectForm.nameInput, cmd = m.projectForm.nameInput.Update(msg)
	return m, cmd
}

func (m model) handleAddProjectPathKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewAddProjectName
		m.projectForm.nameInput.SetValue(m.newProjectName)
		m.projectForm.nameInput.Focus()
		return m, textinput.Blink
	case "enter":
		path := m.projectForm.pathInput.Value()
		if path == "" {
			return m, nil
		}
		m.newProjectPath = path
		if project.IsProjDirectory(path) && project.IsProjInstalled() {
			m.view = ViewManageProjects
			return m, m.addProjectCmd(m.newProjectName, m.newProjectPath, true)
		}
		if !project.IsProjInstalled() {
			m.view = ViewManageProjects
			return m, m.addProjectCmd(m.newProjectName, m.newProjectPath, false)
		}
		m.view = ViewAddProjectFastWT
		return m, nil
	}

	var cmd tea.Cmd
	m.projectForm.pathInput, cmd = m.projectForm.pathInput.Update(msg)
	return m, cmd
}

func (m model) handleAddProjectFastWTKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewAddProjectPath
		m.projectForm.pathInput.Focus()
		return m, textinput.Blink
	case "y":
		m.view = ViewProjImporting
		m.projImporting = true
		m.projImportLines.reset()
		return m, m.addProjectCmd(m.newProjectName, m.newProjectPath, true)
	case "n":
		m.view = ViewManageProjects
		return m, m.addProjectCmd(m.newProjectName, m.newProjectPath, false)
	}
	return m, nil
}

func (m model) handleProjImportingKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.projImportCancel != nil {
			m.projImportCancel()
		}
		m.projImporting = false
		m.projImportLines.reset()
		m.projImportCancel = nil
		m.view = ViewManageProjects
	}
	return m, nil
}

func (m model) handleConfirmRemoveProjectKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if m.selectedProj != nil {
			projToRemove := m.selectedProj
			m.selectedProj = nil
			m.view = ViewManageProjects
			m.selectedIndex = 0
			return m, m.removeProjectCmd(projToRemove.Name)
		}
	case "n", "esc":
		m.selectedProj = nil
		m.view = ViewManageProjects
	}
	return m, nil
}

func (m model) handleConfirmKillSessionKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		return m, m.killSessionCmd()
	case "n", "esc":
		m.view = ViewMain
	}
	return m, nil
}

func (m model) handleAgentInfoKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewMain
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down":
		if m.selectedIndex < len(m.agents)-1 {
			m.selectedIndex++
		}
	case "j":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.agents) {
			selected := m.agents[m.selectedIndex]
			m.view = ViewMain
			return m, m.jumpToAgentCmd(selected)
		}
	}
	return m, nil
}

func (m model) handleUpdateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.updateChecking || m.updateDownloading {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		if !m.updateComplete {
			m.view = ViewMain
		}
	case "r":
		if m.updateComplete {
			m.restartRequested = true
			return m, tea.Quit
		}
	case "up", "k":
		if len(m.changelogEntries) > 0 && m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if len(m.changelogEntries) > 0 && m.selectedIndex < len(m.changelogEntries)-1 {
			m.selectedIndex++
		}
	}
	return m, nil
}

func (m model) handleInterveneInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewIntervene
		m.interveneAgent = nil
		m.interveneInput.SetValue("")
		return m, nil
	case "enter":
		text := m.interveneInput.Value()
		if text == "" {
			return m, nil
		}
		a := m.interveneAgent
		m.interveneInput.SetValue("")
		return m, m.sendKeysToAgentCmd(a, text)
	}

	var cmd tea.Cmd
	m.interveneInput, cmd = m.interveneInput.Update(msg)
	return m, cmd
}

func (m model) handleHelpKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = m.previousView
	}
	return m, nil
}

func (m model) updateWindowNames() {
	for _, a := range m.agents {
		if a.TmuxWindow == "" {
			continue
		}
		var name string
		short := a.ID[:8]
		switch a.Status {
		case agent.StatusSpawning:
			name = spinner(m.spinnerFrame) + " " + short
		case agent.StatusRunning:
			name = spinner(m.spinnerFrame) + " " + short
		case agent.StatusCleaningUp:
			name = spinner(m.spinnerFrame) + " " + short
		case agent.StatusKilling:
			name = spinner(m.spinnerFrame) + " " + short
		case agent.StatusReady:
			name = "● " + short
		case agent.StatusWaitingCI:
			name = "⏳ " + short
		default:
			name = short
		}
		if m.prevWindowNames[a.TmuxWindow] != name {
			m.prevWindowNames[a.TmuxWindow] = name
			m.tmuxManager.RenameWindow(a.TmuxWindow, name)
		}
	}
}

func (m model) killSessionCmd() tea.Cmd {
	return func() tea.Msg {
		exePath, err := os.Executable()
		if err != nil {
			return errMsg{fmt.Errorf("failed to get executable: %w", err)}
		}
		cmd := exec.Command(exePath, "kill-session", m.sessionID)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return errMsg{fmt.Errorf("kill-session failed: %s: %w", string(output), err)}
		}
		return nil
	}
}

func (m model) detachCmd() tea.Cmd {
	return func() tea.Msg {
		exec.Command("tmux", "detach-client").Run()
		return nil
	}
}

func (m model) addProjectCmd(name, path string, useFastWT bool) tea.Cmd {
	buf := m.projImportLines
	return func() tea.Msg {
		if useFastWT && !project.IsProjDirectory(path) {
			projDir, err := project.ProjImport(path, buf.addLine)
			if err != nil {
				return errMsg{err}
			}
			path = projDir
		}
		p := &project.Project{
			Name:             name,
			Path:             path,
			UseFastWorktrees: useFastWT,
		}
		if err := m.projectStore.Add(p); err != nil {
			return errMsg{err}
		}
		return successMsg{fmt.Sprintf("Added project '%s'", name)}
	}
}

func (m model) updateProjectCmd(name, path, baseBranch string, ciWait int, useFastWT bool) tea.Cmd {
	buf := m.projImportLines
	return func() tea.Msg {
		if useFastWT && path != "" && !project.IsProjDirectory(path) {
			projDir, err := project.ProjImport(path, buf.addLine)
			if err != nil {
				return errMsg{err}
			}
			path = projDir
		}
		err := m.projectStore.Update(name, func(p *project.Project) {
			if path != "" {
				p.Path = path
			}
			p.DefaultBaseBranch = baseBranch
			p.CIWaitMinutes = ciWait
			p.UseFastWorktrees = useFastWT
		})
		if err != nil {
			return errMsg{err}
		}
		return successMsg{fmt.Sprintf("Updated project '%s'", name)}
	}
}

func (m model) removeProjectCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if err := m.projectStore.Remove(name); err != nil {
			return errMsg{err}
		}
		return successMsg{fmt.Sprintf("Removed project '%s'", name)}
	}
}

func (m model) spawnAgentCmd(task string, proj *project.Project, branch string) tea.Cmd {
	return func() tea.Msg {
		exePath, err := os.Executable()
		if err != nil {
			return errMsg{fmt.Errorf("failed to get executable: %w", err)}
		}
		cmd := exec.Command(exePath, "spawn", task, "--project", proj.Name, "--branch", branch)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return errMsg{fmt.Errorf("spawn failed: %s: %w", string(output), err)}
		}
		return spawnStartedMsg{}
	}
}

func (m model) jumpToAgentCmd(a *agent.Agent) tea.Cmd {
	return func() tea.Msg {
		if err := m.tmuxManager.SelectWindow(a.TmuxWindow); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m model) quickRespondToAgentCmd(a *agent.Agent) tea.Cmd {
	return func() tea.Msg {
		m.queueManager.RemoveByAgent(a.ID)
		if err := m.tmuxManager.SelectWindow(a.TmuxWindow); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m model) sendKeysToAgentCmd(a *agent.Agent, text string) tea.Cmd {
	return func() tea.Msg {
		if err := m.tmuxManager.SendKeys(a.TmuxWindow, text); err != nil {
			return errMsg{err}
		}
		m.queueManager.RemoveByAgent(a.ID)
		return successMsg{fmt.Sprintf("Sent message to agent %s", a.ID)}
	}
}

func (m model) cleanupAgentCmd(a *agent.Agent) tea.Cmd {
	agentID := a.ID
	m.agentStore.Update(agentID, func(ag *agent.Agent) {
		ag.Status = agent.StatusCleaningUp
	})
	m.queueManager.RemoveByAgent(agentID)
	return func() tea.Msg {
		go func() {
			exePath, _ := os.Executable()
			cmd := exec.Command(exePath, "cleanup", agentID)
			cmd.Run()
		}()
		return successMsg{fmt.Sprintf("Cleaning up agent %s...", agentID)}
	}
}

func (m model) acceptPRCmd(a *agent.Agent) tea.Cmd {
	agentID := a.ID
	return func() tea.Msg {
		m.queueManager.RemoveByAgent(agentID)

		go func() {
			exePath, _ := os.Executable()
			exec.Command(exePath, "cleanup", agentID).Run()
		}()

		return successMsg{fmt.Sprintf("Accepted PR, cleaning up agent %s", agentID)}
	}
}

func (m model) commentPRCmd(a *agent.Agent, prURL string) tea.Cmd {
	agentID := a.ID
	worktreePath := a.WorktreePath
	tmuxWindow := a.TmuxWindow
	return func() tea.Msg {
		m.agentStore.Update(agentID, func(ag *agent.Agent) {
			ag.Status = agent.StatusRunning
		})

		m.queueManager.RemoveByAgent(agentID)

		scriptPath, err := writeReviewScript(agentID, worktreePath, prURL)
		if err != nil {
			return errMsg{fmt.Errorf("failed to write review script: %w", err)}
		}

		// Kill old window and create a fresh one instead of respawning the
		// dead pane. Respawn-pane inherits stale terminal state (alternate
		// screen, raw mode) from the previous Claude Code process, which
		// causes the new process to hang waiting for input.
		m.tmuxManager.KillWindow(tmuxWindow)
		newWindowID, err := m.tmuxManager.CreateWindow(worktreePath, "bash "+scriptPath, agentID[:8])
		if err != nil {
			return errMsg{fmt.Errorf("failed to create review window: %w", err)}
		}

		m.agentStore.Update(agentID, func(ag *agent.Agent) {
			ag.TmuxWindow = newWindowID
		})

		return successMsg{fmt.Sprintf("Agent %s resumed to address PR comments", agentID)}
	}
}

func (m model) rejectPRCmd(a *agent.Agent, prURL string) tea.Cmd {
	agentID := a.ID
	return func() tea.Msg {
		m.queueManager.RemoveByAgent(agentID)

		closeCmd := exec.Command("gh", "pr", "close", prURL, "--delete-branch")
		if output, err := closeCmd.CombinedOutput(); err != nil {
			return errMsg{fmt.Errorf("close PR failed: %s: %w", string(output), err)}
		}

		go func() {
			exePath, _ := os.Executable()
			exec.Command(exePath, "cleanup", agentID).Run()
		}()

		return successMsg{fmt.Sprintf("Rejected PR, cleaning up agent %s", agentID)}
	}
}

func writeReviewScript(agentID, worktreePath, prURL string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+"-review.sh")

	script := fmt.Sprintf(`#!/bin/bash
set -e

AGENT_ID="%s"

cd "%s"

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
RESET="\033[0m"
echo -e "${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Reviewing PR comments...${RESET}"
echo ""

export CCMUX_AGENT_ID="$AGENT_ID"
unset CLAUDECODE

claude --continue --dangerously-skip-permissions \
  "The GitHub PR at %s has received comments. Please review ALL comments — both conversation-level comments (gh pr view %s --comments) AND inline review comments (gh api repos/{owner}/{repo}/pulls/{number}/comments). Make sure to check both types so you don't miss any feedback. Address all the feedback. Commit and push your changes. Then check CI status with: gh pr checks %s -- If all checks passed, run: ccmux pr-ready %s -- If checks failed or are still pending, run: ccmux ci-wait %s"

ccmux agent-stopped "$AGENT_ID"
`, agentID, worktreePath, prURL, prURL, prURL, prURL, prURL)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

func writeCICheckScript(agentID, worktreePath, prURL string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+"-ci-check.sh")

	script := fmt.Sprintf(`#!/bin/bash
set -e

AGENT_ID="%s"

cd "%s"

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
RESET="\033[0m"
echo -e "${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Checking CI status...${RESET}"
echo ""

export CCMUX_AGENT_ID="$AGENT_ID"
unset CLAUDECODE

claude --continue --dangerously-skip-permissions \
  "Check the CI status for the PR at %s. Run: gh pr checks %s -- Then: If ALL checks passed, run: ccmux pr-ready %s -- If any checks FAILED, investigate and fix the failures, commit and push, then run: ccmux ci-wait %s -- If checks are still pending/running, run: ccmux ci-wait %s"

ccmux agent-stopped "$AGENT_ID"
`, agentID, worktreePath, prURL, prURL, prURL, prURL, prURL)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

func (m model) resumeCICheckCmd(a *agent.Agent) tea.Cmd {
	agentID := a.ID
	worktreePath := a.WorktreePath
	tmuxWindow := a.TmuxWindow
	prURL := a.PRURL
	return func() tea.Msg {
		m.agentStore.Update(agentID, func(ag *agent.Agent) {
			ag.Status = agent.StatusRunning
		})

		scriptPath, err := writeCICheckScript(agentID, worktreePath, prURL)
		if err != nil {
			return errMsg{fmt.Errorf("failed to write CI check script: %w", err)}
		}

		m.tmuxManager.KillWindow(tmuxWindow)
		newWindowID, err := m.tmuxManager.CreateWindow(worktreePath, "bash "+scriptPath, agentID[:8])
		if err != nil {
			return errMsg{fmt.Errorf("failed to create CI check window: %w", err)}
		}

		m.agentStore.Update(agentID, func(ag *agent.Agent) {
			ag.TmuxWindow = newWindowID
		})

		return successMsg{fmt.Sprintf("Agent %s resumed to check CI", agentID)}
	}
}

func (m model) killAgentCmd(a *agent.Agent) tea.Cmd {
	agentID := a.ID
	m.agentStore.Update(agentID, func(ag *agent.Agent) {
		ag.Status = agent.StatusKilling
	})
	m.queueManager.RemoveByAgent(agentID)
	return func() tea.Msg {
		exePath, err := os.Executable()
		if err != nil {
			return errMsg{fmt.Errorf("failed to get executable: %w", err)}
		}
		cmd := exec.Command(exePath, "kill", agentID)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return errMsg{fmt.Errorf("kill failed: %s: %w", string(output), err)}
		}
		return successMsg{fmt.Sprintf("Killed agent %s", agentID)}
	}
}

func (m model) View() string {
	var content string
	switch m.view {
	case ViewMain:
		content = renderMainView(m)
	case ViewSelectProject:
		content = renderSelectProjectView(m)
	case ViewNewTaskBranch:
		content = renderNewTaskBranchView(m)
	case ViewNewTaskBranchInput:
		content = renderNewTaskBranchInputView(m)
	case ViewNewTaskInput:
		content = renderNewTaskInputView(m)
	case ViewIntervene:
		content = renderInterveneView(m)
	case ViewInterveneInput:
		content = renderInterveneInputView(m)
	case ViewReview:
		content = renderReviewView(m)
	case ViewConfirmMerge:
		content = renderConfirmMergeView(m)
	case ViewConfirmKill:
		content = renderConfirmKillView(m)
	case ViewManageProjects:
		content = renderManageProjectsView(m)
	case ViewAddProjectName:
		content = renderAddProjectNameView(m)
	case ViewAddProjectPath:
		content = renderAddProjectPathView(m)
	case ViewAddProjectFastWT:
		content = renderAddProjectFastWTView(m)
	case ViewProjImporting:
		content = renderProjImportingView(m)
	case ViewEditProject:
		content = renderEditProjectView(m)
	case ViewConfirmRemoveProject:
		content = renderConfirmRemoveProjectView(m)
	case ViewConfirmKillSession:
		content = renderConfirmKillSessionView(m)
	case ViewAgentInfo:
		content = renderAgentInfoView(m)
	case ViewUpdate:
		content = renderUpdateView(m)
	case ViewHelp:
		content = renderHelpView(m)
	default:
		content = renderMainView(m)
	}

	return content
}

func Run(agentStore *agent.Store, queueManager *queue.Queue, projectStore *project.Store, tmuxManager *tmux.Manager, sessionID string) (bool, error) {
	m := initialModel(agentStore, queueManager, projectStore, tmuxManager, sessionID)
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return false, err
	}
	if fm, ok := finalModel.(model); ok && fm.restartRequested {
		return true, nil
	}
	return false, nil
}
