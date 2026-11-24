package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/Amansingh-afk/mux/internal/session"
	"github.com/Amansingh-afk/mux/internal/state"
)

// inbox ranks: what wants you most comes first.
const (
	rankBlocked = iota // at a permission prompt
	rankUnread         // finished since you last looked
	rankWaiting        // at its prompt, already seen
	rankWorking
	rankIdle
	rankStopped
)

var rankLabels = [...]string{"needs you", "finished", "waiting", "working", "idle", "stopped"}

type inboxItem struct {
	agent   state.Agent
	project state.Project
	rank    int
}

// inboxState backs the all-agents overlay (M-a). The cursor is an agent id:
// rows re-rank while the overlay is open and must not slide under it.
type inboxState struct {
	query string
	curID string
}

func (m Model) agentRank(a state.Agent) int {
	if a.Dead || !m.aliveCache[a.ID] {
		return rankStopped
	}
	rec := m.statusRec[a.ID]
	switch {
	case rec.status == session.StatusWaiting && rec.reason == session.ReasonPermission:
		return rankBlocked
	case rec.unread:
		return rankUnread
	case rec.status == session.StatusWaiting:
		return rankWaiting
	case rec.status == session.StatusActive:
		return rankWorking
	}
	return rankIdle
}

// inboxView lists every agent in every project, open tab or not, filtered by
// the query and ranked.
func (m Model) inboxView() []inboxItem {
	var items []inboxItem
	var names []string
	for _, p := range m.store.Snapshot().Projects {
		for _, a := range p.Agents {
			items = append(items, inboxItem{agent: a, project: p, rank: m.agentRank(a)})
			names = append(names, p.Name+" "+a.Name+" "+a.Provider)
		}
	}
	if m.inbox.query != "" {
		var out []inboxItem
		for _, match := range fuzzy.Find(m.inbox.query, names) {
			out = append(out, items[match.Index])
		}
		items = out
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].rank < items[j].rank })
	return items
}

// inboxIndex is the cursor's row, or 0 when its agent is filtered out.
func (m Model) inboxIndex(items []inboxItem) int {
	for i, it := range items {
		if it.agent.ID == m.inbox.curID {
			return i
		}
	}
	return 0
}

func (m Model) startInbox() (tea.Model, tea.Cmd) {
	m.focusForOverlay()
	m.mode = modeInbox
	m.inbox = inboxState{}
	if items := m.inboxView(); len(items) > 0 {
		m.inbox.curID = items[0].agent.ID
	}
	return m, nil
}

func (m Model) handleInbox(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.inboxView()
	cur := m.inboxIndex(items)
	move := func(i int) {
		if i >= 0 && i < len(items) {
			m.inbox.curID = items[i].agent.ID
		}
	}
	refilter := func(q string) {
		m.inbox = inboxState{query: q}
		if v := m.inboxView(); len(v) > 0 {
			m.inbox.curID = v[0].agent.ID
		}
	}
	switch k.String() {
	case "esc", "alt+a":
		m.mode = modeNormal
	case "down", "ctrl+n", "ctrl+j", "alt+j", "alt+down":
		move(cur + 1)
	case "up", "ctrl+p", "ctrl+k", "alt+k", "alt+up":
		move(cur - 1)
	case "enter":
		if cur < len(items) {
			return m.jumpToAgent(items[cur].agent.ID)
		}
	case "backspace":
		if r := []rune(m.inbox.query); len(r) > 0 {
			refilter(string(r[:len(r)-1]))
		}
	default:
		if !k.Alt && len(k.Runes) > 0 {
			refilter(m.inbox.query + cleanText(strings.ReplaceAll(string(k.Runes), "\n", "")))
		}
	}
	return m, nil
}

// jumpToAgent closes the overlay and selects the agent, opening its project's
// tab if needed. Focus lands in the agent pane.
func (m Model) jumpToAgent(id string) (tea.Model, tea.Cmd) {
	m.mode = modeNormal
	a, p := m.agentByID(id)
	if a == nil {
		return m, nil
	}
	m.store.OpenTab(p.Path)
	for i, t := range m.store.Snapshot().OpenTabs {
		if t == p.Path {
			m.activeTab = i
		}
	}
	m.setActiveFromIndex()
	for i := range p.Agents {
		if p.Agents[i].ID == id {
			m.sidebarCur = i
		}
	}
	m.refocusAgent = true
	cmd := m.swapToCurrentAgent()
	_ = m.store.Save()
	m.pushTabBar()
	return m, cmd
}

func (m Model) renderInbox() string {
	boxW := max(30, min(76, m.w-4))
	innerW := boxW - 6
	dim := lipgloss.NewStyle().Foreground(colorDim)
	items := m.inboxView()
	cur := m.inboxIndex(items)
	rows := []string{
		lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("all agents"),
		"› " + m.inbox.query + "▎", "",
	}
	maxRows := max(3, min(16, m.h-12))
	start := 0
	if cur >= maxRows {
		start = cur - maxRows + 1
	}
	for i := start; i < len(items) && i < start+maxRows; i++ {
		it := items[i]
		marker, nameSt := "  ", lipgloss.NewStyle().Foreground(colorFg)
		if i == cur {
			marker, nameSt = styleSelMarker.Render("▌ "), styleListItemSel
		}
		labelSt := dim
		switch it.rank {
		case rankBlocked:
			labelSt = lipgloss.NewStyle().Foreground(colorWarn).Bold(true)
		case rankUnread:
			labelSt = lipgloss.NewStyle().Foreground(colorOK)
		}
		rows = append(rows, clipLine(marker+m.statusIcon(it.agent.ID)+" "+
			labelSt.Width(10).Render(rankLabels[it.rank])+" "+
			nameSt.Width(18).Render(truncName(it.agent.Name, 17))+" "+
			dim.Width(18).Render(truncName(it.project.Name, 17))+" "+
			providerIcon(it.agent.Provider, it.rank == rankStopped), innerW))
	}
	if len(items) == 0 {
		rows = append(rows, dim.Render("no agents"))
	} else if len(items) > maxRows {
		rows = append(rows, dim.Render(fmt.Sprintf("%d/%d", cur+1, len(items))))
	}
	rows = append(rows, "", styleStatus.Render("type to filter  ↓↑ select  enter jump  esc close"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(1, 2).
		Width(boxW).
		Render(strings.Join(rows, "\n"))
}
