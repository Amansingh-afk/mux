package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amansingh-afk/mux/internal/session"
	"github.com/Amansingh-afk/mux/internal/state"
)

// pushTabBar pushes the project tab list to outer tmux's status-format[0]
// so it renders in the full-width status line above (or below) all panes.
// Called whenever tab membership or active tab changes.
func (m Model) pushTabBar() {
	snap := m.store.Snapshot()
	if len(snap.OpenTabs) == 0 {
		session.SetTopTabs("#[fg=colour244] mux · press o to open a project ")
		return
	}
	var b strings.Builder
	b.WriteString("#[fg=colour205,bold] mux #[default] ")
	for i, path := range snap.OpenTabs {
		if i > 0 {
			b.WriteString("#[fg=colour240]│#[default]")
		}
		name := truncName(filepath.Base(path), 16)
		label := fmt.Sprintf("%d %s", i+1, name)
		b.WriteString(session.FormatTab(label, i == m.activeTab))
	}
	session.SetTopTabs(b.String())
}

func (m Model) prevTab() (tea.Model, tea.Cmd) {
	if m.activeTab > 0 {
		m.activeTab--
		m.setActiveFromIndex()
		m.sidebarCur = m.restoreSidebarCur()
		cmd := m.swapToCurrentAgent()
		m.pushTabBar()
		return m, cmd
	}
	return m, nil
}

func (m Model) nextTab() (tea.Model, tea.Cmd) {
	snap := m.store.Snapshot()
	if m.activeTab < len(snap.OpenTabs)-1 {
		m.activeTab++
		m.setActiveFromIndex()
		m.sidebarCur = m.restoreSidebarCur()
		cmd := m.swapToCurrentAgent()
		m.pushTabBar()
		return m, cmd
	}
	return m, nil
}

func (m Model) jumpTab(i int) (tea.Model, tea.Cmd) {
	snap := m.store.Snapshot()
	if i < 0 || i >= len(snap.OpenTabs) {
		return m, nil
	}
	m.activeTab = i
	m.setActiveFromIndex()
	m.sidebarCur = m.restoreSidebarCur()
	cmd := m.swapToCurrentAgent()
	m.pushTabBar()
	return m, cmd
}

// restoreSidebarCur returns the sidebar index for the project's last-focused
// agent, falling back to 0 when no record exists or the agent was removed.
func (m Model) restoreSidebarCur() int {
	p := m.currentProject()
	if p == nil || p.LastAgentID == "" {
		return 0
	}
	for i, a := range p.Agents {
		if a.ID == p.LastAgentID {
			return i
		}
	}
	return 0
}

func (m *Model) setActiveFromIndex() {
	snap := m.store.Snapshot()
	if m.activeTab < len(snap.OpenTabs) {
		m.store.SetActive(snap.OpenTabs[m.activeTab])
	}
}

// closeCurrentTab closes the active tab (optionally killing its agents) and
// returns the tea.Cmd produced by retargeting the right pane, which the
// caller must return into the bubbletea runtime.
func (m *Model) closeCurrentTab(killAgents bool) tea.Cmd {
	snap := m.store.Snapshot()
	if m.activeTab >= len(snap.OpenTabs) {
		return nil
	}
	path := snap.OpenTabs[m.activeTab]
	if killAgents {
		for _, proj := range snap.Projects {
			if proj.Path != path {
				continue
			}
			for _, a := range proj.Agents {
				_ = session.Kill(a.ID)
				m.store.RemoveAgent(path, a.ID)
			}
		}
	}
	m.store.CloseTab(path)
	snap = m.store.Snapshot()
	if m.activeTab >= len(snap.OpenTabs) {
		m.activeTab = len(snap.OpenTabs) - 1
	}
	if m.activeTab < 0 {
		m.activeTab = 0
	}
	m.sidebarCur = 0
	_ = m.store.Save()
	cmd := m.swapToCurrentAgent()
	m.pushTabBar()
	return cmd
}

func (m Model) handleOpenProject(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "enter":
		it := m.picker.selectedOrLiteral()
		if it == nil {
			m.statusMsg = "no match"
			return m, nil
		}
		abs, err := filepath.Abs(it.path)
		if err != nil {
			m.statusMsg = "bad path: " + err.Error()
			m.mode = modeNormal
			return m, nil
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			m.statusMsg = "not a dir: " + abs
			m.mode = modeNormal
			return m, nil
		}
		name := it.name
		if name == "" {
			name = filepath.Base(abs)
		}
		m.store.UpsertProject(state.Project{Name: name, Path: abs, LastUse: time.Now().Unix()})
		m.store.OpenTab(abs)
		snap := m.store.Snapshot()
		for i, t := range snap.OpenTabs {
			if t == abs {
				m.activeTab = i
			}
		}
		m.sidebarCur = 0
		m.mode = modeNormal
		_ = m.store.Save()
		cmd := m.swapToCurrentAgent()
		m.pushTabBar()
		return m, cmd
	case "backspace":
		m.picker.backspace()
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		m.picker.down()
		return m, nil
	case "up", "ctrl+p", "ctrl+k":
		m.picker.up()
		return m, nil
	default:
		s := k.String()
		if len(s) == 1 {
			m.picker.typed(s)
		}
		return m, nil
	}
}
