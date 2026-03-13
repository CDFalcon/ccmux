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

var viewTitles = map[ViewState]string{
	ViewMain:                 "Main",
	ViewSelectProject:        "Select Project",
	ViewNewTaskBranch:        "New Task - Base Branch",
	ViewNewTaskBranchInput:   "New Task - Specify Branch",
	ViewNewTaskInput:         "New Task",
	ViewNewTaskWorktreeName:  "New Task - Branch Name",
	ViewIntervene:            "Intervene",
	ViewInterveneInput:       "Send Message",
	ViewReview:               "Review PR",
	ViewConfirmMerge:         "Confirm Cleanup",
	ViewConfirmKill:          "Kill Agent",
	ViewManageProjects:       "Manage Projects",
	ViewAddProjectName:       "Add Project (Name)",
	ViewAddProjectPath:       "Add Project (Path)",
	ViewAddProjectFastWT:     "Add Project (Fast Worktrees)",
	ViewEditProject:          "Edit Project",
	ViewConfirmRemoveProject: "Remove Project",
	ViewConfirmKillSession:   "Kill Session",
	ViewAgentInfo:            "Info on Agent",
	ViewUpdate:               "Update",
	ViewProjImporting:        "Setting Up Project",
	ViewManagePrompts:        "Manage Prompts",
	ViewAddPromptName:        "Add Prompt (Name)",
	ViewAddPromptContent:     "Add Prompt (Content)",
	ViewAddPromptDefault:     "Add Prompt (Default)",
	ViewAddPromptProjects:   "Add Prompt (Projects)",
	ViewEditPrompt:           "Edit Prompt",
	ViewConfirmRemovePrompt:  "Remove Prompt",
	ViewNewTaskSelectPrompts: "New Task - Select Prompts",
}

var viewHelpCommands = map[ViewState][]helpCommand{
	ViewMain: {
		{FooterText: "[q]uick action", Description: "Pop and act on the next quick action in the queue (e.g. PR review)"},
		{FooterText: "[n]ew task", Description: "Spawn a new Claude agent"},
		{FooterText: "[i]nfo on agent", Description: "View agent details and jump to its tmux window"},
		{FooterText: "[k]ill agent", Description: "Terminate a running agent"},
		{FooterText: "[p]rompts", Description: "Manage custom agent prompts"},
		{FooterText: "[P]rojects", Description: "Manage registered projects"},
		{FooterText: "[K]ill session", Description: "Kill all agents and the tmux session"},
		{FooterText: "[u]pdate", Description: "Check for and install updates"},
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
		{FooterText: "[enter] next", Description: "Continue to prompt selection"},
		{FooterText: "[esc] back", Description: "Return to branch selection"},
	},
	ViewNewTaskWorktreeName: {
		{FooterText: "[enter] spawn", Description: "Spawn the agent (name is optional)"},
		{FooterText: "[esc] back", Description: "Return to task description"},
	},
	ViewIntervene: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the agent list"},
		{FooterText: "[enter] focus agent", Description: "Send input to the selected agent"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewInterveneInput: {
		{FooterText: "[enter] send", Description: "Send the message to the agent"},
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
		{FooterText: "[enter] select", Description: "Select agent to kill (shows confirmation)"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewManageProjects: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the project list"},
		{FooterText: "[a]dd project", Description: "Register a new project"},
		{FooterText: "[enter] edit selected", Description: "Edit the selected project"},
		{FooterText: "[d]elete selected", Description: "Remove the selected project"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewEditProject: {
		{FooterText: "[tab] next field", Description: "Move to the next field"},
		{FooterText: "[shift+tab] prev field", Description: "Move to the previous field"},
		{FooterText: "[enter] save", Description: "Save changes"},
		{FooterText: "[esc] cancel", Description: "Cancel and return to project management"},
	},
	ViewAddProjectName: {
		{FooterText: "[enter] next", Description: "Proceed to path entry"},
		{FooterText: "[esc] cancel", Description: "Cancel and return to project management"},
	},
	ViewAddProjectPath: {
		{FooterText: "[enter] create project", Description: "Create the project registration"},
		{FooterText: "[esc] back", Description: "Return to name entry"},
	},
	ViewAddProjectFastWT: {
		{FooterText: "[y]es", Description: "Enable fast worktrees (proj) for this project"},
		{FooterText: "[n]o", Description: "Use standard git worktrees"},
		{FooterText: "[esc] back", Description: "Return to path entry"},
	},
	ViewConfirmRemoveProject: {
		{FooterText: "[y]es", Description: "Confirm project removal"},
		{FooterText: "[n]o", Description: "Cancel and go back"},
	},
	ViewProjImporting: {
		{FooterText: "[esc] back", Description: "Return to project management (import continues)"},
	},
	ViewManagePrompts: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the prompt list"},
		{FooterText: "[a]dd prompt", Description: "Create a new prompt"},
		{FooterText: "[enter] edit selected", Description: "Edit the selected prompt"},
		{FooterText: "[d]elete selected", Description: "Remove the selected prompt"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewAddPromptName: {
		{FooterText: "[enter] next", Description: "Proceed to content entry"},
		{FooterText: "[esc] cancel", Description: "Cancel and return to prompt management"},
	},
	ViewAddPromptContent: {
		{FooterText: "[enter] next", Description: "Proceed to default setting"},
		{FooterText: "[esc] back", Description: "Return to name entry"},
	},
	ViewAddPromptDefault: {
		{FooterText: "[y]es", Description: "Set as default and proceed"},
		{FooterText: "[n]o", Description: "Skip default and proceed"},
		{FooterText: "[esc] back", Description: "Return to content entry"},
	},
	ViewAddPromptProjects: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the project list"},
		{FooterText: "[space] toggle", Description: "Enable or disable for project"},
		{FooterText: "[enter] create prompt", Description: "Create the prompt"},
		{FooterText: "[esc] back", Description: "Return to default setting"},
	},
	ViewEditPrompt: {
		{FooterText: "[tab] next field", Description: "Move to the next field"},
		{FooterText: "[shift+tab] prev field", Description: "Move to the previous field"},
		{FooterText: "[enter] save", Description: "Save changes"},
		{FooterText: "[esc] cancel", Description: "Cancel and return to prompt management"},
	},
	ViewConfirmRemovePrompt: {
		{FooterText: "[y]es", Description: "Confirm prompt removal"},
		{FooterText: "[n]o", Description: "Cancel and go back"},
	},
	ViewNewTaskSelectPrompts: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the prompt list"},
		{FooterText: "[space] toggle", Description: "Enable or disable the selected prompt"},
		{FooterText: "[enter] continue", Description: "Continue to worktree name step"},
		{FooterText: "[esc] back", Description: "Return to task description"},
	},
	ViewConfirmKillSession: {
		{FooterText: "[y]es, kill everything", Description: "Kill all agents and the tmux session"},
		{FooterText: "[n]o", Description: "Cancel and go back"},
	},
	ViewAgentInfo: {
		{FooterText: "[↑/↓/j/k] select", Description: "Navigate the agent list"},
		{FooterText: "[enter] jump to agent", Description: "Jump to the selected agent's window"},
		{FooterText: "[esc] back", Description: "Return to main view"},
	},
	ViewUpdate: {
		{FooterText: "[↑/↓/j/k] scroll", Description: "Navigate the changelog"},
		{FooterText: "[c]onfirm", Description: "Download and install the update"},
		{FooterText: "[b]eta / un[b]eta", Description: "Toggle beta update channel (pre-release builds on every PR merge)"},
		{FooterText: "[r]estart", Description: "Restart after update completes"},
		{FooterText: "[esc] cancel", Description: "Cancel and return to main view"},
	},
}

func withHelpKey(help string) string {
	if help == "" {
		return "[F1] help"
	}
	return help + "  [F1] help"
}

func helpFooter(view ViewState) string {
	commands := viewHelpCommands[view]
	parts := make([]string, 0, len(commands))
	for _, cmd := range commands {
		if !cmd.HideFromFooter {
			parts = append(parts, cmd.FooterText)
		}
	}
	return withHelpKey(strings.Join(parts, "  "))
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
		helpEntry := "[F1] help"
		if w := lipgloss.Width(helpEntry); w > maxWidth {
			maxWidth = w
		}

		for _, cmd := range commands {
			w := lipgloss.Width(cmd.FooterText)
			padding := strings.Repeat(" ", maxWidth-w+4)
			b.WriteString(fmt.Sprintf("  %s%s%s\n", cmd.FooterText, padding, dimStyle.Render(cmd.Description)))
		}

		w := lipgloss.Width(helpEntry)
		padding := strings.Repeat(" ", maxWidth-w+4)
		b.WriteString(fmt.Sprintf("  %s%s%s\n", helpEntry, padding, dimStyle.Render("Show this help screen")))
	}

	b.WriteString("\n")
	help := "[esc] back"
	b.WriteString(renderFooter(help, m.ctrlCPressed))

	return b.String()
}
