package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amansingh-afk/mux/internal/session"
	"github.com/Amansingh-afk/mux/internal/state"
)

// reviewState backs the review overlay (M-v). The target agent is held by id
// so a later cursor move can't redirect a merge or discard.
type reviewState struct {
	agentID string
	// seq drops results from an earlier load of the overlay.
	seq     int
	busy    string // non-empty while a load or merge runs
	loaded  bool
	rev     session.Review
	lines   []string
	top     int
	left    int
	notice  string
	failed  bool
	ask     string // "m" or "x" while that action awaits y
	noting  bool
	noteBuf string
}

type reviewLoadedMsg struct {
	seq int
	rev session.Review
	err error
}

type reviewLandedMsg struct {
	seq  int
	into string
	err  error
}

func (m Model) startReview() (tea.Model, tea.Cmd) {
	a := m.currentAgent()
	if a == nil {
		return m, nil
	}
	m.focusForOverlay()
	m.mode = modeReview
	m.review = reviewState{agentID: a.ID, seq: m.review.seq}
	if a.Worktree == "" {
		m.review.notice = "this agent works in the main tree, so there is nothing to land"
		return m, nil
	}
	return m, m.loadReview()
}

func (m *Model) loadReview() tea.Cmd {
	a, p := m.agentByID(m.review.agentID)
	if a == nil || a.Worktree == "" {
		return nil
	}
	m.review.seq++
	m.review.busy = "reading changes"
	seq, repo, wt := m.review.seq, p.Path, a.Worktree
	return func() tea.Msg {
		rev, err := session.LoadReview(repo, wt)
		return reviewLoadedMsg{seq: seq, rev: rev, err: err}
	}
}

func (m Model) reviewLoaded(msg reviewLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.review.seq {
		return m, nil
	}
	m.review.busy = ""
	m.review.loaded = msg.err == nil
	if msg.err != nil {
		m.review.notice, m.review.failed = msg.err.Error(), true
		return m, nil
	}
	m.review.rev = msg.rev
	m.review.lines = diffLines(msg.rev.Diff)
	m.scrollReview(0)
	return m, nil
}

func (m Model) reviewLanded(msg reviewLandedMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.review.seq {
		return m, nil
	}
	m.review.busy = ""
	if msg.err != nil {
		m.review.notice, m.review.failed = msg.err.Error(), true
		return m, nil
	}
	m.review.notice, m.review.failed = "merged into "+msg.into, false
	return m, m.loadReview()
}

// diffLines prepares diff text for the overlay. Control characters are
// dropped so file content can't drive the terminal.
func diffLines(diff string) []string {
	diff = strings.TrimSuffix(diff, "\n")
	if diff == "" {
		return nil
	}
	return strings.Split(cleanText(strings.ReplaceAll(diff, "\t", "    ")), "\n")
}

func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && (r < 0x20 || (r >= 0x7f && r < 0xa0)) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "?"))
}

func (m Model) reviewBodyH() int { return max(m.h-7, 1) }

func (m *Model) scrollReview(by int) {
	m.review.top = max(0, min(m.review.top+by, len(m.review.lines)-m.reviewBodyH()))
}

// jumpFile moves to the next (dir 1) or previous (dir -1) file header.
func (m *Model) jumpFile(dir int) {
	for i := m.review.top + dir; i >= 0 && i < len(m.review.lines); i += dir {
		if strings.HasPrefix(m.review.lines[i], "diff --git ") {
			m.review.top = i
			m.scrollReview(0)
			return
		}
	}
}

func (m Model) handleReview(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// a merge in flight must finish before anything else touches the tree.
	if m.review.busy == "merging" {
		return m, nil
	}
	if m.review.noting {
		return m.handleReviewNote(k)
	}
	if m.review.ask != "" {
		ask := m.review.ask
		m.review.ask = ""
		if k.String() != "y" {
			return m, nil
		}
		if ask == "x" {
			return m.discardReviewed()
		}
		return m.landReviewed()
	}
	ready := m.review.loaded && m.review.busy == ""
	page := m.reviewBodyH()
	switch k.String() {
	case "esc", "q", "alt+v":
		m.mode = modeNormal
		m.review.seq++ // drop a load still in flight
	case "j", "down":
		m.scrollReview(1)
	case "k", "up":
		m.scrollReview(-1)
	case "d", "pgdown", " ":
		m.scrollReview(page)
	case "u", "pgup":
		m.scrollReview(-page)
	case "g", "home":
		m.review.top = 0
	case "G", "end":
		m.scrollReview(len(m.review.lines))
	case "l", "right":
		m.review.left += 8
	case "h", "left":
		m.review.left = max(0, m.review.left-8)
	case "n":
		m.jumpFile(1)
	case "N":
		m.jumpFile(-1)
	case "r":
		m.review.notice = ""
		return m, m.loadReview()
	case "m":
		if ready && m.review.rev.Empty() {
			m.review.notice, m.review.failed = "nothing to merge", false
		} else if ready {
			m.review.notice, m.review.ask = "", "m"
		}
	case "x":
		// also offered when the review can't load: a broken worktree still
		// needs a way out.
		if a, _ := m.agentByID(m.review.agentID); a != nil && a.Worktree != "" && m.review.busy == "" {
			m.review.notice, m.review.ask = "", "x"
		}
	case "s":
		if !m.aliveCache[m.review.agentID] {
			m.review.notice, m.review.failed = "the agent isn't running; resume it first", true
		} else {
			m.review.noting = true
		}
	}
	return m, nil
}

func (m Model) landReviewed() (tea.Model, tea.Cmd) {
	a, p := m.agentByID(m.review.agentID)
	if a == nil {
		return m, nil
	}
	m.review.seq++
	m.review.busy = "merging"
	seq, rev := m.review.seq, m.review.rev
	repo, wt, branch, name := p.Path, a.Worktree, a.Branch, a.Name
	return m, func() tea.Msg {
		return reviewLandedMsg{seq: seq, into: rev.Into, err: session.Land(repo, wt, branch, name, rev)}
	}
}

func (m Model) discardReviewed() (tea.Model, tea.Cmd) {
	a, p := m.agentByID(m.review.agentID)
	if a == nil {
		return m, nil
	}
	if err := m.removeAgent(p.Path, *a, true); err != nil {
		m.review.notice, m.review.failed = err.Error(), true
		return m, nil
	}
	m.mode = modeNormal
	m.review.seq++
	return m, m.afterRemove()
}

// handleReviewNote edits the note. Enter pastes it into the agent's input
// unsubmitted: the agent may be sitting at a permission prompt, where a blind
// enter would approve it.
func (m Model) handleReviewNote(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.review.noting = false
	case "enter":
		note := strings.TrimSpace(m.review.noteBuf)
		if note == "" {
			m.review.noting = false
			return m, nil
		}
		if err := session.PasteText(m.review.agentID, note); err != nil {
			m.review.noting = false
			m.review.notice, m.review.failed = "could not reach the agent: "+err.Error(), true
			return m, nil
		}
		return m.jumpToAgent(m.review.agentID)
	case "backspace":
		if r := []rune(m.review.noteBuf); len(r) > 0 {
			m.review.noteBuf = string(r[:len(r)-1])
		}
	default:
		if !k.Alt {
			m.review.noteBuf += cleanText(strings.ReplaceAll(string(k.Runes), "\n", " "))
		}
	}
	return m, nil
}

// removeAgent kills the agent and deletes its worktree and state. When the
// worktree can't be deleted the agent is kept so its files stay reachable.
func (m *Model) removeAgent(projectPath string, a state.Agent, deleteBranch bool) error {
	_ = session.Kill(a.ID)
	session.KillShell(a.ID)
	delete(m.aliveCache, a.ID)
	if a.Worktree != "" {
		err := session.RemoveWorktree(projectPath, a.Worktree, a.Branch, deleteBranch)
		if _, serr := os.Stat(a.Worktree); serr == nil {
			m.store.MarkAgentDead(projectPath, a.ID)
			_ = m.store.Save()
			return fmt.Errorf("could not delete %s: %v", a.Worktree, err)
		}
	}
	m.store.RemoveAgent(projectPath, a.ID)
	delete(m.statusRec, a.ID)
	delete(m.diffStats, a.ID)
	return m.store.Save()
}

// afterRemove clamps the cursor and retargets the right pane.
func (m *Model) afterRemove() tea.Cmd {
	if p := m.currentProject(); p != nil {
		m.sidebarCur = max(0, min(m.sidebarCur, len(p.Agents)-1))
	}
	return m.swapToCurrentAgent()
}

func (m Model) renderReview() string {
	a, _ := m.agentByID(m.review.agentID)
	if a == nil {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(colorDim)
	warn := lipgloss.NewStyle().Foreground(colorWarn)
	w := max(m.w-4, 10)
	rev := m.review.rev

	head := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("review " + a.Name)
	if m.review.loaded {
		head += dim.Render(fmt.Sprintf("  %s → %s  ", a.Branch, rev.Into)) + diffStatLine(rev.Ins, rev.Del) +
			dim.Render(fmt.Sprintf("  %d commits, %d uncommitted", rev.Commits, rev.Loose))
		if rev.Truncated {
			head += warn.Render("  diff cut short")
		}
	}

	bodyH := m.reviewBodyH()
	body := make([]string, 0, bodyH)
	for i := m.review.top; i < len(m.review.lines) && len(body) < bodyH; i++ {
		l := m.review.lines[i]
		st := lipgloss.NewStyle().Foreground(colorFg)
		switch {
		case strings.HasPrefix(l, "diff --git "):
			st = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		case strings.HasPrefix(l, "+"):
			st = lipgloss.NewStyle().Foreground(colorOK)
		case strings.HasPrefix(l, "-"):
			st = lipgloss.NewStyle().Foreground(colorErr)
		case !strings.HasPrefix(l, " "):
			st = dim
		}
		body = append(body, st.Render(ansi.Cut(l, m.review.left, m.review.left+w)))
	}
	if len(body) == 0 && m.review.loaded {
		body = append(body, dim.Render("no changes"))
	}
	for len(body) < bodyH {
		body = append(body, "")
	}

	var line string
	switch {
	case m.review.noting:
		line = "› " + ansi.TruncateLeft(m.review.noteBuf, max(0, ansi.StringWidth(m.review.noteBuf)-w+4), "") + "▎"
	case m.review.ask == "m":
		q := fmt.Sprintf("merge into %s? y / any other key cancels", rev.Into)
		if rev.Loose > 0 {
			q = fmt.Sprintf("commit %d uncommitted files and %s", rev.Loose, q)
		}
		if m.statusRec[a.ID].status == session.StatusActive {
			q = "agent is still working! " + q
		}
		line = warn.Render(q)
	case m.review.ask == "x":
		line = lipgloss.NewStyle().Foreground(colorErr).Bold(true).
			Render("delete this agent, its worktree, ignored files and branch for good? y / any other key cancels")
	case m.review.busy != "":
		line = dim.Render(m.review.busy + "…")
	case m.review.failed:
		line = lipgloss.NewStyle().Foreground(colorErr).Render(cleanText(strings.ReplaceAll(m.review.notice, "\n", " ")))
	default:
		line = lipgloss.NewStyle().Foreground(colorOK).Render(m.review.notice)
	}
	hint := "m merge  x discard  s send back  r reload  n/N file  ↓↑ scroll  ←→ pan  esc close"
	if m.review.noting {
		hint = "enter put note in agent's input  esc cancel"
	}
	rows := []string{"", clipLine(head, w), gradientRule(w)}
	rows = append(rows, body...)
	rows = append(rows, gradientRule(w), clipLine(line, w), clipLine(dim.Render(hint), w))
	return lipgloss.NewStyle().Padding(0, 2).Render(strings.Join(rows, "\n"))
}

// confirmState is a yes/no overlay shown before something is deleted.
type confirmState struct {
	title string
	lines []string
	yes   func(*Model) tea.Cmd
}

func (m *Model) askConfirm(c confirmState) {
	m.focusForOverlay()
	m.confirm = c
	m.mode = modeConfirm
}

func (m Model) handleConfirm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y":
		m.mode = modeNormal
		yes := m.confirm.yes
		m.confirm = confirmState{}
		if yes == nil {
			return m, nil
		}
		cmd := yes(&m)
		return m, cmd
	case "n", "esc", "q", "enter":
		m.mode = modeNormal
		m.confirm = confirmState{}
	}
	return m, nil
}

func (m Model) renderConfirm() string {
	rows := []string{lipgloss.NewStyle().Bold(true).Foreground(colorErr).Render(m.confirm.title), ""}
	rows = append(rows, m.confirm.lines...)
	hint := "y delete  esc cancel"
	if m.confirm.yes == nil {
		hint = "esc close"
	}
	rows = append(rows, "", styleStatus.Render(hint))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorErr).
		Padding(1, 2).
		Width(max(20, min(70, m.w-4))).
		Render(strings.Join(rows, "\n"))
}

// deleteOrConfirm forgets agents, asking first when that would lose work.
// done runs after the deletions.
func (m *Model) deleteOrConfirm(projectPath, title string, agents []state.Agent, done func(*Model) tea.Cmd) tea.Cmd {
	var lines []string
	for _, a := range agents {
		if a.Worktree == "" {
			continue
		}
		r := session.WorktreeRisk(projectPath, a.Worktree, a.Branch)
		if !r.Any() {
			continue
		}
		lines = append(lines, a.Name+" would lose "+r.String())
		if r.Unmerged > 0 {
			lines = append(lines, fmt.Sprintf("  (its %d unmerged commits stay on %s)", r.Unmerged, a.Branch))
		}
	}
	run := func(m *Model) tea.Cmd {
		var failed []string
		for _, a := range agents {
			if err := m.removeAgent(projectPath, a, session.BranchMerged(projectPath, a.Branch)); err != nil {
				failed = append(failed, a.Name+": "+err.Error())
			}
		}
		if len(failed) > 0 {
			m.askConfirm(confirmState{title: "some agents were kept", lines: failed})
			return m.afterRemove()
		}
		return done(m)
	}
	if len(lines) == 0 {
		return run(m)
	}
	m.askConfirm(confirmState{title: title, lines: lines, yes: run})
	return nil
}
