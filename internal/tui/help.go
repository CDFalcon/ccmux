package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type helpCommand struct {
	FooterText     string
	Description    string
	HideFromFooter bool
}

var inputViews = map[ViewState]bool{
	ViewNewTaskBranch:      true,
	ViewNewTaskBranchInput: true,
	ViewNewTaskInput:       true,
	ViewInterveneInput:     true,
	ViewAddProjectName:     true,
	ViewAddProjectPath:     true,
}

var viewTitles = map[ViewState]string{
	ViewMain:                 "Main",
	ViewSelectProject:        "Select Project",
	ViewNewTaskBranch:        "New Task - Base Branch",
	ViewNewTaskBranchInput:   "New Task - Specify Branch",
	ViewNewTaskInput:         "New Task",
	ViewIntervene:            "Intervene",
	ViewInterveneInput:       "Send Message",
	ViewReview:               "Review PR",
	ViewConfirmMerge:         "Confirm Cleanup",
	ViewConfirmKill:          "Kill Agent",
	ViewManageProjects:       "Manage Projects",
	ViewAddProjectName:       "Add Project (Name)",
	ViewAddProjectPath:       "Add Project (Path)",
	ViewConfirmRemoveProject: "Remove Project",
	ViewConfirmKillSession:   "Kill Session",
	ViewJumpToAgent:          "Jump to Agent",
	ViewUpdate:               "Update",
}

var viewHelpCommands = map[ViewState][]helpCommand{
	ViewMain: {
		{FooterText: "[q]uick action", Description: "Pop and act on the next quick action in the queue (e.g. PR review)"},
		{FooterText: "[n]ew task", Description: "Spawn a new Claude agent"},
		{FooterText: "[j]ump to agent", Description: "Switch to an agent's tmux window"},
		{FooterText: "[k]ill agent", Description: "Terminate a running agent"},
		{FooterText: "[p]rojects", Description: "Manage registered projects"},
		{FooterText: "[K]ill session", Description: "Kill all agents and the tmux session"},
		{FooterText: "[u]pdate", Description: "Check for and install updates", HideFromFooter: true},
	},
	ViewSelectProject: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the project list"},
		{FooterText: "[enter] choose", Description: "Choose the selected project"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewNewTaskBranch: {
		{FooterText: "[↑/↓] select", Description: "Navigate the branch list"},
		{FooterText: "[enter] choose", Description: "Choose the selected branch"},
		{FooterText: "[esc] back", Description: "Go back or clear search filter"},
	},
	ViewNewTaskBranchInput: {
		{FooterText: "[enter] confirm", Description: "Use the entered branch name"},
		{FooterText: "[esc] back", Description: "Return to branch selection"},
	},
	ViewNewTaskInput: {
		{FooterText: "[enter] submit", Description: "Submit the task and spawn an agent"},
		{FooterText: "[shift+enter] new line", Description: "Add a line break in the description"},
		{FooterText: "[esc] back", Description: "Return to branch selection"},
	},
	ViewIntervene: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the agent list"},
		{FooterText: "[enter] focus agent", Description: "Send input to the selected agent"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewInterveneInput: {
		{FooterText: "[enter] send", Description: "Send the message to the agent"},
		{FooterText: "[shift+enter] new line", Description: "Add a line break in the message"},
		{FooterText: "[esc] back", Description: "Return to agent selection"},
	},
	ViewReview: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the PR list"},
		{FooterText: "[a]ccept", Description: "Accept the PR and clean up the agent"},
		{FooterText: "[c]omment", Description: "Resume agent to address PR comments"},
		{FooterText: "[r]eject", Description: "Reject the PR and clean up"},
		{FooterText: "[b]rowser", Description: "Open the PR in a web browser"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewConfirmMerge: {
		{FooterText: "[y]es", Description: "Confirm cleanup of the agent"},
		{FooterText: "[n]o", Description: "Cancel and go back"},
	},
	ViewConfirmKill: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the agent list"},
		{FooterText: "[enter] kill", Description: "Kill the selected agent"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewManageProjects: {
		{FooterText: "[a]dd project", Description: "Register a new project"},
		{FooterText: "[d]elete selected", Description: "Remove the selected project"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewAddProjectName: {
		{FooterText: "[enter] next", Description: "Proceed to path entry"},
		{FooterText: "[esc] cancel", Description: "Cancel and return to project management"},
	},
	ViewAddProjectPath: {
		{FooterText: "[enter] create project", Description: "Create the project registration"},
		{FooterText: "[esc] back", Description: "Return to name entry"},
	},
	ViewConfirmRemoveProject: {
		{FooterText: "[y]es", Description: "Confirm project removal"},
		{FooterText: "[n]o", Description: "Cancel and go back"},
	},
	ViewConfirmKillSession: {
		{FooterText: "[y]es, kill everything", Description: "Kill all agents and the tmux session"},
		{FooterText: "[n]o", Description: "Cancel and go back"},
	},
	ViewJumpToAgent: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the agent list"},
		{FooterText: "[enter] jump", Description: "Jump to the selected agent's window"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewUpdate: {
		{FooterText: "[↑/↓/j/k] scroll", Description: "Navigate the changelog"},
		{FooterText: "[y] install", Description: "Download and install the update"},
		{FooterText: "[n] cancel", Description: "Cancel and return to main view"},
		{FooterText: "[r]estart", Description: "Restart after update completes"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
}

func isInputView(v ViewState) bool {
	return inputViews[v]
}

func helpFooter(view ViewState) string {
	commands := viewHelpCommands[view]
	parts := make([]string, 0, len(commands)+1)
	for _, cmd := range commands {
		if !cmd.HideFromFooter {
			parts = append(parts, cmd.FooterText)
		}
	}
	if !isInputView(view) {
		parts = append(parts, "[h]elp")
	}
	return strings.Join(parts, "  ")
}

func renderHelpView(m model) string {
	var b strings.Builder

	title := "Help"
	if name, ok := viewTitles[m.previousView]; ok {
		title = fmt.Sprintf("Help - %s", name)
	}
	b.WriteString(titleStyle.Render("# " + title))
	b.WriteString("\n\n")

	commands := viewHelpCommands[m.previousView]
	if len(commands) == 0 {
		b.WriteString(dimStyle.Render("  No commands available for this view"))
		b.WriteString("\n")
	} else {
		maxWidth := 0
		for _, cmd := range commands {
			if w := lipgloss.Width(cmd.FooterText); w > maxWidth {
				maxWidth = w
			}
		}
		helpEntry := "[h]elp"
		if !isInputView(m.previousView) {
			if w := lipgloss.Width(helpEntry); w > maxWidth {
				maxWidth = w
			}
		}

		for _, cmd := range commands {
			w := lipgloss.Width(cmd.FooterText)
			padding := strings.Repeat(" ", maxWidth-w+4)
			b.WriteString(fmt.Sprintf("  %s%s%s\n", cmd.FooterText, padding, dimStyle.Render(cmd.Description)))
		}

		if !isInputView(m.previousView) {
			w := lipgloss.Width(helpEntry)
			padding := strings.Repeat(" ", maxWidth-w+4)
			b.WriteString(fmt.Sprintf("  %s%s%s\n", helpEntry, padding, dimStyle.Render("Show this help screen")))
		}
	}

	b.WriteString("\n")
	help := "[esc] back"
	b.WriteString(renderFooter(help, m.ctrlCPressed))

	return b.String()
}
