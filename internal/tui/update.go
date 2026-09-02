package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amansingh-afk/mux/internal/session"
)

// Update is the bubbletea entry point. It delegates to dispatch and then
// reconciles tmux pane zoom with the resulting mode: modal modes
// (open/spawn/rename/help) zoom mux's pane to fill the window so the picker
// has room; modeNormal restores the sidebar+agent layout. Idempotent guard
// is in session.SetPaneZoom — reads window_zoomed_flag and only toggles on
// change, so this fires safely on every Update.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	prevModal := m.mode != modeNormal
	res, cmd := m.dispatch(msg)
	mm, ok := res.(Model)
	if !ok {
		return res, cmd
	}
	nowModal := mm.mode != modeNormal
	if prevModal != nowModal {
		if mp := session.OuterPane(); mp != "" {
			session.SetPaneZoom(mp, nowModal)
		}
	}
	return mm, cmd
}

func (m Model) dispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if !m.booted {
			// first sizing: restore the cursor and hold a short boot splash —
			// mux still owns the full window here, so the splash gets the
			// whole screen and the split happens behind bootRevealMsg. The
			// pre-warm resize runs now so the reveal is a single clean paint.
			m.booted = true
			m.loading = true
			m.sidebarCur = m.restoreSidebarCur()
			reveal := tea.Tick(700*time.Millisecond, func(time.Time) tea.Msg { return bootRevealMsg{} })
			return m, tea.Batch(m.resizeAllSessions(), reveal)
		}
		return m, m.resizeAllSessions()

	case bootRevealMsg:
		m.loading = false
		return m, m.swapToCurrentAgent()

	case tickMsg:
		return m, tea.Batch(tick(), refreshAlive(), m.captureAllForStatus())

	case spinTickMsg:
		m.spinnerFrame++
		return m, spinTick()

	case statusBatchMsg:
		for _, p := range msg {
			m.classifyAndNotify(p)
		}
		return m, nil

	case aliveMsg:
		m.aliveCache = msg
		// reconcile state.Dead against live set so dead agents flip on
		// spontaneously (e.g. tmux socket went away mid-session). Save
		// only when something actually flipped so we don't fsync every tick.
		if n := m.store.Reconcile(msg); n > 0 {
			_ = m.store.Save()
		}
		return m, nil

	case uuidCapturedMsg:
		if msg.uuid != "" && session.ValidUUID(msg.uuid) {
			m.store.SetAgentUUID(msg.projectPath, msg.agentID, msg.uuid)
			_ = m.store.Save()
		}
		return m, nil

	case rightClientMsg:
		// only adopt if the right pane is still the same one we asked about,
		// and we don't already have a client. otherwise the capture is stale.
		if m.rightPane == msg.pane && m.rightClient == "" {
			m.rightClient = msg.client
			// pre-warm every other agent's backing to match the right pane.
			// Now that we know the pane is up, sizing the rest avoids the
			// flicker on first swap into them.
			return m, m.resizeAllSessions()
		}
		return m, nil

	case adoptListMsg:
		// ignore a scan that raced a tab switch — it lists another project.
		if m.mode == modeAdopt {
			if p := m.currentProject(); p != nil && p.Path == msg.projectPath {
				m.adoptItems = msg.items
				m.adoptCur = 0
				m.adoptLoading = false
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeOpenProject {
		return m.handleOpenProject(k)
	}
	if m.mode == modeSpawnAgent {
		return m.handleSpawnAgent(k)
	}
	if m.mode == modeRenameAgent {
		return m.handleRenameAgent(k)
	}
	if m.mode == modeAdopt {
		return m.handleAdopt(k)
	}
	if m.mode == modeHelp {
		switch k.String() {
		case "esc", "?", "q":
			m.mode = modeNormal
		}
		return m, nil
	}

	switch k.String() {
	case "ctrl+c", "q":
		_ = m.store.Save()
		m.clearRightPane()
		return m, tea.Quit

	case "j", "down":
		p := m.currentProject()
		if p != nil && m.sidebarCur < len(p.Agents)-1 {
			m.sidebarCur++
			if a := m.currentAgent(); a != nil {
				m.store.SetLastAgent(p.Path, a.ID)
				return m, m.ensureAgentVisible(a.ID)
			}
		}
		return m, nil

	case "k", "up":
		if m.sidebarCur > 0 {
			m.sidebarCur--
			if a := m.currentAgent(); a != nil {
				if p := m.currentProject(); p != nil {
					m.store.SetLastAgent(p.Path, a.ID)
				}
				return m, m.ensureAgentVisible(a.ID)
			}
		}
		return m, nil

	case "[", "shift+tab":
		return m.prevTab()

	case "]":
		return m.nextTab()

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.jumpTab(int(k.String()[0] - '1'))

	case "o":
		m.mode = modeOpenProject
		snap := m.store.Snapshot()
		m.picker = newRepoPicker(snap.Projects)
		return m, nil

	case "w":
		return m, m.closeCurrentTab(false)

	case "W":
		return m, m.closeCurrentTab(true)

	case "n":
		if m.currentProject() == nil {
			m.statusMsg = "open a project first (o)"
			return m, nil
		}
		m.mode = modeSpawnAgent
		m.providerCur = 0
		return m, nil

	case "d":
		return m, m.killCurrentAgent()

	case "i":
		return m.startAdopt()

	case "r":
		a := m.currentAgent()
		if a == nil {
			return m, nil
		}
		m.mode = modeRenameAgent
		m.renameBuf = a.Name
		return m, nil

	case "enter":
		return m.attachCurrent()

	case "z":
		// zen mode: zoom the right (agent) pane to fill the window. mux
		// pane disappears until user un-zooms via tmux's own toggle (C-b z).
		// Resize the agent session after zoom so its TUI redraws at the
		// new full-window dimensions instead of leaving dot-grid filler.
		if m.rightPane != "" && session.PaneExists(m.rightPane) {
			_ = session.ZoomPane(m.rightPane)
			if w, h, err := session.PaneDims(m.rightPane); err == nil && m.rightPaneAgent != "" {
				session.ForgetResize(m.rightPaneAgent)
				_ = session.Resize(m.rightPaneAgent, w, h)
			}
			return m, nudgeAgentCmd(m.rightPane, m.rightPaneAgent)
		}
		m.statusMsg = "no agent pane to zen"
		return m, nil

	case "?":
		m.mode = modeHelp
		return m, nil
	}
	return m, nil
}
