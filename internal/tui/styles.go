package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent   = lipgloss.Color("205")
	colorDim      = lipgloss.Color("244")
	colorOK       = lipgloss.Color("42")
	colorWarn     = lipgloss.Color("214")
	colorErr      = lipgloss.Color("196")
	colorBorder   = lipgloss.Color("238")
	colorSelected = lipgloss.Color("63")
	colorTabBg    = lipgloss.Color("235")

	styleTabBar = lipgloss.NewStyle().
			Background(colorTabBg)

	styleTab = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(colorDim).
			Background(colorTabBg)

	styleTabActive = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(lipgloss.Color("230")).
			Background(colorSelected).
			Bold(true)

	styleSidebar = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(colorBorder).
			Padding(0, 1)

	styleBody = lipgloss.NewStyle().
			PaddingLeft(1)

	styleListItem = lipgloss.NewStyle()

	styleListItemSel = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	styleSelMarker = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	styleStatus = lipgloss.NewStyle().
			Foreground(colorDim).
			Padding(0, 1)

	styleDot = map[string]lipgloss.Style{
		"running": lipgloss.NewStyle().Foreground(colorOK),
		"idle":    lipgloss.NewStyle().Foreground(colorDim),
		"dead":    lipgloss.NewStyle().Foreground(colorErr),
		"wait":    lipgloss.NewStyle().Foreground(colorWarn),
	}
)
