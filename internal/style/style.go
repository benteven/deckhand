package style

import "charm.land/lipgloss/v2"

var (
	White  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	Gray   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7c8291"))
	Dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#3a3f4b"))
	Orange = lipgloss.NewStyle().Foreground(lipgloss.Color("#d97757"))
	Yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#e5b85c"))
	Green  = lipgloss.NewStyle().Foreground(lipgloss.Color("#8cc97a"))
	Red    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e06c75"))
	Blue   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7fb3ff"))
	Purple = lipgloss.NewStyle().Foreground(lipgloss.Color("#b39ddb"))
	Rule   = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
)

func SelectionBar(selected bool) string {
	if !selected {
		return "  "
	}
	return Orange.Render("▌") + " "
}
