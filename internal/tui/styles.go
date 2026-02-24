package tui

import "github.com/charmbracelet/lipgloss"

const (
	ColorRoyalBlue   = "63"
	ColorLightBlue   = "117"
	ColorSilverWhite = "252"
	ColorDimGray     = "245"
	ColorGreen       = "46"
	ColorYellow      = "226"
	ColorRed         = "196"
	ColorOrange      = "208"
)

var (
	royalBlue   = lipgloss.Color(ColorRoyalBlue)
	silverWhite = lipgloss.Color(ColorSilverWhite)
	dimGray     = lipgloss.Color(ColorDimGray)

	logoStyle = lipgloss.NewStyle().
			Foreground(silverWhite).
			Bold(true)

	logoCStyle = lipgloss.NewStyle().
			Foreground(royalBlue).
			Bold(true)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(royalBlue).
			MarginBottom(1)

	projectStyle = lipgloss.NewStyle().
			Foreground(royalBlue).
			Bold(true)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(royalBlue)

	queueItemStyle = lipgloss.NewStyle().
			PaddingLeft(2).
			Foreground(silverWhite)

	selectedItemStyle = lipgloss.NewStyle().
				PaddingLeft(2).
				Foreground(royalBlue).
				Bold(true)

	agentRunningStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGreen))

	agentReadyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorYellow))

	agentMergedStyle = lipgloss.NewStyle().
				Foreground(royalBlue)

	agentFailedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorRed))

	agentSpawningStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorYellow))

	agentKillingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorRed))

	agentCleaningUpStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorLightBlue))

	helpStyle = lipgloss.NewStyle().
			Foreground(dimGray).
			MarginTop(1)

	inputStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(royalBlue).
			Padding(0, 1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorRed)).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(dimGray)

	questionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorYellow))

	prReadyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorGreen))

)
