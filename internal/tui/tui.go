// Package tui implements the orchestrator terminal UI.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	view          ViewState
	agents        []*agent.Agent
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
	branchAllEntries      []branchEntry
	branchFilteredEntries []branchEntry
	branchSearchIndex     int
	spawnBranch           string

	// Project form inputs
	projectForm    projectFormModel
	newProjectName string
	newProjectPath string

	// Intervention input
	interveneInput textarea.Model
	interveneAgent *agent.Agent

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
	spinnerFrame  int
	marqueeOffset int

	// Periodic update check
	lastUpdateCheck time.Time

	agentStore   *agent.Store
	queueManager *queue.Queue
	projectStore *project.Store
	tmuxManager  *tmux.Manager
	sessionID    string
}

type branchEntry struct {
	tag            string
	name           string
	matchedIndexes []int
}

type projectFormModel struct {
	nameInput  textinput.Model
	pathInput  textinput.Model
	focusIndex int // 0=name, 1=path
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

func getAllBranches(repoPath string) []branchEntry {
	var entries []branchEntry

	localCmd := exec.Command("git", "-C", repoPath, "branch", "--format=%(refname:short)")
	if localOutput, err := localCmd.Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(localOutput)), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				entries = append(entries, branchEntry{tag: "(local)", name: line})
			}
		}
	}

	remoteCmd := exec.Command("git", "-C", repoPath, "branch", "-r", "--format=%(refname:short)")
	if remoteOutput, err := remoteCmd.Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(remoteOutput)), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.Contains(line, "HEAD") {
				entries = append(entries, branchEntry{tag: "(remote)", name: line})
			}
		}
	}

	return entries
}

type tickMsg time.Time
type spinnerTickMsg time.Time
type refreshMsg struct {
	agents     []*agent.Agent
	queueItems []*queue.QueueItem
	projects   []*project.Project
}
type errMsg struct{ err error }
type successMsg struct{ msg string }
type clearMessageMsg struct{}
type clearCtrlCMsg struct{}
type spawnStartedMsg struct{}
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

func newAutoGrowTextarea(placeholder string, width int) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.EndOfBufferCharacter = ' '
	ta.SetWidth(width)
	ta.SetHeight(1)
	ta.CharLimit = 0
	ta.KeyMap.InsertNewline.SetKeys("shift+enter")
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	return ta
}

func autoResizeTextarea(ta *textarea.Model, maxHeight int) {
	lines := ta.LineCount()
	if lines < 1 {
		lines = 1
	}
	if lines > maxHeight {
		lines = maxHeight
	}
	ta.SetHeight(lines)
}

func initialModel(agentStore *agent.Store, queueManager *queue.Queue, projectStore *project.Store, tmuxManager *tmux.Manager, sessionID string) model {
	taskInput := newAutoGrowTextarea("Describe the task...", 60)
	branchInput := textinput.New()
	branchInput.Placeholder = "origin/master"
	branchInput.Width = 50
	branchInput.CharLimit = 100

	interveneInput := newAutoGrowTextarea("Type message to send to agent...", 60)

	return model{
		view:            ViewMain,
		taskInput:       taskInput,
		branchInput:     branchInput,
		interveneInput:  interveneInput,
		projectForm:     newProjectForm(),
		agentStore:      agentStore,
		queueManager:    queueManager,
		projectStore:    projectStore,
		tmuxManager:     tmuxManager,
		sessionID:       sessionID,
		lastUpdateCheck: time.Now(),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		spinnerTickCmd(),
		m.refreshCmd(),
		checkForUpdateCmd(),
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

		queueItems, _ := m.queueManager.List()

		idleItemByAgent := make(map[string]*queue.QueueItem)
		for _, item := range queueItems {
			if item.Type == queue.ItemTypeIdle {
				idleItemByAgent[item.AgentID] = item
			}
		}

		changed := false
		for _, a := range agents {
			if a.Status != agent.StatusRunning || a.TmuxWindow == "" {
				continue
			}

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
		}

		if changed {
			queueItems, _ = m.queueManager.List()
		}

		return refreshMsg{agents: agents, queueItems: queueItems, projects: projects}
	}
}

func clearMessageCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
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

func fetchChangelogCmd(currentVersion, latestVersion string) tea.Cmd {
	return func() tea.Msg {
		entries, err := updater.FetchChangelog(currentVersion, latestVersion)
		return changelogFetchedMsg{entries: entries, err: err}
	}
}

func downloadUpdateCmd(targetVersion string) tea.Cmd {
	return func() tea.Msg {
		err := updater.DownloadUpdate(targetVersion)
		return updateCompleteMsg{err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tickMsg:
		cmds := []tea.Cmd{tickCmd(), m.refreshCmd()}
		if time.Since(m.lastUpdateCheck) > 5*time.Minute {
			m.lastUpdateCheck = time.Now()
			cmds = append(cmds, checkForUpdateCmd())
		}
		return m, tea.Batch(cmds...)

	case spinnerTickMsg:
		hasAnimatedAgents := false
		for _, a := range m.agents {
			if a.Status == agent.StatusSpawning || a.Status == agent.StatusRunning || a.Status == agent.StatusKilling || a.Status == agent.StatusCleaningUp {
				hasAnimatedAgents = true
				break
			}
		}
		if hasAnimatedAgents {
			m.spinnerFrame = (m.spinnerFrame + 1) % SpinnerFrameCount
			m.marqueeOffset++
			m.updateWindowNames()
		}
		return m, spinnerTickCmd()

	case refreshMsg:
		m.agents = msg.agents
		m.queueItems = msg.queueItems
		m.projects = msg.projects
		return m, nil

	case spawnStartedMsg:
		return m, nil

	case updateCheckResultMsg:
		m.updateChecking = false
		m.lastUpdateCheck = time.Now()
		if msg.err != nil {
			m.updateError = fmt.Sprintf("Update check failed: %s", msg.err.Error())
			return m, nil
		}
		m.updateVersion = msg.version
		m.updateAvailable = msg.available
		if msg.available {
			m.changelogLoading = true
			return m, fetchChangelogCmd(version.Version, msg.version)
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
		return m, tea.Batch(clearMessageCmd(), m.refreshCmd())

	case successMsg:
		m.err = nil
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
	if m.view == ViewNewTaskBranchInput {
		var cmd tea.Cmd
		m.branchInput, cmd = m.branchInput.Update(msg)
		cmds = append(cmds, cmd)
	}
	if m.view == ViewNewTaskInput {
		var cmd tea.Cmd
		m.taskInput, cmd = m.taskInput.Update(msg)
		autoResizeTextarea(&m.taskInput, 5)
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
	if m.view == ViewInterveneInput {
		var cmd tea.Cmd
		m.interveneInput, cmd = m.interveneInput.Update(msg)
		autoResizeTextarea(&m.interveneInput, 5)
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
	case ViewConfirmRemoveProject:
		return m.handleConfirmRemoveProjectKeys(msg)
	case ViewConfirmKillSession:
		return m.handleConfirmKillSessionKeys(msg)
	case ViewJumpToAgent:
		return m.handleJumpToAgentKeys(msg)
	case ViewUpdate:
		return m.handleUpdateKeys(msg)
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
	case "j":
		m.view = ViewJumpToAgent
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
			m.branchAllEntries = getAllBranches(m.selectedProj.Path)
			m.view = ViewNewTaskBranch
			m.selectedIndex = 0
			return m, nil
		}
	}
	return m, nil
}

func (m model) handleNewTaskBranchKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	totalItems := 2 + len(m.branchOptions)

	switch msg.String() {
	case "esc":
		m.view = ViewSelectProject
		m.selectedIndex = 0
		return m, nil
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if m.selectedIndex < totalItems-1 {
			m.selectedIndex++
		}
	case "enter":
		if m.selectedIndex == 0 {
			m.spawnBranch = "origin/master"
			m.view = ViewNewTaskInput
			m.taskInput.SetValue("")
			m.taskInput.SetHeight(1)
			m.selectedIndex = 0
			cmd := m.taskInput.Focus()
			return m, cmd
		}
		if m.selectedIndex == 1 {
			m.view = ViewNewTaskBranchInput
			m.branchInput.SetValue("")
			m.branchInput.Focus()
			m.branchFilteredEntries = make([]branchEntry, len(m.branchAllEntries))
			copy(m.branchFilteredEntries, m.branchAllEntries)
			m.branchSearchIndex = 0
			return m, textinput.Blink
		}
		branch := m.branchOptions[m.selectedIndex-2]
		m.spawnBranch = branch
		m.view = ViewNewTaskInput
		m.taskInput.SetValue("")
		m.taskInput.SetHeight(1)
		m.selectedIndex = 0
		cmd := m.taskInput.Focus()
		return m, cmd
	}
	return m, nil
}

func (m model) handleNewTaskBranchInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewNewTaskBranch
		m.branchInput.SetValue("")
		return m, nil
	case "up":
		if m.branchSearchIndex > 0 {
			m.branchSearchIndex--
		}
		return m, nil
	case "down":
		if m.branchSearchIndex < len(m.branchFilteredEntries)-1 {
			m.branchSearchIndex++
		}
		return m, nil
	case "enter":
		if len(m.branchFilteredEntries) > 0 && m.branchSearchIndex >= 0 && m.branchSearchIndex < len(m.branchFilteredEntries) {
			m.spawnBranch = m.branchFilteredEntries[m.branchSearchIndex].name
		} else {
			branch := m.branchInput.Value()
			if branch == "" {
				branch = "origin/master"
			}
			m.spawnBranch = branch
		}
		m.view = ViewNewTaskInput
		m.taskInput.SetValue("")
		m.taskInput.SetHeight(1)
		m.selectedIndex = 0
		cmd := m.taskInput.Focus()
		return m, cmd
	}

	var cmd tea.Cmd
	m.branchInput, cmd = m.branchInput.Update(msg)
	m.updateBranchFilter()
	return m, cmd
}

func (m *model) updateBranchFilter() {
	query := m.branchInput.Value()
	if query == "" {
		m.branchFilteredEntries = make([]branchEntry, len(m.branchAllEntries))
		copy(m.branchFilteredEntries, m.branchAllEntries)
		m.branchSearchIndex = 0
		return
	}

	names := make([]string, len(m.branchAllEntries))
	for i, e := range m.branchAllEntries {
		names[i] = e.name
	}
	matches := fuzzy.Find(query, names)
	m.branchFilteredEntries = make([]branchEntry, len(matches))
	for i, match := range matches {
		entry := m.branchAllEntries[match.Index]
		entry.matchedIndexes = match.MatchedIndexes
		m.branchFilteredEntries[i] = entry
	}
	if m.branchSearchIndex >= len(m.branchFilteredEntries) {
		m.branchSearchIndex = 0
	}
}

func (m model) handleNewTaskInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewNewTaskBranch
		m.selectedIndex = 0
		m.taskInput.SetValue("")
		m.taskInput.SetHeight(1)
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
		m.taskInput.SetHeight(1)
		m.selectedProj = nil
		m.spawnBranch = ""
		return m, m.spawnAgentCmd(task, proj, branch)
	}

	var cmd tea.Cmd
	m.taskInput, cmd = m.taskInput.Update(msg)
	autoResizeTextarea(&m.taskInput, 5)
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
	case "d":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.projects) {
			m.selectedProj = m.projects[m.selectedIndex]
			m.view = ViewConfirmRemoveProject
		}
	}
	return m, nil
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
		m.view = ViewManageProjects
		return m, m.addProjectCmd(m.newProjectName, path)
	}

	var cmd tea.Cmd
	m.projectForm.pathInput, cmd = m.projectForm.pathInput.Update(msg)
	return m, cmd
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

func (m model) handleJumpToAgentKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "esc", "n":
		m.view = ViewMain
	case "y":
		if m.updateAvailable && !m.updateComplete {
			m.updateDownloading = true
			m.updateError = ""
			return m, downloadUpdateCmd(m.updateVersion)
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
		m.interveneInput.SetHeight(1)
		return m, nil
	case "enter":
		text := m.interveneInput.Value()
		if text == "" {
			return m, nil
		}
		a := m.interveneAgent
		m.interveneInput.SetValue("")
		m.interveneInput.SetHeight(1)
		return m, m.sendKeysToAgentCmd(a, text)
	}

	var cmd tea.Cmd
	m.interveneInput, cmd = m.interveneInput.Update(msg)
	autoResizeTextarea(&m.interveneInput, 5)
	return m, cmd
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
		default:
			name = short
		}
		m.tmuxManager.RenameWindow(a.TmuxWindow, name)
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

func (m model) addProjectCmd(name, path string) tea.Cmd {
	return func() tea.Msg {
		p := &project.Project{
			Name: name,
			Path: path,
		}
		if err := m.projectStore.Add(p); err != nil {
			return errMsg{err}
		}
		return successMsg{fmt.Sprintf("Added project '%s'", name)}
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
export CLAUDE_CODE_USE_BEDROCK=1
export AWS_REGION=us-west-2
unset CLAUDECODE

claude --continue --permission-mode dontAsk \
  "The GitHub PR at %s has received review comments. Please review the comments with: gh pr view %s --comments, then address all the feedback. Commit and push your changes, and then run: ccmux pr-ready %s"

ccmux agent-stopped "$AGENT_ID"
`, agentID, worktreePath, prURL, prURL, prURL)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

func (m model) killAgentCmd(a *agent.Agent) tea.Cmd {
	agentID := a.ID
	m.agentStore.Update(agentID, func(ag *agent.Agent) {
		ag.Status = agent.StatusKilling
	})
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
	case ViewConfirmRemoveProject:
		content = renderConfirmRemoveProjectView(m)
	case ViewConfirmKillSession:
		content = renderConfirmKillSessionView(m)
	case ViewJumpToAgent:
		content = renderJumpToAgentView(m)
	case ViewUpdate:
		content = renderUpdateView(m)
	default:
		content = renderMainView(m)
	}

	// Add Ctrl+C indicator at bottom
	if m.ctrlCPressed {
		content += "\n\n" + renderCtrlCIndicator(m.ctrlCPressed)
	}

	return content
}

func Run(agentStore *agent.Store, queueManager *queue.Queue, projectStore *project.Store, tmuxManager *tmux.Manager, sessionID string) error {
	m := initialModel(agentStore, queueManager, projectStore, tmuxManager, sessionID)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
