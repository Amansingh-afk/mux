package diffview

import "github.com/charmbracelet/lipgloss"

// Palette — mirrors mux's TUI palette exactly. Single hero accent (205 pink),
// soft add/del hues so a wall of diff stays readable, semantic ok/warn/err
// reserved for the merge-prediction line and status glyphs.
var (
	cAccent = lipgloss.Color("205") // hero (pink)
	cFg     = lipgloss.Color("252") // primary text
	cDim    = lipgloss.Color("244") // meta / secondary
	cFaint  = lipgloss.Color("240") // rules / gutters
	cOK     = lipgloss.Color("42")  // merges clean
	cWarn   = lipgloss.Color("214") // untracked tag / soft conflict
	cErr    = lipgloss.Color("196") // hard conflict
	cAdd    = lipgloss.Color("114") // soft green additions
	cDel    = lipgloss.Color("174") // soft red deletions

	sAccent  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sFg      = lipgloss.NewStyle().Foreground(cFg)
	sDim     = lipgloss.NewStyle().Foreground(cDim)
	sFaint   = lipgloss.NewStyle().Foreground(cFaint)
	sOK      = lipgloss.NewStyle().Foreground(cOK)
	sWarn    = lipgloss.NewStyle().Foreground(cWarn)
	sErr     = lipgloss.NewStyle().Foreground(cErr)
	sAdd     = lipgloss.NewStyle().Foreground(cAdd)
	sDel     = lipgloss.NewStyle().Foreground(cDel)
	sAddDim  = lipgloss.NewStyle().Foreground(cAdd).Faint(true)
	sDelDim  = lipgloss.NewStyle().Foreground(cDel).Faint(true)
	sPath    = lipgloss.NewStyle().Foreground(cFg).Bold(true)
	sSelPath = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
)
