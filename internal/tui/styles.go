package tui

import "github.com/charmbracelet/lipgloss"

// Palette — single hero (accent 205 pink). Structural colors derived from a
// dark background ramp so sections blend instead of clashing. Semantic
// colors (ok/warn/err) reserved for genuine state signals, not decoration.
var (
	colorAccent = lipgloss.Color("205") // hero (pink)
	colorFg     = lipgloss.Color("252") // primary text
	colorDim    = lipgloss.Color("244") // meta / secondary
	colorFaint  = lipgloss.Color("240") // dividers / borders
	colorBgDeep = lipgloss.Color("234") // base bg tint (modal hints)
	colorOK     = lipgloss.Color("42")
	colorWarn   = lipgloss.Color("214")
	colorErr    = lipgloss.Color("196")

	colorBorder = colorFaint

	// Sidebar — no right border; tmux's pane separator already divides mux
	// from the agent pane next to it.
	styleSidebar = lipgloss.NewStyle().
			Padding(0, 1)

	styleSectionHeader = lipgloss.NewStyle().
				Foreground(colorDim).
				Bold(true)

	styleDivider = lipgloss.NewStyle().
			Foreground(colorFaint)

	styleListItemSel = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	styleSelMarker = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	// Hint/footer text used in modal overlays.
	styleStatus = lipgloss.NewStyle().
			Foreground(colorDim).
			Background(colorBgDeep).
			Padding(0, 1)

	// Dots (agent state)
	styleDot = map[string]lipgloss.Style{
		"running": lipgloss.NewStyle().Foreground(colorOK),
		"dead":    lipgloss.NewStyle().Foreground(colorErr),
	}
)
