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
	colorBgDeep = lipgloss.Color("234") // base bg tint (status, deepest)
	colorBgSoft = lipgloss.Color("236") // soft bg (sidebar section headers)
	colorBgRow  = lipgloss.Color("238") // selection row bg
	colorOK     = lipgloss.Color("42")
	colorWarn   = lipgloss.Color("214")
	colorErr    = lipgloss.Color("196")

	// legacy aliases kept so other files compile; retarget gradually.
	colorBorder   = colorFaint
	colorTabBg    = colorBgDeep
	colorSelected = colorAccent

	// Tab bar
	styleTabBar = lipgloss.NewStyle().
			Background(colorBgDeep)

	styleTab = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(colorDim).
			Background(colorBgDeep)

	styleTabActive = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(colorAccent).
			Background(colorBgDeep).
			Bold(true).
			Underline(true)

	// Sidebar — subtle right border so it reads as its own column
	styleSidebar = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(colorFaint).
			Padding(0, 1)

	styleSectionHeader = lipgloss.NewStyle().
				Foreground(colorDim).
				Bold(true)

	styleDivider = lipgloss.NewStyle().
			Foreground(colorFaint)

	// Body
	styleBody = lipgloss.NewStyle().
			PaddingLeft(1)

	// List rows
	styleListItem = lipgloss.NewStyle()

	styleListItemSel = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	styleRowSelected = lipgloss.NewStyle().
				Background(colorBgRow).
				Foreground(colorAccent).
				Bold(true)

	styleSelMarker = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	// Status bar — deep bg, structured zones
	styleStatus = lipgloss.NewStyle().
			Foreground(colorDim).
			Background(colorBgDeep).
			Padding(0, 1)

	styleStatusMode = lipgloss.NewStyle().
			Foreground(colorAccent).
			Background(colorBgDeep).
			Bold(true).
			Padding(0, 1)

	styleStatusMeta = lipgloss.NewStyle().
			Foreground(colorFg).
			Background(colorBgDeep)

	styleStatusMetaDim = lipgloss.NewStyle().
				Foreground(colorDim).
				Background(colorBgDeep)

	styleStatusKeys = lipgloss.NewStyle().
			Foreground(colorDim).
			Background(colorBgDeep).
			Padding(0, 1)

	styleStatusMsg = lipgloss.NewStyle().
			Foreground(colorWarn).
			Background(colorBgDeep).
			Padding(0, 1).
			Italic(true)

	// Dots (agent state)
	styleDot = map[string]lipgloss.Style{
		"running": lipgloss.NewStyle().Foreground(colorOK),
		"dead":    lipgloss.NewStyle().Foreground(colorErr),
	}
)
