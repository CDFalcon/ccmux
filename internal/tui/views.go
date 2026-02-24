package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/queue"
)

type ViewState int

const (
	ViewMain ViewState = iota
	ViewSelectProject
	ViewNewTaskBranch
	ViewNewTaskInput
	ViewIntervene
	ViewInterveneInput
	ViewReview
	ViewConfirmMerge
	ViewConfirmKill
	ViewManageProjects
	ViewAddProjectName
	ViewAddProjectPath
	ViewConfirmRemoveProject
	ViewConfirmKillSession
	ViewJumpToAgent
)

const (
	MaxTaskDisplayLen    = 40
	MaxSummaryDisplayLen = 50
	SpinnerFrameCount    = 6
	MarqueeTickRate      = 3
	MarqueeSeparator     = "  \u00b7\u00b7\u00b7  "
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴"}

func renderCtrlCIndicator(pressed bool) string {
	if pressed {
		return errorStyle.Render("Press Ctrl+C again to detach")
	}
	return ""
}

func renderLogo() string {
	c := logoCStyle
	w := logoStyle

	lines := []string{
		c.Render("   ██████╗  ██████╗ ") + w.Render("███╗   ███╗██╗   ██╗██╗  ██╗"),
		c.Render("  ██╔════╝ ██╔════╝ ") + w.Render("████╗ ████║██║   ██║╚██╗██╔╝"),
		c.Render("  ██║      ██║      ") + w.Render("██╔████╔██║██║   ██║ ╚███╔╝ "),
		c.Render("  ██║      ██║      ") + w.Render("██║╚██╔╝██║██║   ██║ ██╔██╗ "),
		c.Render("  ╚██████╗ ╚██████╗ ") + w.Render("██║ ╚═╝ ██║╚██████╔╝██╔╝ ██╗"),
		c.Render("   ╚═════╝  ╚═════╝ ") + w.Render("╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═╝"),
		"  " + c.Render("C") + w.Render("laude ") + c.Render("C") + w.Render("ode ") + w.Render("Mu") + w.Render("ltiple") + w.Render("x") + w.Render("er"),
	}

	return strings.Join(lines, "\n")
}

func renderMainView(m model) string {
	var b strings.Builder

	b.WriteString(renderLogo())
	b.WriteString("\n\n")

	b.WriteString(headerStyle.Render(fmt.Sprintf("# Agents (%d)", len(m.agents))))
	b.WriteString("\n")
	if len(m.agents) == 0 {
		b.WriteString(dimStyle.Render("  No agents running"))
		b.WriteString("\n")
	} else {
		for _, a := range m.agents {
			if a.Status == agent.StatusKilling || (m.cleaningUp && a.ID == m.cleaningUpAgent) {
				spin := styledSpinner(m.spinnerFrame, agentKillingStyle)
				status := agentKillingStyle.Render("killing")
				line := fmt.Sprintf("  %s %s: %s [%s]", spin, a.ID, marquee(a.Task, MaxTaskDisplayLen, m.marqueeOffset), status)
				b.WriteString(line)
				b.WriteString("\n")
			} else if a.Status == agent.StatusSpawning {
				spin := styledSpinner(m.spinnerFrame, agentSpawningStyle)
				status := agentSpawningStyle.Render("spawning")
				line := fmt.Sprintf("  %s %s: %s [%s]", spin, a.ID, marquee(a.Task, MaxTaskDisplayLen, m.marqueeOffset), status)
				b.WriteString(line)
				b.WriteString("\n")
			} else if a.Status == agent.StatusRunning {
				spin := styledSpinner(m.spinnerFrame, agentRunningStyle)
				status := agentRunningStyle.Render("running")
				line := fmt.Sprintf("  %s %s: %s [%s]", spin, a.ID, marquee(a.Task, MaxTaskDisplayLen, m.marqueeOffset), status)
				b.WriteString(line)
				b.WriteString("\n")
			} else {
				status := renderAgentStatus(a.Status)
				line := fmt.Sprintf("  - %s: %s [%s]", a.ID, truncate(a.Task, MaxTaskDisplayLen), status)
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("\n")

	b.WriteString(headerStyle.Render(fmt.Sprintf("# Queue (%d items)", len(m.queueItems))))
	b.WriteString("\n")
	if len(m.queueItems) == 0 {
		b.WriteString(dimStyle.Render("  No items needing attention"))
		b.WriteString("\n")
	} else {
		for _, item := range m.queueItems {
			icon := getItemIcon(item.Type)
			style := getItemStyle(item.Type)
			age := formatAge(item.Timestamp)
			line := fmt.Sprintf("  - %s %s: %s %s", icon, item.AgentID, style.Render(truncate(item.Summary, MaxSummaryDisplayLen)), dimStyle.Render(age))
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")

	b.WriteString(headerStyle.Render(fmt.Sprintf("# Projects (%d)", len(m.projects))))
	b.WriteString("\n")
	if len(m.projects) == 0 {
		b.WriteString(dimStyle.Render("  No projects registered. Press [p] to add one."))
		b.WriteString("\n")
	} else {
		for _, p := range m.projects {
			b.WriteString(fmt.Sprintf("  - %s %s\n", projectStyle.Render(p.Name), dimStyle.Render(p.Path)))
		}
	}
	b.WriteString("\n")

	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %s", m.err.Error())))
		b.WriteString("\n\n")
	}

	help := "[q]uick respond  [n]ew task  [j]ump to agent  [k]ill agent  [p]rojects  [K]ill session"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderSelectProjectView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Select Project"))
	b.WriteString("\n\n")

	if len(m.projects) == 0 {
		b.WriteString(dimStyle.Render("No projects registered"))
		b.WriteString("\n\n")
	} else {
		for i, p := range m.projects {
			style := queueItemStyle
			if i == m.selectedIndex {
				style = selectedItemStyle
			}
			line := fmt.Sprintf("%s  %s", p.Name, dimStyle.Render(p.Path))
			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	help := "[↑/↓/j/k] select  [enter] choose  [esc] back"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderNewTaskBranchView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# New Task - Base Branch"))
	b.WriteString("\n\n")

	if m.selectedProj != nil {
		b.WriteString(fmt.Sprintf("Project: %s\n", projectStyle.Render(m.selectedProj.Name)))
		b.WriteString(fmt.Sprintf("Path: %s\n", dimStyle.Render(m.selectedProj.Path)))
		b.WriteString("\n")
	}

	b.WriteString("Enter base branch (leave empty for origin/master):\n")
	b.WriteString(inputStyle.Render(m.branchInput.View()))
	b.WriteString("\n\n")

	b.WriteString(dimStyle.Render("Branch to create worktree from (e.g., 'origin/main', 'origin/develop')"))
	b.WriteString("\n\n")

	help := "[enter] next  [esc] back"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderNewTaskInputView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# New Task"))
	b.WriteString("\n\n")

	if m.selectedProj != nil {
		b.WriteString(fmt.Sprintf("Project: %s\n", projectStyle.Render(m.selectedProj.Name)))
		b.WriteString(fmt.Sprintf("Path: %s\n", dimStyle.Render(m.selectedProj.Path)))
		b.WriteString(fmt.Sprintf("Base branch: %s\n", dimStyle.Render(m.spawnBranch)))
		b.WriteString("\n")
	}

	b.WriteString("Enter task description:\n")
	b.WriteString(inputStyle.Render(m.taskInput.View()))
	b.WriteString("\n\n")

	help := "[enter] submit  [esc] back"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderInterveneView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Intervene - Select Agent"))
	b.WriteString("\n\n")

	items := filterQueueByType(m.queueItems, queue.ItemTypeQuestion, queue.ItemTypeIdle)
	if len(items) == 0 {
		b.WriteString(dimStyle.Render("No agents need intervention"))
		b.WriteString("\n\n")
	} else {
		for i, item := range items {
			icon := getItemIcon(item.Type)
			style := queueItemStyle
			if i == m.selectedIndex {
				style = selectedItemStyle
			}
			line := fmt.Sprintf("%s %s: %s", icon, item.AgentID, item.Summary)
			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if m.selectedIndex >= 0 && m.selectedIndex < len(items) {
		selected := items[m.selectedIndex]
		b.WriteString(headerStyle.Render("## Details"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(selected.Details))
		b.WriteString("\n\n")
	}

	help := "[↑/↓/j/k] select  [enter] focus agent  [esc] back"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderInterveneInputView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Send Message to Agent"))
	b.WriteString("\n\n")

	if m.interveneAgent != nil {
		b.WriteString(fmt.Sprintf("Agent: %s\n", projectStyle.Render(m.interveneAgent.ID)))
		b.WriteString(fmt.Sprintf("Task: %s\n", dimStyle.Render(truncate(m.interveneAgent.Task, 50))))
		b.WriteString("\n")
	}

	b.WriteString("Type your message:\n")
	b.WriteString(inputStyle.Render(m.interveneInput.View()))
	b.WriteString("\n\n")

	b.WriteString(dimStyle.Render("This will send the text to the agent's terminal"))
	b.WriteString("\n\n")

	help := "[enter] send  [esc] back"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderReviewView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Review PR - Select"))
	b.WriteString("\n\n")

	items := filterQueueByType(m.queueItems, queue.ItemTypePRReady)
	if len(items) == 0 {
		b.WriteString(dimStyle.Render("No PRs ready for review"))
		b.WriteString("\n\n")
	} else {
		for i, item := range items {
			style := queueItemStyle
			if i == m.selectedIndex {
				style = selectedItemStyle
			}
			line := fmt.Sprintf("🔀 %s: %s", item.AgentID, item.Summary)
			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if m.selectedIndex >= 0 && m.selectedIndex < len(items) {
		selected := items[m.selectedIndex]
		b.WriteString(headerStyle.Render("## PR Details"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("Agent: %s\n", selected.AgentID))
		b.WriteString(fmt.Sprintf("URL: %s\n", selected.Details))
		b.WriteString("\n")
	}

	help := "[↑/↓/j/k] select  [a]pprove & merge  [c]omment  [r]eject  [b]rowser  [esc] back"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderConfirmMergeView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Confirm Cleanup"))
	b.WriteString("\n\n")

	if m.selectedAgent != nil {
		b.WriteString(fmt.Sprintf("Cleanup agent '%s'?\n", m.selectedAgent.ID))
		b.WriteString(fmt.Sprintf("Task: %s\n", m.selectedAgent.Task))
		b.WriteString(fmt.Sprintf("Branch: %s\n", m.selectedAgent.BranchName))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("This will remove the worktree and close the agent pane."))
		b.WriteString("\n\n")
	}

	help := "[y] confirm  [n] cancel"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderConfirmKillView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Kill Agent"))
	b.WriteString("\n\n")

	if len(m.agents) == 0 {
		b.WriteString(dimStyle.Render("No agents to kill"))
		b.WriteString("\n\n")
	} else {
		for i, a := range m.agents {
			style := queueItemStyle
			if i == m.selectedIndex {
				style = selectedItemStyle
			}
			statusStyle := getAgentStatusStyle(a.Status)
			line := fmt.Sprintf("%s: %s [%s]", a.ID, truncate(a.Task, MaxTaskDisplayLen), statusStyle.Render(string(a.Status)))
			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if m.selectedIndex >= 0 && m.selectedIndex < len(m.agents) {
		selected := m.agents[m.selectedIndex]
		b.WriteString(headerStyle.Render("## Agent Details"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("ID: %s\n", selected.ID))
		b.WriteString(fmt.Sprintf("Task: %s\n", selected.Task))
		b.WriteString(fmt.Sprintf("Branch: %s\n", dimStyle.Render(selected.BranchName)))
		b.WriteString(fmt.Sprintf("Worktree: %s\n", dimStyle.Render(selected.WorktreePath)))
		b.WriteString("\n")
	}

	help := "[↑/↓/j/k] select  [enter] kill  [esc] back"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderManageProjectsView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Manage Projects"))
	b.WriteString("\n\n")

	if len(m.projects) == 0 {
		b.WriteString(dimStyle.Render("No projects registered"))
		b.WriteString("\n\n")
	} else {
		for i, p := range m.projects {
			style := queueItemStyle
			if i == m.selectedIndex {
				style = selectedItemStyle
			}
			line := fmt.Sprintf("%s  %s", p.Name, dimStyle.Render(p.Path))
			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	help := "[a]dd project  [d]elete selected  [esc] back"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderAddProjectNameView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Add Project - Step 1/2"))
	b.WriteString("\n\n")

	b.WriteString("Enter project name:\n")
	b.WriteString(inputStyle.Render(m.projectForm.nameInput.View()))
	b.WriteString("\n\n")

	b.WriteString(dimStyle.Render("A short identifier for the project (e.g., 'myapp', 'backend')"))
	b.WriteString("\n\n")

	help := "[enter] next  [esc] cancel"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderAddProjectPathView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Add Project - Step 2/2"))
	b.WriteString("\n\n")

	b.WriteString(fmt.Sprintf("Project: %s\n\n", projectStyle.Render(m.newProjectName)))

	b.WriteString("Enter path to git repository:\n")
	b.WriteString(inputStyle.Render(m.projectForm.pathInput.View()))
	b.WriteString("\n\n")

	b.WriteString(dimStyle.Render("Full path to the repo root (e.g., '/home/user/projects/myapp')"))
	b.WriteString("\n\n")

	help := "[enter] create project  [esc] back"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderConfirmRemoveProjectView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Remove Project"))
	b.WriteString("\n\n")

	if m.selectedProj != nil {
		b.WriteString(fmt.Sprintf("Remove project '%s'?\n", projectStyle.Render(m.selectedProj.Name)))
		b.WriteString(fmt.Sprintf("Path: %s\n", dimStyle.Render(m.selectedProj.Path)))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("This only removes the registration, not the actual files."))
		b.WriteString("\n\n")
	}

	help := "[y] confirm  [n] cancel"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderConfirmKillSessionView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Kill Session"))
	b.WriteString("\n\n")

	b.WriteString(errorStyle.Render("WARNING: This will kill ALL agents and the entire tmux session!"))
	b.WriteString("\n\n")

	if len(m.agents) > 0 {
		b.WriteString(fmt.Sprintf("Active agents: %d\n", len(m.agents)))
		for _, a := range m.agents {
			b.WriteString(fmt.Sprintf("  • %s: %s\n", a.ID, truncate(a.Task, MaxTaskDisplayLen)))
		}
		b.WriteString("\n")
	}

	b.WriteString("Are you sure you want to kill everything?\n\n")

	help := "[y] yes, kill everything  [n] cancel"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func renderAgentStatus(status agent.Status) string {
	switch status {
	case agent.StatusRunning:
		return agentRunningStyle.Render("running")
	case agent.StatusKilling:
		return agentKillingStyle.Render("killing")
	case agent.StatusReady:
		return agentReadyStyle.Render("ready")
	case agent.StatusMerged:
		return agentMergedStyle.Render("merged")
	case agent.StatusFailed:
		return agentFailedStyle.Render("failed")
	default:
		return string(status)
	}
}

func getAgentStatusStyle(status agent.Status) lipgloss.Style {
	switch status {
	case agent.StatusSpawning:
		return agentSpawningStyle
	case agent.StatusRunning:
		return agentRunningStyle
	case agent.StatusKilling:
		return agentKillingStyle
	case agent.StatusReady:
		return agentReadyStyle
	case agent.StatusMerged:
		return agentMergedStyle
	case agent.StatusFailed:
		return agentFailedStyle
	default:
		return dimStyle
	}
}

func getItemIcon(itemType queue.ItemType) string {
	switch itemType {
	case queue.ItemTypeQuestion:
		return "❓"
	case queue.ItemTypePRReady:
		return "🔀"
	case queue.ItemTypeIdle:
		return "💤"
	default:
		return "•"
	}
}

func getItemStyle(itemType queue.ItemType) lipgloss.Style {
	switch itemType {
	case queue.ItemTypeQuestion:
		return questionStyle
	case queue.ItemTypePRReady:
		return prReadyStyle
	default:
		return lipgloss.NewStyle()
	}
}

func filterQueueByType(items []*queue.QueueItem, types ...queue.ItemType) []*queue.QueueItem {
	typeSet := make(map[queue.ItemType]bool)
	for _, t := range types {
		typeSet[t] = true
	}

	var filtered []*queue.QueueItem
	for _, item := range items {
		if typeSet[item.Type] {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func marquee(s string, maxWidth int, offset int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	sep := []rune(MarqueeSeparator)
	combined := append(append(make([]rune, 0, len(runes)+len(sep)), runes...), sep...)
	totalLen := len(combined)
	start := (offset / MarqueeTickRate) % totalLen
	result := make([]rune, maxWidth)
	for i := 0; i < maxWidth; i++ {
		result[i] = combined[(start+i)%totalLen]
	}
	return string(result)
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func spinner(frame int) string {
	return spinnerFrames[frame%SpinnerFrameCount]
}

func styledSpinner(frame int, style lipgloss.Style) string {
	return style.Render(spinnerFrames[frame%SpinnerFrameCount])
}

func renderJumpToAgentView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Jump to Agent"))
	b.WriteString("\n\n")

	if len(m.agents) == 0 {
		b.WriteString(dimStyle.Render("No agents running"))
		b.WriteString("\n\n")
	} else {
		for i, a := range m.agents {
			style := queueItemStyle
			if i == m.selectedIndex {
				style = selectedItemStyle
			}
			statusStyle := getAgentStatusStyle(a.Status)
			line := fmt.Sprintf("%s: %s [%s]", a.ID, truncate(a.Task, MaxTaskDisplayLen), statusStyle.Render(string(a.Status)))
			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if m.selectedIndex >= 0 && m.selectedIndex < len(m.agents) {
		selected := m.agents[m.selectedIndex]
		b.WriteString(headerStyle.Render("## Agent Details"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("ID: %s\n", selected.ID))
		b.WriteString(fmt.Sprintf("Task: %s\n", selected.Task))
		b.WriteString(fmt.Sprintf("Branch: %s\n", dimStyle.Render(selected.BranchName)))
		b.WriteString(fmt.Sprintf("Worktree: %s\n", dimStyle.Render(selected.WorktreePath)))
		b.WriteString("\n")
	}

	help := "[↑/↓/j/k] select  [enter] jump  [esc] back"
	b.WriteString(helpStyle.Render(help))

	return b.String()
}
