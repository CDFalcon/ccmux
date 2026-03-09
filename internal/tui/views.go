package tui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/queue"
	"github.com/CDFalcon/ccmux/internal/updater"
	"github.com/CDFalcon/ccmux/internal/version"
)

type ViewState int

const (
	ViewMain ViewState = iota
	ViewSelectProject
	ViewNewTaskBranch
	ViewNewTaskBranchInput
	ViewNewTaskInput
	ViewIntervene
	ViewInterveneInput
	ViewReview
	ViewConfirmMerge
	ViewConfirmKill
	ViewManageProjects
	ViewAddProjectName
	ViewAddProjectPath
	ViewAddProjectFastWT
	ViewConfirmRemoveProject
	ViewEditProject
	ViewConfirmKillSession
	ViewAgentInfo
	ViewUpdate
	ViewHelp
)

const (
	MaxTaskDisplayLen     = 40
	MaxSummaryDisplayLen  = 50
	SpinnerFrameCount     = 6
	MarqueeTickRate       = 3
	MarqueeSeparator      = "  \u00b7\u00b7\u00b7  "
	MaxVisibleBranchItems = 10
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴"}

func renderFooter(help string, ctrlCPressed bool) string {
	footer := helpStyle.Render(help)
	if ctrlCPressed {
		footer += "\n" + dimStyle.Render("  Press Ctrl+C again to detach")
	}
	return footer
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
		"  " + c.Render("C") + w.Render("olby's ") + c.Render("C") + w.Render("laude ") + w.Render("Mu") + w.Render("ltiple") + w.Render("x") + w.Render("er"),
	}

	return strings.Join(lines, "\n")
}

func renderMainView(m model) string {
	var b strings.Builder

	b.WriteString(renderLogo())
	b.WriteString("\n")
	if m.updateAvailable && m.updateVersion != "" {
		b.WriteString("  " + dimStyle.Render(version.Version) + " - " + projectStyle.Render("[u]pdate to latest remote ("+m.updateVersion+")"))
	} else if !m.updateAvailable && m.updateVersion != "" {
		b.WriteString("  " + dimStyle.Render(version.Version+" - latest"))
	} else {
		b.WriteString("  " + dimStyle.Render(version.Version))
	}
	b.WriteString("\n\n")

	b.WriteString(headerStyle.Render(fmt.Sprintf("# Agents (%d)", len(m.agents))))
	b.WriteString("\n")
	if len(m.agents) == 0 {
		b.WriteString(dimStyle.Render("  No agents running"))
		b.WriteString("\n")
	} else {
		for _, a := range m.agents {
			statsStr := formatAgentOneLiner(m.agentResources[a.ID])

			if a.Status == agent.StatusCleaningUp {
				spin := styledSpinner(m.spinnerFrame, agentCleaningUpStyle)
				status := agentCleaningUpStyle.Render("cleaning up")
				line := fmt.Sprintf("  %s %s: %s [%s]%s", spin, a.ID, marquee(a.Task, MaxTaskDisplayLen, m.marqueeOffset), status, statsStr)
				b.WriteString(line)
				b.WriteString("\n")
			} else if a.Status == agent.StatusKilling {
				spin := styledSpinner(m.spinnerFrame, agentKillingStyle)
				status := agentKillingStyle.Render("killing")
				line := fmt.Sprintf("  %s %s: %s [%s]%s", spin, a.ID, marquee(a.Task, MaxTaskDisplayLen, m.marqueeOffset), status, statsStr)
				b.WriteString(line)
				b.WriteString("\n")
			} else if a.Status == agent.StatusSpawning {
				spin := styledSpinner(m.spinnerFrame, agentSpawningStyle)
				status := agentSpawningStyle.Render("spawning")
				line := fmt.Sprintf("  %s %s: %s [%s]%s", spin, a.ID, marquee(a.Task, MaxTaskDisplayLen, m.marqueeOffset), status, statsStr)
				b.WriteString(line)
				b.WriteString("\n")
			} else if a.Status == agent.StatusRunning {
				spin := styledSpinner(m.spinnerFrame, agentRunningStyle)
				status := agentRunningStyle.Render("running")
				line := fmt.Sprintf("  %s %s: %s [%s]%s", spin, a.ID, marquee(a.Task, MaxTaskDisplayLen, m.marqueeOffset), status, statsStr)
				b.WriteString(line)
				b.WriteString("\n")
			} else if a.Status == agent.StatusWaitingCI {
				icon := agentWaitingCIStyle.Render("⏳")
				status := agentWaitingCIStyle.Render("waiting on CI")
				line := fmt.Sprintf("  %s %s: %s [%s]%s", icon, a.ID, marquee(a.Task, MaxTaskDisplayLen, m.marqueeOffset), status, statsStr)
				b.WriteString(line)
				b.WriteString("\n")
			} else {
				status := renderAgentStatus(a.Status)
				line := fmt.Sprintf("  - %s: %s [%s]%s", a.ID, truncate(a.Task, MaxTaskDisplayLen), status, statsStr)
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("\n")

	b.WriteString(headerStyle.Render(fmt.Sprintf("# Quick action queue (%d items)", len(m.queueItems))))
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
			extras := dimStyle.Render(p.Path)
			if p.DefaultBaseBranch != "" {
				extras += "  " + dimStyle.Render("base:"+p.DefaultBaseBranch)
			}
			if p.CIWaitMinutes > 0 {
				extras += "  " + dimStyle.Render(fmt.Sprintf("ci:%dm", p.CIWaitMinutes))
			}
			if p.UseFastWorktrees {
				extras += "  " + dimStyle.Render("fast-wt")
			}
			b.WriteString(fmt.Sprintf("  - %s %s\n", projectStyle.Render(p.Name), extras))
		}
	}
	b.WriteString("\n")

	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %s", m.err.Error())))
		b.WriteString("\n\n")
	}

	help := helpFooter(ViewMain)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

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

	help := helpFooter(ViewSelectProject)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

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

	b.WriteString("Search:\n")
	b.WriteString(inputStyle.Render(m.branchFilter.View()))
	b.WriteString("\n\n")

	entries := m.branchEntries()

	renderScrollableList(&b, len(entries), m.selectedIndex, MaxVisibleBranchItems, func(i int, selected bool) string {
		entry := entries[i]
		style := queueItemStyle
		if selected {
			style = selectedItemStyle
		}
		var text string
		if entry.tag != "" {
			if selected {
				text = entry.tag + " " + entry.name
			} else {
				text = branchTagStyle.Render(entry.tag) + " " + entry.name
			}
		} else {
			text = entry.name
		}
		return style.Render(text)
	})

	b.WriteString("\n")

	help := helpFooter(ViewNewTaskBranch)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

	return b.String()
}

func renderNewTaskBranchInputView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# New Task - Specify Branch"))
	b.WriteString("\n\n")

	if m.selectedProj != nil {
		b.WriteString(fmt.Sprintf("Project: %s\n", projectStyle.Render(m.selectedProj.Name)))
		b.WriteString(fmt.Sprintf("Path: %s\n", dimStyle.Render(m.selectedProj.Path)))
		b.WriteString("\n")
	}

	b.WriteString("Enter branch name:\n")
	b.WriteString(inputStyle.Render(m.branchInput.View()))
	b.WriteString("\n\n")

	defaultBranch := "origin/master"
	if m.selectedProj != nil && m.selectedProj.DefaultBaseBranch != "" {
		defaultBranch = m.selectedProj.DefaultBaseBranch
	}
	b.WriteString(dimStyle.Render("Leave empty for " + defaultBranch))
	b.WriteString("\n\n")

	help := helpFooter(ViewNewTaskBranchInput)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

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

	help := helpFooter(ViewNewTaskInput)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

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

	help := helpFooter(ViewIntervene)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

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

	help := helpFooter(ViewInterveneInput)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

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

	help := helpFooter(ViewReview)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

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

	help := helpFooter(ViewConfirmMerge)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

	return b.String()
}

func renderConfirmKillView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Kill Agent"))
	b.WriteString("\n\n")
	b.WriteString(renderAgentSelector(m, "No agents to kill"))

	help := helpFooter(ViewConfirmKill)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

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

		if m.selectedIndex >= 0 && m.selectedIndex < len(m.projects) {
			selected := m.projects[m.selectedIndex]
			b.WriteString(headerStyle.Render("## Details"))
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("  Path:        %s\n", dimStyle.Render(selected.Path)))
			b.WriteString(fmt.Sprintf("  Base branch: %s\n", dimStyle.Render(selected.EffectiveBaseBranch())))
			b.WriteString(fmt.Sprintf("  CI wait:     %s\n", dimStyle.Render(fmt.Sprintf("%d min", selected.EffectiveCIWaitMinutes()))))
			fastWtStatus := "no"
			if selected.UseFastWorktrees {
				fastWtStatus = "yes (proj)"
			}
			b.WriteString(fmt.Sprintf("  Fast wt:     %s\n", dimStyle.Render(fastWtStatus)))
			b.WriteString("\n")
		}
	}

	help := helpFooter(ViewManageProjects)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

	return b.String()
}

func renderAddProjectNameView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Add Project - Step 1/3"))
	b.WriteString("\n\n")

	b.WriteString("Enter project name:\n")
	b.WriteString(inputStyle.Render(m.projectForm.nameInput.View()))
	b.WriteString("\n\n")

	b.WriteString(dimStyle.Render("A short identifier for the project (e.g., 'myapp', 'backend')"))
	b.WriteString("\n\n")

	help := helpFooter(ViewAddProjectName)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

	return b.String()
}

func renderAddProjectPathView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Add Project - Step 2/3"))
	b.WriteString("\n\n")

	b.WriteString(fmt.Sprintf("Project: %s\n\n", projectStyle.Render(m.newProjectName)))

	b.WriteString("Enter path to git repository:\n")
	b.WriteString(inputStyle.Render(m.projectForm.pathInput.View()))
	b.WriteString("\n\n")

	b.WriteString(dimStyle.Render("Full path to the repo root (e.g., '/home/user/projects/myapp')"))
	b.WriteString("\n\n")

	help := helpFooter(ViewAddProjectPath)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

	return b.String()
}

func renderAddProjectFastWTView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Add Project - Step 3/3"))
	b.WriteString("\n\n")

	b.WriteString(fmt.Sprintf("Project: %s\n", projectStyle.Render(m.newProjectName)))
	b.WriteString(fmt.Sprintf("Path:    %s\n\n", dimStyle.Render(m.newProjectPath)))

	b.WriteString("Use Andrew Paon's fast worktrees? (beta)\n\n")

	b.WriteString(dimStyle.Render("Uses 'proj' (github.com/Applied-Shared/proj) to create worktrees via"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("reflink copy instead of git worktree. Near-instant for large repos."))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Requires proj to be installed and the project imported with 'proj import'."))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("The path above should point to the proj project directory (containing .repo)."))
	b.WriteString("\n\n")

	help := helpFooter(ViewAddProjectFastWT)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

	return b.String()
}

func renderEditProjectView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Edit Project"))
	b.WriteString("\n\n")

	if m.selectedProj != nil {
		b.WriteString(fmt.Sprintf("Project: %s\n\n", projectStyle.Render(m.selectedProj.Name)))
	}

	fields := []struct {
		label string
		input string
	}{
		{"Path:", m.editProjectForm.pathInput.View()},
		{"Default base branch:", m.editProjectForm.baseBranchInput.View()},
		{"CI wait (minutes):", m.editProjectForm.ciWaitInput.View()},
		{"Fast worktrees (yes/no):", m.editProjectForm.fastWTInput.View()},
	}

	for i, f := range fields {
		marker := "  "
		if i == m.editProjectForm.focusIndex {
			marker = "> "
		}
		b.WriteString(fmt.Sprintf("%s%s\n", marker, f.label))
		b.WriteString(fmt.Sprintf("  %s\n", inputStyle.Render(f.input)))
		b.WriteString("\n")
	}

	help := helpFooter(ViewEditProject)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

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

	help := helpFooter(ViewConfirmRemoveProject)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

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

	help := helpFooter(ViewConfirmKillSession)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

	return b.String()
}

func renderAgentStatus(status agent.Status) string {
	switch status {
	case agent.StatusRunning:
		return agentRunningStyle.Render("running")
	case agent.StatusCleaningUp:
		return agentCleaningUpStyle.Render("cleaning up")
	case agent.StatusKilling:
		return agentKillingStyle.Render("killing")
	case agent.StatusReady:
		return agentReadyStyle.Render("ready")
	case agent.StatusWaitingCI:
		return agentWaitingCIStyle.Render("waiting on CI")
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
	case agent.StatusCleaningUp:
		return agentCleaningUpStyle
	case agent.StatusKilling:
		return agentKillingStyle
	case agent.StatusReady:
		return agentReadyStyle
	case agent.StatusWaitingCI:
		return agentWaitingCIStyle
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

func renderScrollableList(b *strings.Builder, totalItems, selectedIndex, maxVisible int, renderItem func(index int, selected bool) string) {
	if totalItems == 0 {
		return
	}

	visibleStart := 0
	visibleEnd := totalItems
	if totalItems > maxVisible {
		half := maxVisible / 2
		visibleStart = selectedIndex - half
		if visibleStart < 0 {
			visibleStart = 0
		}
		visibleEnd = visibleStart + maxVisible
		if visibleEnd > totalItems {
			visibleEnd = totalItems
			visibleStart = visibleEnd - maxVisible
		}
	}

	if visibleStart > 0 {
		b.WriteString(dimStyle.Render("  ↑ more"))
		b.WriteString("\n")
	}

	for i := visibleStart; i < visibleEnd; i++ {
		b.WriteString(renderItem(i, i == selectedIndex))
		b.WriteString("\n")
	}

	if visibleEnd < totalItems {
		b.WriteString(dimStyle.Render("  ↓ more"))
		b.WriteString("\n")
	}
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func wrapText(s string, width int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= width {
		return s
	}
	var lines []string
	for len(s) > 0 {
		if len(s) <= width {
			lines = append(lines, s)
			break
		}
		cut := strings.LastIndex(s[:width], " ")
		if cut <= 0 {
			cut = width
		}
		lines = append(lines, s[:cut])
		s = strings.TrimLeft(s[cut:], " ")
	}
	return strings.Join(lines, "\n        ")
}

func renderAgentSelector(m model, emptyMsg string) string {
	var b strings.Builder

	if len(m.agents) == 0 {
		b.WriteString(dimStyle.Render(emptyMsg))
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
		b.WriteString(fmt.Sprintf("ID:       %s\n", selected.ID))
		b.WriteString(fmt.Sprintf("Task:     %s\n", wrapText(selected.Task, 60)))
		b.WriteString(fmt.Sprintf("Branch:   %s\n", dimStyle.Render(selected.BranchName)))
		b.WriteString(fmt.Sprintf("Worktree: %s\n", dimStyle.Render(selected.WorktreePath)))
		if r, ok := m.agentResources[selected.ID]; ok {
			b.WriteString(fmt.Sprintf("CPU:      %s\n", fmt.Sprintf("%.0f%%", r.CPUPercent)))
			b.WriteString(fmt.Sprintf("Memory:   %s (%.0f%%)\n", formatBytes(r.MemBytes), r.MemPercent))
			b.WriteString(fmt.Sprintf("Disk:     %s\n", formatBytes(r.DiskBytes)))
			costLine := formatCost(r.CostUSD)
			if costLine != "" {
				b.WriteString(fmt.Sprintf("Cost:     %s (est.)\n", costLine))
			}
			tokenDetail := formatTokenDetail(r)
			if tokenDetail != "" {
				b.WriteString(fmt.Sprintf("Tokens:   %s\n", tokenDetail))
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

func formatAgentOneLiner(r *AgentResources) string {
	var parts []string
	resLine := formatResourceLine(r)
	if resLine != "" {
		parts = append(parts, resLine)
	}
	if r != nil {
		costLine := formatCost(r.CostUSD)
		if costLine != "" {
			parts = append(parts, "Cost: "+costLine)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + dimStyle.Render(strings.Join(parts, "  "))
}

func marquee(s string, maxWidth int, offset int) string {
	s = strings.ReplaceAll(s, "\n", " ")
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

func renderAgentInfoView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Info on Agent"))
	b.WriteString("\n\n")
	b.WriteString(renderAgentSelector(m, "No agents running"))

	help := helpFooter(ViewAgentInfo)
	b.WriteString(renderFooter(help, m.ctrlCPressed))

	return b.String()
}

func renderChangelog(b *strings.Builder, entries []updater.ChangelogEntry, selectedIndex int, loading bool, spinnerFrame int) {
	if loading {
		b.WriteString(fmt.Sprintf("\n%s Loading changelog...\n", styledSpinner(spinnerFrame, agentRunningStyle)))
		return
	}

	if len(entries) == 0 {
		return
	}

	b.WriteString("\n")
	b.WriteString(headerStyle.Render("## Changelog"))
	b.WriteString("\n")

	renderScrollableList(b, len(entries), selectedIndex, MaxVisibleBranchItems, func(i int, selected bool) string {
		entry := entries[i]
		style := queueItemStyle
		if selected {
			style = selectedItemStyle
		}
		return style.Render(fmt.Sprintf("#%d %s", entry.Number, entry.Title))
	})
}

func renderUpdateView(m model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("# Update"))
	b.WriteString("\n\n")

	b.WriteString(fmt.Sprintf("Current version: %s\n", projectStyle.Render(version.Version)))

	if m.updateChecking {
		b.WriteString(fmt.Sprintf("\n%s Checking for updates...\n", styledSpinner(m.spinnerFrame, agentRunningStyle)))
	} else if m.updateError != "" {
		b.WriteString(fmt.Sprintf("\n%s\n", errorStyle.Render(m.updateError)))
	} else if m.updateDownloading {
		b.WriteString(fmt.Sprintf("Latest version:  %s\n", projectStyle.Render(m.updateVersion)))
		renderChangelog(&b, m.changelogEntries, m.selectedIndex, false, m.spinnerFrame)
		pct := atomic.LoadInt64(m.downloadProgress)
		b.WriteString(fmt.Sprintf("\n%s Downloading update... %d%%\n", styledSpinner(m.spinnerFrame, agentRunningStyle), pct))
	} else if m.updateComplete {
		b.WriteString(fmt.Sprintf("Updated to:      %s\n", projectStyle.Render(m.updateVersion)))
		renderChangelog(&b, m.changelogEntries, m.selectedIndex, false, m.spinnerFrame)
		b.WriteString("\n")
		b.WriteString(agentReadyStyle.Render("Update complete!"))
		b.WriteString("\n")
	} else if m.updateAvailable {
		b.WriteString(fmt.Sprintf("Latest version:  %s\n", projectStyle.Render(m.updateVersion)))
		renderChangelog(&b, m.changelogEntries, m.selectedIndex, m.changelogLoading, m.spinnerFrame)
		b.WriteString("\nUpdate available. Install it?\n")
	} else {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("You are on the latest version."))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	if m.updateComplete {
		help := "[r]estart  [esc] back  [h]elp"
		b.WriteString(renderFooter(help, m.ctrlCPressed))
	} else if m.updateError != "" {
		help := "[esc] back"
		b.WriteString(renderFooter(help, m.ctrlCPressed))
	} else if m.updateAvailable && !m.updateDownloading {
		help := "[↑/↓/j/k] scroll  [y] install  [n] cancel  [h]elp"
		b.WriteString(renderFooter(help, m.ctrlCPressed))
	} else if !m.updateChecking && !m.updateDownloading {
		help := "[esc] back  [h]elp"
		b.WriteString(renderFooter(help, m.ctrlCPressed))
	}

	return b.String()
}
