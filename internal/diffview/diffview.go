// Package diffview is mux's full-screen review pager. It renders the diff
// between a base repo and an agent's worktree branch — committed, uncommitted,
// and untracked changes — plus a merge prediction, in the right tmux pane.
package diffview

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Run gathers the diff and runs the full-screen pager until quit.
// base = base repo path, wt = agent worktree path, branch = agent branch.
func Run(base, wt, branch string) error {
	d, err := gather(base, wt, branch)
	if err != nil {
		return err
	}
	p := tea.NewProgram(newModel(d), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = p.Run()
	return err
}

const (
	headerHeight = 3 // summary, merge line, rule
	footerHeight = 1
	maxListRows  = 6
	wheelStep    = 3
)

type focusArea int

const (
	focusBody focusArea = iota
	focusList
)

type model struct {
	d      *data
	width  int
	height int

	body        []string // styled, width-truncated body lines
	fileOffsets []int    // file index → body line index of its header

	offset int // body scroll
	cursor int // file list cursor
	focus  focusArea
}

func newModel(d *data) *model {
	return &model{d: d}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) listRows() int {
	if len(m.d.files) == 0 {
		return 0
	}
	n := len(m.d.files)
	if n > maxListRows {
		n = maxListRows
	}
	return n + 1 // trailing rule
}

func (m *model) bodyHeight() int {
	h := m.height - headerHeight - m.listRows() - footerHeight
	if h < 1 {
		h = 1
	}
	return h
}

func (m *model) maxOffset() int {
	max := len(m.body) - m.bodyHeight()
	if max < 0 {
		max = 0
	}
	return max
}

func (m *model) clampOffset() {
	if m.offset > m.maxOffset() {
		m.offset = m.maxOffset()
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *model) scroll(delta int) {
	m.offset += delta
	m.clampOffset()
}

// currentFile derives the file the viewport top is inside.
func (m *model) currentFile() int {
	cur := 0
	for i, off := range m.fileOffsets {
		if off <= m.offset {
			cur = i
		}
	}
	return cur
}

func (m *model) jumpToFile(i int) {
	if i < 0 || i >= len(m.fileOffsets) {
		return
	}
	m.cursor = i
	m.offset = m.fileOffsets[i]
	m.clampOffset()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rebuild()
		m.clampOffset()
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scroll(-wheelStep)
		case tea.MouseButtonWheelDown:
			m.scroll(wheelStep)
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "tab":
			if m.focus == focusBody {
				m.focus = focusList
				m.cursor = m.currentFile()
			} else {
				m.focus = focusBody
			}
		case "j", "down":
			if m.focus == focusList {
				m.moveCursor(1)
			} else {
				m.scroll(1)
			}
		case "k", "up":
			if m.focus == focusList {
				m.moveCursor(-1)
			} else {
				m.scroll(-1)
			}
		case "J":
			m.moveCursor(1)
			m.jumpToFile(m.cursor)
		case "K":
			m.moveCursor(-1)
			m.jumpToFile(m.cursor)
		case "d":
			m.scroll(m.bodyHeight() / 2)
		case "u":
			m.scroll(-m.bodyHeight() / 2)
		case " ", "pgdown":
			m.scroll(m.bodyHeight())
		case "b", "pgup":
			m.scroll(-m.bodyHeight())
		case "g", "home":
			m.offset = 0
		case "G", "end":
			m.offset = m.maxOffset()
		case "n":
			m.jumpToFile(m.currentFile() + 1)
		case "N":
			m.jumpToFile(m.currentFile() - 1)
		case "enter", "l":
			if m.focus == focusList {
				m.jumpToFile(m.cursor)
				m.focus = focusBody
			}
		}
	}
	return m, nil
}

func (m *model) moveCursor(delta int) {
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.d.files)-1 {
		m.cursor = len(m.d.files) - 1
	}
}

func (m *model) View() string {
	if m.width == 0 {
		return ""
	}
	if len(m.d.files) == 0 {
		return m.emptyView()
	}
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString(m.listView())
	b.WriteString(m.bodyView())
	b.WriteString(m.footerView())
	return b.String()
}

func (m *model) headerView() string {
	adds, dels := 0, 0
	uncommitted := 0
	for _, f := range m.d.files {
		adds += f.Adds
		dels += f.Dels
		if f.Section != SectionCommitted {
			uncommitted++
		}
	}
	line1 := sAccent.Render(m.d.branch) + sDim.Render(" · "+summaryText(len(m.d.files), adds, dels, m.d.ahead, uncommitted))

	var line2 string
	switch {
	case !m.d.merge.Supported:
		line2 = sDim.Render("merge prediction unavailable")
	case len(m.d.merge.Blocked) > 0:
		line2 = sErr.Render("⚠ merge blocked: untracked in base: ") + sWarn.Render(strings.Join(m.d.merge.Blocked, ", "))
	case m.d.merge.Clean:
		line2 = sOK.Render("merges clean ✓")
	case len(m.d.merge.Conflicts) > 0:
		line2 = sErr.Render("⚠ will conflict: ") + sWarn.Render(strings.Join(m.d.merge.Conflicts, ", "))
	default:
		line2 = sWarn.Render("⚠ may conflict")
	}

	rule := sFaint.Render(strings.Repeat("─", m.width))
	return truncLine(line1, m.width) + "\n" + truncLine(line2, m.width) + "\n" + rule + "\n"
}

func (m *model) listView() string {
	files := m.d.files
	rows := m.listRows() - 1
	if rows <= 0 {
		return ""
	}
	top := 0
	if len(files) > rows {
		top = m.cursor - rows/2
		if top < 0 {
			top = 0
		}
		if top > len(files)-rows {
			top = len(files) - rows
		}
	}
	var b strings.Builder
	for i := top; i < top+rows && i < len(files); i++ {
		b.WriteString(truncLine(m.fileRow(files[i], i == m.cursor), m.width))
		b.WriteByte('\n')
	}
	b.WriteString(sFaint.Render(strings.Repeat("─", m.width)))
	b.WriteByte('\n')
	return b.String()
}

func (m *model) fileRow(f FileDiff, selected bool) string {
	marker := " "
	if selected {
		marker = sAccent.Render("▍")
	}
	var glyph string
	switch {
	case f.Section == SectionUntracked:
		glyph = sWarn.Render("+")
	case f.Status == 'A':
		glyph = sAdd.Render("+")
	case f.Status == 'D':
		glyph = sDel.Render("−")
	default:
		glyph = sDim.Render("~")
	}
	pathStyle := sFg
	if selected {
		pathStyle = sSelPath
	}

	counts := countsPlain(f)
	// layout: marker glyph space path ... counts (right aligned)
	pad := m.width - 3 - ansi.StringWidth(f.Path) - ansi.StringWidth(counts) - 1
	if pad < 1 {
		pad = 1
	}
	return marker + glyph + " " + pathStyle.Render(f.Path) +
		strings.Repeat(" ", pad) + countsStyled(f)
}

func countsPlain(f FileDiff) string {
	if f.Binary {
		return "bin"
	}
	return fmt.Sprintf("+%d −%d", f.Adds, f.Dels)
}

func countsStyled(f FileDiff) string {
	if f.Binary {
		return sDim.Render("bin")
	}
	return sAddDim.Render(fmt.Sprintf("+%d", f.Adds)) + " " + sDelDim.Render(fmt.Sprintf("−%d", f.Dels))
}

func (m *model) bodyView() string {
	h := m.bodyHeight()
	var b strings.Builder
	for i := m.offset; i < m.offset+h; i++ {
		if i < len(m.body) {
			b.WriteString(m.body[i])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (m *model) footerView() string {
	hints := "j/k scroll · n/N file · tab list · q close"
	pct := "100%"
	if max := m.maxOffset(); max > 0 {
		pct = fmt.Sprintf("%d%%", m.offset*100/max)
	}
	pad := m.width - ansi.StringWidth(hints) - ansi.StringWidth(pct) - 2
	if pad < 1 {
		pad = 1
	}
	return truncLine(sDim.Render(hints)+strings.Repeat(" ", pad)+sDim.Render(pct)+" ", m.width)
}

func (m *model) emptyView() string {
	msg := sDim.Render("no changes yet") + "\n\n" + sFaint.Render("q close")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, msg)
}

// rebuild renders the body lines for the current width.
func (m *model) rebuild() {
	m.body = m.body[:0]
	m.fileOffsets = m.fileOffsets[:0]
	if m.width == 0 {
		return
	}
	prevSection := Section(-1)
	for _, f := range m.d.files {
		if f.Section != prevSection {
			prevSection = f.Section
			if len(m.body) > 0 {
				m.body = append(m.body, "")
			}
			m.body = append(m.body, m.sectionTitle(f.Section))
		}
		m.fileOffsets = append(m.fileOffsets, len(m.body))
		m.body = append(m.body, m.fileHeader(f))
		if f.Binary {
			m.body = append(m.body, sDim.Render("  binary file"))
		}
		for _, h := range f.Hunks {
			m.body = append(m.body, truncLine(sDim.Render(h.Header), m.width))
			for _, ln := range h.Lines {
				m.body = append(m.body, m.diffLine(ln))
			}
		}
		m.body = append(m.body, "")
	}
}

func (m *model) sectionTitle(s Section) string {
	label := s.String()
	style := sDim
	if s == SectionUntracked {
		style = sWarn
	}
	fill := m.width - ansi.StringWidth(label) - 4
	if fill < 0 {
		fill = 0
	}
	return sFaint.Render("── ") + style.Render(label) + " " + sFaint.Render(strings.Repeat("─", fill))
}

func (m *model) fileHeader(f FileDiff) string {
	path := f.Path
	if f.Status == 'R' && f.OldPath != "" && f.OldPath != f.Path {
		path = f.OldPath + " → " + f.Path
	}
	counts := countsPlain(f)
	fill := m.width - ansi.StringWidth(path) - ansi.StringWidth(counts) - 4
	if fill < 0 {
		fill = 0
	}
	return truncLine(
		sFaint.Render("─ ")+sPath.Render(path)+" "+countsStyled(f)+" "+sFaint.Render(strings.Repeat("─", fill)),
		m.width)
}

func (m *model) diffLine(ln Line) string {
	old, new_ := "    ", "    "
	if ln.OldNo > 0 {
		old = fmt.Sprintf("%4d", ln.OldNo)
	}
	if ln.NewNo > 0 {
		new_ = fmt.Sprintf("%4d", ln.NewNo)
	}
	gutter := sFaint.Render(old + " " + new_ + " │ ")
	var content string
	switch ln.Kind {
	case LineAdd:
		content = sAdd.Render("+" + ln.Text)
	case LineDel:
		content = sDel.Render("−" + ln.Text)
	default:
		content = sDim.Render(" " + ln.Text)
	}
	return truncLine(gutter+content, m.width)
}

// truncLine hard-truncates a styled line to width, ANSI-aware, no wrap.
func truncLine(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

// noticeModel is the styled full-screen message page used for merge errors.
type noticeModel struct {
	title  string
	body   string
	width  int
	height int
}

func (n *noticeModel) Init() tea.Cmd { return nil }

func (n *noticeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		n.width, n.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "enter", "esc", "ctrl+c":
			return n, tea.Quit
		}
	}
	return n, nil
}

func (n *noticeModel) View() string {
	if n.width == 0 {
		return ""
	}
	wrapW := 80
	if n.width-4 < wrapW {
		wrapW = n.width - 4
	}
	if wrapW < 10 {
		wrapW = 10
	}
	ruleW := wrapW
	title := sAccent.Render(n.title)
	rule := sFaint.Render(strings.Repeat("─", ruleW))
	body := lipgloss.NewStyle().Foreground(cFg).Width(wrapW).Render(n.body)
	hint := sFaint.Render("q close")
	block := lipgloss.JoinVertical(lipgloss.Left, title, rule, "", body, "", hint)
	return lipgloss.Place(n.width, n.height, lipgloss.Center, lipgloss.Center, block)
}

// RunNotice shows a styled full-screen message page (used for merge errors):
// title + body text, q/enter/esc quits. Same visual language as the pager.
func RunNotice(title, body string) error {
	p := tea.NewProgram(&noticeModel{title: title, body: body}, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
