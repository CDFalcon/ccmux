// Package tui implements the orchestrator terminal UI.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	"github.com/CDFalcon/ccmux/internal/repo"
	"github.com/CDFalcon/ccmux/internal/prompt"
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
	repos         []*repo.Repo
	selectedIndex int
	selectedAgent *agent.Agent
	selectedRepo  *repo.Repo
	err           error

	// Task spawn inputs
	taskInput             textarea.Model
	branchInput           textinput.Model
	branchOptions         []string
	branchFilter          textinput.Model
	filteredBranches      []string
	spawnBranch           string
	spawnTask             string
	worktreeNameInput     textinput.Model
	spawnPromptEnabled    map[string]bool
	spawnFilteredPrompts  []*prompt.Prompt

	// Repo form inputs
	repoForm        repoFormModel
	editRepoForm    editRepoFormModel
	newRepoName     string
	newRepoPath      string

	// Prompt form inputs
	prompts              []*prompt.Prompt
	promptForm           promptFormModel
	editPromptForm       editPromptFormModel
	newPromptName        string
	newPromptIsDefault   bool
	selectedPrompt       *prompt.Prompt
	promptRepoEnabled map[string]bool
	promptRepoIndex   int

	// Intervention input
	interveneInput textarea.Model
	interveneAgent *agent.Agent

	// Repo setup state (per-repo import buffers)
	projSetupBuffers map[string]*projImportBuffer
	projSetupName    string

	// Update state
	updateChecking    bool
	updateAvailable   bool
	updateVersion     string
	updateDownloading bool
	updateComplete    bool
	updateError       string
	changelogEntries  []updater.ChangelogEntry
	changelogLoading  bool

	// Kill agent confirmation
	confirmKillAgent *agent.Agent

	// Ctrl+C confirmation
	ctrlCPressed bool

	// Animation state
	spinnerFrame    int
	marqueeOffset   int
	prevWindowNames map[string]string

	// CI check tracking
	ciLastChecked    map[string]time.Time
	ciChecking       map[string]bool
	ciCheckProgress  map[string]ciProgress

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
	repoStore *repo.Store
	promptStore  *prompt.Store
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

type repoFormModel struct {
	nameInput  textinput.Model
	pathInput  textinput.Model
	focusIndex int // 0=name, 1=path
}

type editRepoFormModel struct {
	pathInput       textinput.Model
	baseBranchInput textinput.Model
	fastWTInput     textinput.Model
	focusIndex      int // 0=path, 1=baseBranch, 2=fastWT
}

type promptFormModel struct {
	nameInput    textinput.Model
	contentInput textarea.Model
}

type editPromptFormModel struct {
	nameInput    textinput.Model
	contentInput textarea.Model
	defaultInput textinput.Model
	focusIndex   int
}

func newPromptForm() promptFormModel {
	nameInput := textinput.New()
	nameInput.Placeholder = "my-prompt"
	nameInput.Width = 50
	nameInput.CharLimit = 100

	contentInput := newFixedTextarea("Enter prompt content...", 60)

	return promptFormModel{
		nameInput:    nameInput,
		contentInput: contentInput,
	}
}

func (pf *promptFormModel) reset() {
	pf.nameInput.SetValue("")
	pf.contentInput.SetValue("")
	pf.nameInput.Blur()
	pf.contentInput.Blur()
	pf.nameInput.Focus()
}

func newEditPromptForm() editPromptFormModel {
	nameInput := textinput.New()
	nameInput.Placeholder = "prompt name"
	nameInput.Width = 50
	nameInput.CharLimit = 100

	contentInput := newFixedTextarea("Prompt content...", 60)

	defaultInput := textinput.New()
	defaultInput.Placeholder = "no"
	defaultInput.Width = 10
	defaultInput.CharLimit = 5

	return editPromptFormModel{
		nameInput:    nameInput,
		contentInput: contentInput,
		defaultInput: defaultInput,
		focusIndex:   0,
	}
}

func (ef *editPromptFormModel) blurAll() {
	ef.nameInput.Blur()
	ef.contentInput.Blur()
	ef.defaultInput.Blur()
}

func (ef *editPromptFormModel) focusCurrent() {
	ef.blurAll()
	switch ef.focusIndex {
	case 0:
		ef.nameInput.Focus()
	case 1:
		ef.contentInput.Focus()
	case 2:
		ef.defaultInput.Focus()
	case 3:
		// repos field - no text input to focus
	}
}

func (ef *editPromptFormModel) loadFromPrompt(p *prompt.Prompt) {
	ef.nameInput.SetValue(p.Name)
	ef.contentInput.SetValue(p.Content)
	if p.IsDefault {
		ef.defaultInput.SetValue("yes")
	} else {
		ef.defaultInput.SetValue("")
	}
	ef.focusIndex = 0
	ef.focusCurrent()
}

func promptRepoEnabledFromPrompt(p *prompt.Prompt) map[string]bool {
	enabled := make(map[string]bool)
	for _, name := range p.RepoNames {
		enabled[name] = true
	}
	return enabled
}

func newRepoForm() repoFormModel {
	nameInput := textinput.New()
	nameInput.Placeholder = "my-repo"
	nameInput.Width = 50
	nameInput.CharLimit = 50

	pathInput := textinput.New()
	pathInput.Placeholder = "/home/user/repos/my-repo"
	pathInput.Width = 50
	pathInput.CharLimit = 200

	return repoFormModel{
		nameInput:  nameInput,
		pathInput:  pathInput,
		focusIndex: 0,
	}
}

func (pf *repoFormModel) blurAll() {
	pf.nameInput.Blur()
	pf.pathInput.Blur()
}

func (pf *repoFormModel) reset() {
	pf.nameInput.SetValue("")
	pf.pathInput.SetValue("")
	pf.focusIndex = 0
	pf.blurAll()
	pf.nameInput.Focus()
}

func newEditRepoForm() editRepoFormModel {
	pathInput := textinput.New()
	pathInput.Placeholder = "/home/user/repos/my-repo"
	pathInput.Width = 50
	pathInput.CharLimit = 200

	baseBranchInput := textinput.New()
	baseBranchInput.Placeholder = "origin/master"
	baseBranchInput.Width = 50
	baseBranchInput.CharLimit = 100

	fastWTInput := textinput.New()
	fastWTInput.Placeholder = "no"
	fastWTInput.Width = 10
	fastWTInput.CharLimit = 5

	return editRepoFormModel{
		pathInput:       pathInput,
		baseBranchInput: baseBranchInput,
		fastWTInput:     fastWTInput,
		focusIndex:      0,
	}
}

func (ef *editRepoFormModel) blurAll() {
	ef.pathInput.Blur()
	ef.baseBranchInput.Blur()
	ef.fastWTInput.Blur()
}

func (ef *editRepoFormModel) focusCurrent() {
	ef.blurAll()
	switch ef.focusIndex {
	case 0:
		ef.pathInput.Focus()
	case 1:
		ef.baseBranchInput.Focus()
	case 2:
		ef.fastWTInput.Focus()
	}
}

func (ef *editRepoFormModel) loadFromRepo(p *repo.Repo) {
	ef.pathInput.SetValue(p.Path)
	ef.baseBranchInput.SetValue(p.DefaultBaseBranch)
	if p.UseFastWorktrees {
		ef.fastWTInput.SetValue("yes")
	} else {
		ef.fastWTInput.SetValue("")
	}
	ef.focusIndex = 0
	ef.focusCurrent()
}

func findRepoPath(name string) string {
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

	if projRoot := os.Getenv("PROJ_ROOT"); projRoot != "" {
		searchDirs = append(searchDirs, projRoot+"/projects")
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
	if m.selectedRepo != nil && m.selectedRepo.DefaultBaseBranch != "" {
		defaultBranch = m.selectedRepo.DefaultBaseBranch
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

	if m.selectedRepo != nil {
		remoteCmd := exec.Command("git", "-C", m.selectedRepo.Path, "branch", "-r", "--format=%(refname:short)")
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
	repos     []*repo.Repo
	prompts      []*prompt.Prompt
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
type projSetupCompleteMsg struct{ name string }
type projSetupFailedMsg struct {
	name string
	err  error
}
type ciCheckResultMsg struct {
	agentID          string
	status           ciStatus
	summary          string
	prURL            string
	err              error
	completed        int
	total            int
	hasMergeConflict bool
}

type ciStatus int

const (
	ciStatusPending ciStatus = iota
	ciStatusPassed
	ciStatusFailed
)

type ciProgress struct {
	Completed int
	Total     int
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

func initialModel(agentStore *agent.Store, queueManager *queue.Queue, repoStore *repo.Store, promptStore *prompt.Store, tmuxManager *tmux.Manager, sessionID string) model {
	taskInput := newFixedTextarea("Describe the task...", 60)
	branchInput := textinput.New()
	branchInput.Placeholder = "origin/master"
	branchInput.Width = 50
	branchInput.CharLimit = 100

	branchFilter := textinput.New()
	branchFilter.Placeholder = "Type to search branches..."
	branchFilter.Width = 50
	branchFilter.CharLimit = 100

	worktreeNameInput := textinput.New()
	worktreeNameInput.Placeholder = "e.g. fix-auth-bug (optional)"
	worktreeNameInput.Width = 50
	worktreeNameInput.CharLimit = 50

	interveneInput := newFixedTextarea("Type message to send to agent...", 60)

	progress := new(int64)

	return model{
		view:              ViewMain,
		taskInput:         taskInput,
		branchInput:       branchInput,
		branchFilter:      branchFilter,
		worktreeNameInput: worktreeNameInput,
		interveneInput:    interveneInput,
		repoForm:       newRepoForm(),
		editRepoForm:   newEditRepoForm(),
		promptForm:        newPromptForm(),
		editPromptForm:    newEditPromptForm(),
		spawnPromptEnabled: make(map[string]bool),
		ciLastChecked:   make(map[string]time.Time),
		ciChecking:      make(map[string]bool),
		ciCheckProgress: make(map[string]ciProgress),
		prevWindowNames:   make(map[string]string),
		totalMemKB:        getTotalMemoryKB(),
		clkTck:            getClockTicks(),
		prevCPUTicks:      make(map[int]int64),
		downloadProgress:  progress,
		projSetupBuffers:  make(map[string]*projImportBuffer),
		agentStore:        agentStore,
		queueManager:      queueManager,
		repoStore:      repoStore,
		promptStore:       promptStore,
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
		repos, _ := m.repoStore.List()

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
					m.agentStore.Update(a.ID, func(ag *agent.Agent) {
						ag.Status = agent.StatusReady
					})
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

		fastWTRepos := make(map[string]bool)
		for _, p := range repos {
			if p.UseFastWorktrees {
				fastWTRepos[p.Name] = true
			}
		}
		resources, newCPUTicks := queryAllAgentResources(
			agents, m.tmuxManager, m.totalMemKB, m.clkTck, m.prevCPUTicks, fastWTRepos,
		)

		prompts, _ := m.promptStore.List()

		return refreshMsg{
			agents:       agents,
			queueItems:   queueItems,
			repos:     repos,
			prompts:      prompts,
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
		shouldAnimate := m.updateChecking || m.updateDownloading || m.changelogLoading
		if !shouldAnimate {
			for _, p := range m.repos {
				if p.IsSettingUp() {
					shouldAnimate = true
					break
				}
			}
		}
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
		m.repos = msg.repos
		m.prompts = msg.prompts
		m.agentResources = msg.resources
		m.prevCPUTicks = msg.prevCPUTicks

		activeWaiting := make(map[string]bool)
		var cmds []tea.Cmd
		const ciPollInterval = 30 * time.Second
		for _, a := range m.agents {
			if (a.Status == agent.StatusWaitingCI || a.Status == agent.StatusWaitingReview) && a.PRURL != "" {
				activeWaiting[a.ID] = true
				if !m.ciChecking[a.ID] {
					lastCheck, checked := m.ciLastChecked[a.ID]
					if !checked || time.Since(lastCheck) >= ciPollInterval {
						m.ciChecking[a.ID] = true
						m.ciLastChecked[a.ID] = time.Now()
						cmds = append(cmds, checkPRChecksCmd(a.ID, a.PRURL, a.WorktreePath))
					}
				}
			}
		}
		for id := range m.ciLastChecked {
			if !activeWaiting[id] {
				delete(m.ciLastChecked, id)
				delete(m.ciChecking, id)
				delete(m.ciCheckProgress, id)
			}
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case spawnStartedMsg:
		return m, nil

	case ciCheckResultMsg:
		delete(m.ciChecking, msg.agentID)
		if msg.err != nil {
			return m, nil
		}

		var currentAgent *agent.Agent
		for _, ag := range m.agents {
			if ag.ID == msg.agentID {
				currentAgent = ag
				break
			}
		}
		wasWaitingReview := currentAgent != nil && currentAgent.Status == agent.StatusWaitingReview

		m.ciCheckProgress[msg.agentID] = ciProgress{Completed: msg.completed, Total: msg.total}
		if msg.hasMergeConflict {
			if currentAgent != nil {
				if wasWaitingReview {
					m.queueManager.RemoveByAgentAndType(msg.agentID, queue.ItemTypePRReady)
				}
				delete(m.ciCheckProgress, msg.agentID)
				return m, m.resumeAgentForMergeConflictCmd(currentAgent)
			}
		}
		switch msg.status {
		case ciStatusPassed:
			if !wasWaitingReview {
				delete(m.ciCheckProgress, msg.agentID)
				summary := getPRTitleFromURL(msg.prURL)
				if summary == "" {
					summary = fmt.Sprintf("PR ready: %s", msg.prURL)
				}
				m.queueManager.Add(queue.ItemTypePRReady, msg.agentID, summary, msg.prURL)
				m.agentStore.Update(msg.agentID, func(ag *agent.Agent) {
					ag.Status = agent.StatusWaitingReview
				})
				return m, m.refreshCmd()
			}
		case ciStatusFailed:
			if currentAgent != nil {
				if wasWaitingReview {
					m.queueManager.RemoveByAgentAndType(msg.agentID, queue.ItemTypePRReady)
				}
				return m, m.resumeAgentForCIFixCmd(currentAgent, msg.summary)
			}
		}
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
			if m.view == ViewUpdate {
				m.updateError = fmt.Sprintf("Update check failed: %s", msg.err.Error())
			}
			return m, nil
		}
		m.updateVersion = msg.version
		m.updateAvailable = msg.available
		if msg.available && m.view == ViewUpdate {
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

	case projSetupCompleteMsg:
		delete(m.projSetupBuffers, msg.name)
		if m.view == ViewProjImporting && m.projSetupName == msg.name {
			m.view = ViewManageRepos
			m.projSetupName = ""
		}
		return m, m.refreshCmd()

	case projSetupFailedMsg:
		delete(m.projSetupBuffers, msg.name)
		m.err = msg.err
		if m.view == ViewProjImporting && m.projSetupName == msg.name {
			m.view = ViewManageRepos
			m.projSetupName = ""
		}
		return m, tea.Batch(clearMessageCmd(), m.refreshCmd())

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
	if m.view == ViewNewTaskWorktreeName {
		var cmd tea.Cmd
		m.worktreeNameInput, cmd = m.worktreeNameInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	if m.view == ViewAddRepoName {
		var cmd tea.Cmd
		m.repoForm.nameInput, cmd = m.repoForm.nameInput.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.view == ViewAddRepoPath {
		var cmd tea.Cmd
		m.repoForm.pathInput, cmd = m.repoForm.pathInput.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.view == ViewEditRepo {
		var cmd tea.Cmd
		switch m.editRepoForm.focusIndex {
		case 0:
			m.editRepoForm.pathInput, cmd = m.editRepoForm.pathInput.Update(msg)
		case 1:
			m.editRepoForm.baseBranchInput, cmd = m.editRepoForm.baseBranchInput.Update(msg)
		case 2:
			m.editRepoForm.fastWTInput, cmd = m.editRepoForm.fastWTInput.Update(msg)
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
	case ViewSelectRepo:
		return m.handleSelectRepoKeys(msg)
	case ViewNewTaskBranch:
		return m.handleNewTaskBranchKeys(msg)
	case ViewNewTaskBranchInput:
		return m.handleNewTaskBranchInputKeys(msg)
	case ViewNewTaskInput:
		return m.handleNewTaskInputKeys(msg)
	case ViewNewTaskWorktreeName:
		return m.handleNewTaskWorktreeNameKeys(msg)
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
	case ViewManageRepos:
		return m.handleManageReposKeys(msg)
	case ViewAddRepoName:
		return m.handleAddRepoNameKeys(msg)
	case ViewAddRepoPath:
		return m.handleAddRepoPathKeys(msg)
	case ViewAddRepoFastWT:
		return m.handleAddRepoFastWTKeys(msg)
	case ViewProjImporting:
		return m.handleProjImportingKeys(msg)
	case ViewManagePrompts:
		return m.handleManagePromptsKeys(msg)
	case ViewAddPromptName:
		return m.handleAddPromptNameKeys(msg)
	case ViewAddPromptContent:
		return m.handleAddPromptContentKeys(msg)
	case ViewAddPromptDefault:
		return m.handleAddPromptDefaultKeys(msg)
	case ViewAddPromptRepos:
		return m.handleAddPromptReposKeys(msg)
	case ViewEditPrompt:
		return m.handleEditPromptKeys(msg)
	case ViewConfirmRemovePrompt:
		return m.handleConfirmRemovePromptKeys(msg)
	case ViewNewTaskSelectPrompts:
		return m.handleNewTaskSelectPromptsKeys(msg)
	case ViewEditRepo:
		return m.handleEditRepoKeys(msg)
	case ViewConfirmRemoveRepo:
		return m.handleConfirmRemoveRepoKeys(msg)
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
		case queue.ItemTypeIdle:
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
		if len(m.repos) == 0 {
			m.err = fmt.Errorf("no repos registered. Press [R] to add one")
			return m, clearMessageCmd()
		}
		m.view = ViewSelectRepo
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
		m.view = ViewManagePrompts
		m.selectedIndex = 0
	case "P":
		m.view = ViewManageRepos
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

func (m model) handleSelectRepoKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewMain
		m.selectedRepo = nil
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if m.selectedIndex < len(m.repos)-1 {
			m.selectedIndex++
		}
	case "enter":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.repos) {
			p := m.repos[m.selectedIndex]
			if p.IsSettingUp() {
				m.err = fmt.Errorf("project '%s' is still being set up", p.Name)
				return m, clearMessageCmd()
			}
			m.selectedRepo = p
			m.branchOptions = getLocalBranches(m.selectedRepo.Path)
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
		m.view = ViewSelectRepo
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
			if m.selectedRepo != nil && m.selectedRepo.DefaultBaseBranch != "" {
				branch = m.selectedRepo.DefaultBaseBranch
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
		m.spawnTask = task
		m.taskInput.Blur()
		m.spawnPromptEnabled = make(map[string]bool)
		m.spawnFilteredPrompts = nil
		for _, p := range m.prompts {
			if m.selectedRepo != nil && !p.AppliesToRepo(m.selectedRepo.Name) {
				continue
			}
			m.spawnFilteredPrompts = append(m.spawnFilteredPrompts, p)
			m.spawnPromptEnabled[p.ID] = p.IsDefault
		}
		m.view = ViewNewTaskSelectPrompts
		m.selectedIndex = 0
		return m, nil
	}

	var cmd tea.Cmd
	m.taskInput, cmd = m.taskInput.Update(msg)
	return m, cmd
}

func (m model) handleNewTaskWorktreeNameKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewNewTaskSelectPrompts
		m.worktreeNameInput.Blur()
		m.selectedIndex = 0
		return m, nil
	case "enter":
		worktreeName := m.worktreeNameInput.Value()
		task := m.spawnTask
		proj := m.selectedRepo
		branch := m.spawnBranch
		promptContent := enabledPromptContent(m.spawnFilteredPrompts, m.spawnPromptEnabled)
		m.view = ViewMain
		m.taskInput.SetValue("")
		m.taskInput.Blur()
		m.worktreeNameInput.SetValue("")
		m.worktreeNameInput.Blur()
		m.selectedRepo = nil
		m.spawnBranch = ""
		m.spawnTask = ""
		m.spawnPromptEnabled = make(map[string]bool)
		return m, m.spawnAgentCmd(task, proj, branch, worktreeName, promptContent)
	}

	var cmd tea.Cmd
	m.worktreeNameInput, cmd = m.worktreeNameInput.Update(msg)
	return m, cmd
}

func (m model) handleInterveneKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := filterQueueByType(m.queueItems, queue.ItemTypeIdle)

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
	// If we're in the confirmation step, handle y/esc to confirm or cancel.
	if m.confirmKillAgent != nil {
		switch msg.String() {
		case "y", "enter":
			selected := m.confirmKillAgent
			m.confirmKillAgent = nil
			m.view = ViewMain
			return m, m.killAgentCmd(selected)
		case "esc", "n":
			m.confirmKillAgent = nil
		}
		return m, nil
	}

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
			m.confirmKillAgent = m.agents[m.selectedIndex]
		}
	}
	return m, nil
}

func (m model) handleManageReposKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewMain
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if m.selectedIndex < len(m.repos)-1 {
			m.selectedIndex++
		}
	case "a":
		m.view = ViewAddRepoName
		m.repoForm.reset()
		m.newRepoName = ""
		m.newRepoPath = ""
		m.repoForm.nameInput.Focus()
		return m, textinput.Blink
	case "enter":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.repos) {
			p := m.repos[m.selectedIndex]
			if p.IsSettingUp() {
				m.projSetupName = p.Name
				m.view = ViewProjImporting
				return m, nil
			}
			m.selectedRepo = p
			m.view = ViewEditRepo
			m.editRepoForm.loadFromRepo(m.selectedRepo)
			return m, textinput.Blink
		}
	case "d":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.repos) {
			m.selectedRepo = m.repos[m.selectedIndex]
			m.view = ViewConfirmRemoveRepo
		}
	}
	return m, nil
}

func (m model) handleEditRepoKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editRepoForm.blurAll()
		m.view = ViewManageRepos
		m.selectedRepo = nil
		return m, nil
	case "tab":
		m.editRepoForm.focusIndex = (m.editRepoForm.focusIndex + 1) % 3
		m.editRepoForm.focusCurrent()
		return m, textinput.Blink
	case "shift+tab":
		m.editRepoForm.focusIndex = (m.editRepoForm.focusIndex + 2) % 3
		m.editRepoForm.focusCurrent()
		return m, textinput.Blink
	case "enter":
		if m.selectedRepo == nil {
			return m, nil
		}
		path := m.editRepoForm.pathInput.Value()
		baseBranch := m.editRepoForm.baseBranchInput.Value()
		fastWTStr := strings.ToLower(strings.TrimSpace(m.editRepoForm.fastWTInput.Value()))
		useFastWT := fastWTStr == "yes" || fastWTStr == "true" || fastWTStr == "y"
		projName := m.selectedRepo.Name
		alreadyHasFastWT := m.selectedRepo.UseFastWorktrees && m.selectedRepo.FastWorktreePath != ""
		m.editRepoForm.blurAll()
		m.selectedRepo = nil
		m.view = ViewManageRepos
		if useFastWT && !alreadyHasFastWT {
			m.projSetupBuffers[projName] = &projImportBuffer{}
		}
		return m, m.updateRepoCmd(projName, path, baseBranch, useFastWT)
	}

	var cmd tea.Cmd
	switch m.editRepoForm.focusIndex {
	case 0:
		m.editRepoForm.pathInput, cmd = m.editRepoForm.pathInput.Update(msg)
	case 1:
		m.editRepoForm.baseBranchInput, cmd = m.editRepoForm.baseBranchInput.Update(msg)
	case 2:
		m.editRepoForm.fastWTInput, cmd = m.editRepoForm.fastWTInput.Update(msg)
	}
	return m, cmd
}

func (m model) handleAddRepoNameKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewManageRepos
		m.repoForm.reset()
		return m, nil
	case "enter":
		name := m.repoForm.nameInput.Value()
		if name == "" {
			return m, nil
		}
		m.newRepoName = name
		m.view = ViewAddRepoPath
		// Auto-detect path
		if found := findRepoPath(name); found != "" {
			m.repoForm.pathInput.SetValue(found)
		} else {
			m.repoForm.pathInput.SetValue("")
		}
		m.repoForm.pathInput.Focus()
		return m, textinput.Blink
	}

	var cmd tea.Cmd
	m.repoForm.nameInput, cmd = m.repoForm.nameInput.Update(msg)
	return m, cmd
}

func (m model) handleAddRepoPathKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewAddRepoName
		m.repoForm.nameInput.SetValue(m.newRepoName)
		m.repoForm.nameInput.Focus()
		return m, textinput.Blink
	case "enter":
		path := m.repoForm.pathInput.Value()
		if path == "" {
			return m, nil
		}
		m.newRepoPath = path
		if repo.IsProjDirectory(path) && repo.IsProjInstalled() {
			m.view = ViewManageRepos
			return m, m.addRepoCmd(m.newRepoName, m.newRepoPath, true)
		}
		if !repo.IsProjInstalled() {
			m.view = ViewManageRepos
			return m, m.addRepoCmd(m.newRepoName, m.newRepoPath, false)
		}
		m.view = ViewAddRepoFastWT
		return m, nil
	}

	var cmd tea.Cmd
	m.repoForm.pathInput, cmd = m.repoForm.pathInput.Update(msg)
	return m, cmd
}

func (m model) handleAddRepoFastWTKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewAddRepoPath
		m.repoForm.pathInput.Focus()
		return m, textinput.Blink
	case "y":
		m.projSetupBuffers[m.newRepoName] = &projImportBuffer{}
		m.view = ViewManageRepos
		return m, m.addRepoCmd(m.newRepoName, m.newRepoPath, true)
	case "n":
		m.view = ViewManageRepos
		return m, m.addRepoCmd(m.newRepoName, m.newRepoPath, false)
	}
	return m, nil
}

func (m model) handleProjImportingKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.projSetupName = ""
		m.view = ViewManageRepos
	}
	return m, nil
}

func (m model) handleConfirmRemoveRepoKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if m.selectedRepo != nil {
			projToRemove := m.selectedRepo
			m.selectedRepo = nil
			m.view = ViewManageRepos
			m.selectedIndex = 0
			return m, m.removeRepoCmd(projToRemove.Name)
		}
	case "n", "esc":
		m.selectedRepo = nil
		m.view = ViewManageRepos
	}
	return m, nil
}

func (m model) handleManagePromptsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewMain
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if m.selectedIndex < len(m.prompts)-1 {
			m.selectedIndex++
		}
	case "a":
		m.view = ViewAddPromptName
		m.promptForm.reset()
		m.newPromptName = ""
		m.newPromptIsDefault = false
		m.promptForm.nameInput.Focus()
		return m, textinput.Blink
	case "enter":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.prompts) {
			m.selectedPrompt = m.prompts[m.selectedIndex]
			m.view = ViewEditPrompt
			m.editPromptForm.loadFromPrompt(m.selectedPrompt)
			m.promptRepoEnabled = promptRepoEnabledFromPrompt(m.selectedPrompt)
			m.promptRepoIndex = 0
			return m, textinput.Blink
		}
	case "d":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.prompts) {
			m.selectedPrompt = m.prompts[m.selectedIndex]
			m.view = ViewConfirmRemovePrompt
		}
	}
	return m, nil
}

func (m model) handleAddPromptNameKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewManagePrompts
		m.promptForm.reset()
		return m, nil
	case "enter":
		name := m.promptForm.nameInput.Value()
		if name == "" {
			return m, nil
		}
		m.newPromptName = name
		m.view = ViewAddPromptContent
		m.promptForm.contentInput.SetValue("")
		m.promptForm.nameInput.Blur()
		cmd := m.promptForm.contentInput.Focus()
		return m, cmd
	}

	var cmd tea.Cmd
	m.promptForm.nameInput, cmd = m.promptForm.nameInput.Update(msg)
	return m, cmd
}

func (m model) handleAddPromptContentKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewAddPromptName
		m.promptForm.contentInput.Blur()
		m.promptForm.nameInput.Focus()
		return m, textinput.Blink
	case "enter":
		content := m.promptForm.contentInput.Value()
		if content == "" {
			return m, nil
		}
		m.view = ViewAddPromptDefault
		m.promptForm.contentInput.Blur()
		return m, nil
	}

	var cmd tea.Cmd
	m.promptForm.contentInput, cmd = m.promptForm.contentInput.Update(msg)
	return m, cmd
}

func (m model) handleAddPromptDefaultKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewAddPromptContent
		cmd := m.promptForm.contentInput.Focus()
		return m, cmd
	case "y":
		m.newPromptIsDefault = true
		m.promptRepoEnabled = make(map[string]bool)
		m.promptRepoIndex = 0
		m.view = ViewAddPromptRepos
		return m, nil
	case "n":
		m.newPromptIsDefault = false
		m.promptRepoEnabled = make(map[string]bool)
		m.promptRepoIndex = 0
		m.view = ViewAddPromptRepos
		return m, nil
	}
	return m, nil
}

func (m model) handleAddPromptReposKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewAddPromptDefault
		return m, nil
	case "up", "k":
		if m.promptRepoIndex > 0 {
			m.promptRepoIndex--
		}
	case "down", "j":
		if m.promptRepoIndex < len(m.repos)-1 {
			m.promptRepoIndex++
		}
	case " ":
		if m.promptRepoIndex >= 0 && m.promptRepoIndex < len(m.repos) {
			p := m.repos[m.promptRepoIndex]
			m.promptRepoEnabled[p.Name] = !m.promptRepoEnabled[p.Name]
		}
	case "enter":
		isDefault := m.newPromptIsDefault
		projectNames := m.enabledRepoNames()
		m.view = ViewManagePrompts
		m.selectedIndex = 0
		return m, m.addPromptCmd(m.newPromptName, m.promptForm.contentInput.Value(), isDefault, projectNames)
	}
	return m, nil
}

func (m model) enabledRepoNames() []string {
	var names []string
	for _, p := range m.repos {
		if m.promptRepoEnabled[p.Name] {
			names = append(names, p.Name)
		}
	}
	return names
}

func (m model) handleEditPromptKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	numFields := 4
	switch msg.String() {
	case "esc":
		m.editPromptForm.blurAll()
		m.view = ViewManagePrompts
		m.selectedPrompt = nil
		return m, nil
	case "tab":
		m.editPromptForm.focusIndex = (m.editPromptForm.focusIndex + 1) % numFields
		m.editPromptForm.focusCurrent()
		if m.editPromptForm.focusIndex == 3 {
			return m, nil
		}
		return m, textinput.Blink
	case "shift+tab":
		m.editPromptForm.focusIndex = (m.editPromptForm.focusIndex + numFields - 1) % numFields
		m.editPromptForm.focusCurrent()
		if m.editPromptForm.focusIndex == 3 {
			return m, nil
		}
		return m, textinput.Blink
	case "enter":
		if m.selectedPrompt == nil {
			return m, nil
		}
		name := m.editPromptForm.nameInput.Value()
		content := m.editPromptForm.contentInput.Value()
		defaultStr := strings.ToLower(strings.TrimSpace(m.editPromptForm.defaultInput.Value()))
		isDefault := defaultStr == "yes" || defaultStr == "true" || defaultStr == "y"
		projectNames := m.enabledRepoNames()
		promptID := m.selectedPrompt.ID
		m.editPromptForm.blurAll()
		m.selectedPrompt = nil
		m.view = ViewManagePrompts
		return m, m.updatePromptCmd(promptID, name, content, isDefault, projectNames)
	}

	if m.editPromptForm.focusIndex == 3 {
		switch msg.String() {
		case "up", "k":
			if m.promptRepoIndex > 0 {
				m.promptRepoIndex--
			}
		case "down", "j":
			if m.promptRepoIndex < len(m.repos)-1 {
				m.promptRepoIndex++
			}
		case " ":
			if m.promptRepoIndex >= 0 && m.promptRepoIndex < len(m.repos) {
				p := m.repos[m.promptRepoIndex]
				m.promptRepoEnabled[p.Name] = !m.promptRepoEnabled[p.Name]
			}
		}
		return m, nil
	}

	var cmd tea.Cmd
	switch m.editPromptForm.focusIndex {
	case 0:
		m.editPromptForm.nameInput, cmd = m.editPromptForm.nameInput.Update(msg)
	case 1:
		m.editPromptForm.contentInput, cmd = m.editPromptForm.contentInput.Update(msg)
	case 2:
		m.editPromptForm.defaultInput, cmd = m.editPromptForm.defaultInput.Update(msg)
	}
	return m, cmd
}

func (m model) handleConfirmRemovePromptKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if m.selectedPrompt != nil {
			promptToRemove := m.selectedPrompt
			m.selectedPrompt = nil
			m.view = ViewManagePrompts
			m.selectedIndex = 0
			return m, m.removePromptCmd(promptToRemove.ID)
		}
	case "n", "esc":
		m.selectedPrompt = nil
		m.view = ViewManagePrompts
	}
	return m, nil
}

func (m model) handleNewTaskSelectPromptsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = ViewNewTaskInput
		m.selectedIndex = 0
		cmd := m.taskInput.Focus()
		return m, cmd
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	case "down", "j":
		if m.selectedIndex < len(m.spawnFilteredPrompts)-1 {
			m.selectedIndex++
		}
	case " ":
		if m.selectedIndex >= 0 && m.selectedIndex < len(m.spawnFilteredPrompts) {
			p := m.spawnFilteredPrompts[m.selectedIndex]
			m.spawnPromptEnabled[p.ID] = !m.spawnPromptEnabled[p.ID]
		}
	case "enter":
		m.view = ViewNewTaskWorktreeName
		m.worktreeNameInput.SetValue("")
		m.worktreeNameInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m model) addPromptCmd(name, content string, isDefault bool, projectNames []string) tea.Cmd {
	return func() tea.Msg {
		p := &prompt.Prompt{
			Name:         name,
			Content:      content,
			IsDefault:    isDefault,
			RepoNames: projectNames,
		}
		if err := m.promptStore.Add(p); err != nil {
			return errMsg{err}
		}
		return successMsg{msg: fmt.Sprintf("Prompt '%s' added", name)}
	}
}

func (m model) updatePromptCmd(id, name, content string, isDefault bool, projectNames []string) tea.Cmd {
	return func() tea.Msg {
		if err := m.promptStore.Update(id, func(p *prompt.Prompt) {
			p.Name = name
			p.Content = content
			p.IsDefault = isDefault
			p.RepoNames = projectNames
		}); err != nil {
			return errMsg{err}
		}
		return successMsg{msg: fmt.Sprintf("Prompt '%s' updated", name)}
	}
}

func (m model) removePromptCmd(id string) tea.Cmd {
	return func() tea.Msg {
		if err := m.promptStore.Remove(id); err != nil {
			return errMsg{err}
		}
		return successMsg{msg: "Prompt removed"}
	}
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
	if m.updateChecking || m.updateDownloading || m.changelogLoading {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		if !m.updateComplete {
			m.view = ViewMain
		}
	case "c":
		if m.updateAvailable && !m.updateDownloading && !m.updateComplete {
			m.updateDownloading = true
			atomic.StoreInt64(m.downloadProgress, 0)
			return m, downloadUpdateCmd(m.updateVersion, m.downloadProgress)
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
		var base string
		if a.WorktreeName != "" && a.RepoName != "" {
			base = a.RepoName + ":" + a.WorktreeName
		} else {
			base = a.ID[:8]
		}
		switch a.Status {
		case agent.StatusSpawning:
			name = spinner(m.spinnerFrame) + " " + base
		case agent.StatusRunning:
			name = spinner(m.spinnerFrame) + " " + base
		case agent.StatusCleaningUp:
			name = spinner(m.spinnerFrame) + " " + base
		case agent.StatusKilling:
			name = spinner(m.spinnerFrame) + " " + base
		case agent.StatusReady:
			name = "● " + base
		case agent.StatusWaitingReview:
			name = "● " + base
		case agent.StatusWaitingCI:
			name = "⏳ " + base
		default:
			name = base
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

func (m model) addRepoCmd(name, path string, useFastWT bool) tea.Cmd {
	buf := m.projSetupBuffers[name]
	return func() tea.Msg {
		var fastWTPath string
		needsImport := false
		if useFastWT {
			if repo.IsProjDirectory(path) {
				fastWTPath = path
			} else {
				needsImport = true
			}
		}
		setupStatus := ""
		if needsImport {
			setupStatus = repo.SetupStatusSettingUp
		}
		p := &repo.Repo{
			Name:             name,
			Path:             path,
			FastWorktreePath: fastWTPath,
			UseFastWorktrees: useFastWT,
			SetupStatus:      setupStatus,
		}
		if err := m.repoStore.Add(p); err != nil {
			return errMsg{err}
		}
		if needsImport {
			projDir, err := repo.ProjImport(path, buf.addLine)
			if err != nil {
				m.repoStore.Update(name, func(p *repo.Repo) {
					p.SetupStatus = ""
					p.UseFastWorktrees = false
				})
				return projSetupFailedMsg{name: name, err: err}
			}
			m.repoStore.Update(name, func(p *repo.Repo) {
				p.FastWorktreePath = projDir
				p.SetupStatus = ""
			})
			return projSetupCompleteMsg{name: name}
		}
		return successMsg{fmt.Sprintf("Added project '%s'", name)}
	}
}

func (m model) updateRepoCmd(name, path, baseBranch string, useFastWT bool) tea.Cmd {
	buf := m.projSetupBuffers[name]
	return func() tea.Msg {
		var fastWTPath string
		needsImport := false
		if useFastWT && path != "" {
			existing, _ := m.repoStore.Get(name)
			if existing != nil && existing.FastWorktreePath != "" && repo.IsProjDirectory(existing.FastWorktreePath) {
				fastWTPath = existing.FastWorktreePath
			} else if repo.IsProjDirectory(path) {
				fastWTPath = path
			} else {
				needsImport = true
			}
		}
		err := m.repoStore.Update(name, func(p *repo.Repo) {
			if path != "" {
				p.Path = path
			}
			if fastWTPath != "" {
				p.FastWorktreePath = fastWTPath
			}
			p.DefaultBaseBranch = baseBranch
			p.UseFastWorktrees = useFastWT
			if needsImport {
				p.SetupStatus = repo.SetupStatusSettingUp
			}
		})
		if err != nil {
			return errMsg{err}
		}
		if needsImport {
			projDir, err := repo.ProjImport(path, buf.addLine)
			if err != nil {
				m.repoStore.Update(name, func(p *repo.Repo) {
					p.SetupStatus = ""
					p.UseFastWorktrees = false
				})
				return projSetupFailedMsg{name: name, err: err}
			}
			m.repoStore.Update(name, func(p *repo.Repo) {
				p.FastWorktreePath = projDir
				p.SetupStatus = ""
			})
			return projSetupCompleteMsg{name: name}
		}
		return successMsg{fmt.Sprintf("Updated project '%s'", name)}
	}
}

func (m model) removeRepoCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if err := m.repoStore.Remove(name); err != nil {
			return errMsg{err}
		}
		return successMsg{fmt.Sprintf("Removed project '%s'", name)}
	}
}

func (m model) spawnAgentCmd(task string, proj *repo.Repo, branch string, worktreeName string, promptContent string) tea.Cmd {
	return func() tea.Msg {
		exePath, err := os.Executable()
		if err != nil {
			return errMsg{fmt.Errorf("failed to get executable: %w", err)}
		}
		args := []string{"spawn", task, "--repo", proj.Name, "--branch", branch}
		if worktreeName != "" {
			args = append(args, "--worktree-name", worktreeName)
		}
		if promptContent != "" {
			args = append(args, "--prompts", promptContent)
		}
		cmd := exec.Command(exePath, args...)
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
		if a.Status == agent.StatusReady {
			m.agentStore.Update(a.ID, func(ag *agent.Agent) {
				ag.Status = agent.StatusRunning
			})
		}
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
		if a.Status == agent.StatusReady {
			m.agentStore.Update(a.ID, func(ag *agent.Agent) {
				ag.Status = agent.StatusRunning
			})
		}
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
  "The GitHub PR at %s has received comments. Please review ALL comments — both conversation-level comments (gh pr view %s --comments) AND inline review comments (gh api repos/{owner}/{repo}/pulls/{number}/comments). Make sure to check both types so you don't miss any feedback. Address all the feedback. Commit and push your changes, then run: ccmux ci-wait %s"

ccmux agent-stopped "$AGENT_ID"
`, agentID, worktreePath, prURL, prURL, prURL)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

type prCheckResult struct {
	TypeName   string `json:"__typename"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	StartedAt  string `json:"startedAt"`
	Context    string `json:"context"`
	State      string `json:"state"`
}

type statusCheckRollupResponse struct {
	StatusCheckRollup []prCheckResult `json:"statusCheckRollup"`
	Mergeable         string          `json:"mergeable"`
}

func parsePRURL(prURL string) (owner, repo, prNumber string, err error) {
	parts := strings.Split(prURL, "/")
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("invalid PR URL")
	}
	prNumber = parts[len(parts)-1]

	for i, part := range parts {
		if part == "github.com" && i+2 < len(parts) {
			owner = parts[i+1]
			repo = parts[i+2]
			break
		}
	}
	if owner == "" || repo == "" {
		return "", "", "", fmt.Errorf("could not parse owner/repo from URL")
	}
	return owner, repo, prNumber, nil
}

func normalizeChecks(checks []prCheckResult) []prCheckResult {
	result := make([]prCheckResult, len(checks))
	for i, c := range checks {
		if c.TypeName == "StatusContext" {
			state := strings.ToUpper(c.State)
			status := "COMPLETED"
			conclusion := state
			if state == "PENDING" || state == "EXPECTED" {
				status = "IN_PROGRESS"
				conclusion = ""
			}
			result[i] = prCheckResult{
				TypeName:   c.TypeName,
				Name:       c.Context,
				Status:     status,
				Conclusion: conclusion,
				StartedAt:  c.StartedAt,
				Context:    c.Context,
				State:      c.State,
			}
		} else {
			result[i] = c
		}
	}
	return result
}

func deduplicateChecks(checks []prCheckResult) []prCheckResult {
	latest := make(map[string]prCheckResult)
	for _, c := range checks {
		existing, found := latest[c.Name]
		if !found {
			latest[c.Name] = c
			continue
		}
		if c.StartedAt > existing.StartedAt {
			latest[c.Name] = c
		}
	}
	result := make([]prCheckResult, 0, len(latest))
	for _, c := range latest {
		result = append(result, c)
	}
	return result
}

func evaluateCIChecks(checks []prCheckResult) (status ciStatus, failedNames []string, completed int, total int) {
	checks = normalizeChecks(checks)
	checks = deduplicateChecks(checks)
	total = len(checks)
	if total == 0 {
		return ciStatusPending, nil, 0, 0
	}

	hasPending := false
	for _, c := range checks {
		st := strings.ToUpper(c.Status)
		concl := strings.ToUpper(c.Conclusion)

		if st == "COMPLETED" {
			completed++
			switch concl {
			case "SUCCESS", "NEUTRAL", "SKIPPED":
			case "FAILURE", "ERROR", "CANCELLED", "ACTION_REQUIRED", "TIMED_OUT", "STARTUP_FAILURE":
				failedNames = append(failedNames, c.Name)
			default:
				failedNames = append(failedNames, c.Name)
			}
		} else {
			hasPending = true
		}
	}

	if len(failedNames) > 0 {
		sort.Strings(failedNames)
		return ciStatusFailed, failedNames, completed, total
	}
	if hasPending {
		return ciStatusPending, nil, completed, total
	}
	return ciStatusPassed, nil, completed, total
}

func checkPRChecksCmd(agentID, prURL, worktreePath string) tea.Cmd {
	return func() tea.Msg {
		owner, repo, prNumber, err := parsePRURL(prURL)
		if err != nil {
			return ciCheckResultMsg{agentID: agentID, err: err}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "gh", "pr", "view", prNumber, "--repo", owner+"/"+repo, "--json", "statusCheckRollup,mergeable")
		output, err := cmd.Output()
		if err != nil {
			return ciCheckResultMsg{agentID: agentID, err: err}
		}

		var resp statusCheckRollupResponse
		if err := json.Unmarshal(output, &resp); err != nil {
			return ciCheckResultMsg{agentID: agentID, err: err}
		}

		hasMergeConflict := resp.Mergeable == "CONFLICTING"

		// If statusCheckRollup is null (not just empty), this repo has no CI checks configured.
		// Treat as passed rather than waiting indefinitely.
		if resp.StatusCheckRollup == nil {
			return ciCheckResultMsg{agentID: agentID, status: ciStatusPassed, prURL: prURL, hasMergeConflict: hasMergeConflict}
		}

		status, failedNames, completed, total := evaluateCIChecks(resp.StatusCheckRollup)
		var summary string
		if status == ciStatusFailed {
			summary = fmt.Sprintf("CI checks failed: %s", strings.Join(failedNames, ", "))
		}

		return ciCheckResultMsg{agentID: agentID, status: status, summary: summary, prURL: prURL, completed: completed, total: total, hasMergeConflict: hasMergeConflict}
	}
}

func getPRTitleFromURL(prURL string) string {
	owner, repo, prNumber, err := parsePRURL(prURL)
	if err != nil {
		return ""
	}

	cmd := exec.Command("gh", "pr", "view", prNumber, "--repo", owner+"/"+repo, "--json", "title", "-q", ".title")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func writeCIFixScript(agentID, worktreePath, prURL, failureSummary string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+"-ci-fix.sh")

	script := fmt.Sprintf(`#!/bin/bash
set -e

AGENT_ID="%s"

cd "%s"

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
RESET="\033[0m"
echo -e "${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Fixing CI failures...${RESET}"
echo ""

export CCMUX_AGENT_ID="$AGENT_ID"
unset CLAUDECODE

claude --continue --dangerously-skip-permissions \
  "CI checks have FAILED for the PR at %s. Failures: %s -- Investigate the failures using: gh pr checks %s -- Fix the issues, commit and push your changes, then run: ccmux ci-wait %s"

ccmux agent-stopped "$AGENT_ID"
`, agentID, worktreePath, prURL, failureSummary, prURL, prURL)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

func (m model) resumeAgentForCIFixCmd(a *agent.Agent, failureSummary string) tea.Cmd {
	agentID := a.ID
	worktreePath := a.WorktreePath
	tmuxWindow := a.TmuxWindow
	prURL := a.PRURL
	return func() tea.Msg {
		m.agentStore.Update(agentID, func(ag *agent.Agent) {
			ag.Status = agent.StatusRunning
		})

		scriptPath, err := writeCIFixScript(agentID, worktreePath, prURL, failureSummary)
		if err != nil {
			return errMsg{fmt.Errorf("failed to write CI fix script: %w", err)}
		}

		m.tmuxManager.KillWindow(tmuxWindow)
		newWindowID, err := m.tmuxManager.CreateWindow(worktreePath, "bash "+scriptPath, agentID[:8])
		if err != nil {
			return errMsg{fmt.Errorf("failed to create CI fix window: %w", err)}
		}

		m.agentStore.Update(agentID, func(ag *agent.Agent) {
			ag.TmuxWindow = newWindowID
		})

		return successMsg{fmt.Sprintf("Agent %s resumed to fix CI failures", agentID)}
	}
}

func writeMergeConflictScript(agentID, worktreePath, prURL, baseBranch string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return "", err
	}

	scriptPath := filepath.Join(launcherDir, agentID+"-merge-conflict.sh")

	script := fmt.Sprintf(`#!/bin/bash
set -e

AGENT_ID="%s"

cd "%s"

BLUE="\033[38;5;63m"
WHITE="\033[1;97m"
DIM="\033[38;5;245m"
RESET="\033[0m"
echo -e "${BLUE}CC${WHITE}MUX Agent ${DIM}$AGENT_ID${RESET}"
echo -e "${DIM}Resolving merge conflicts...${RESET}"
echo ""

export CCMUX_AGENT_ID="$AGENT_ID"
unset CLAUDECODE

claude --continue --dangerously-skip-permissions \
  "The PR at %s has merge conflicts with the base branch (%s). Resolve the merge conflicts, push your changes, then run: ccmux ci-wait %s"

ccmux agent-stopped "$AGENT_ID"
`, agentID, worktreePath, prURL, baseBranch, prURL)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", err
	}

	return scriptPath, nil
}

func (m model) resumeAgentForMergeConflictCmd(a *agent.Agent) tea.Cmd {
	agentID := a.ID
	worktreePath := a.WorktreePath
	tmuxWindow := a.TmuxWindow
	prURL := a.PRURL
	baseBranch := a.BaseBranch
	return func() tea.Msg {
		m.agentStore.Update(agentID, func(ag *agent.Agent) {
			ag.Status = agent.StatusRunning
		})

		scriptPath, err := writeMergeConflictScript(agentID, worktreePath, prURL, baseBranch)
		if err != nil {
			return errMsg{fmt.Errorf("failed to write merge conflict script: %w", err)}
		}

		m.tmuxManager.KillWindow(tmuxWindow)
		newWindowID, err := m.tmuxManager.CreateWindow(worktreePath, "bash "+scriptPath, agentID[:8])
		if err != nil {
			return errMsg{fmt.Errorf("failed to create merge conflict window: %w", err)}
		}

		m.agentStore.Update(agentID, func(ag *agent.Agent) {
			ag.TmuxWindow = newWindowID
		})

		return successMsg{fmt.Sprintf("Agent %s resumed to resolve merge conflicts", agentID)}
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
	case ViewSelectRepo:
		content = renderSelectRepoView(m)
	case ViewNewTaskBranch:
		content = renderNewTaskBranchView(m)
	case ViewNewTaskBranchInput:
		content = renderNewTaskBranchInputView(m)
	case ViewNewTaskInput:
		content = renderNewTaskInputView(m)
	case ViewNewTaskWorktreeName:
		content = renderNewTaskWorktreeNameView(m)
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
	case ViewManageRepos:
		content = renderManageReposView(m)
	case ViewAddRepoName:
		content = renderAddRepoNameView(m)
	case ViewAddRepoPath:
		content = renderAddRepoPathView(m)
	case ViewAddRepoFastWT:
		content = renderAddRepoFastWTView(m)
	case ViewProjImporting:
		content = renderProjImportingView(m)
	case ViewManagePrompts:
		content = renderManagePromptsView(m)
	case ViewAddPromptName:
		content = renderAddPromptNameView(m)
	case ViewAddPromptContent:
		content = renderAddPromptContentView(m)
	case ViewAddPromptDefault:
		content = renderAddPromptDefaultView(m)
	case ViewAddPromptRepos:
		content = renderAddPromptReposView(m)
	case ViewEditPrompt:
		content = renderEditPromptView(m)
	case ViewConfirmRemovePrompt:
		content = renderConfirmRemovePromptView(m)
	case ViewNewTaskSelectPrompts:
		content = renderNewTaskSelectPromptsView(m)
	case ViewEditRepo:
		content = renderEditRepoView(m)
	case ViewConfirmRemoveRepo:
		content = renderConfirmRemoveRepoView(m)
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

func Run(agentStore *agent.Store, queueManager *queue.Queue, repoStore *repo.Store, promptStore *prompt.Store, tmuxManager *tmux.Manager, sessionID string) (bool, error) {
	m := initialModel(agentStore, queueManager, repoStore, promptStore, tmuxManager, sessionID)
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
