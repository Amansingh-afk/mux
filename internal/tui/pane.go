package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amansingh-afk/mux/internal/session"
)

// captureRightClientCmd polls list-clients briefly and returns the client
// name attached to sess. Called after SplitRight; result lands in Update.
func captureRightClientCmd(pane, sess string) tea.Cmd {
	return func() tea.Msg {
		var c string
		for i := 0; i < 6; i++ {
			time.Sleep(80 * time.Millisecond)
			if c = session.FindClientForSession(sess); c != "" {
				break
			}
		}
		return rightClientMsg{pane: pane, session: sess, client: c}
	}
}

// nudgeAgentCmd schedules a delayed bounce-resize on the agent session so its
// TUI redraws to fill the pane. Needed because attach-time SIGWINCH fires
// before the wrapped CLI is ready to handle it, leaving the initial loading
// buffer (often dot fillers) stuck. Two nudges (200ms, 500ms) cover slow
// starters like claude/codex.
func nudgeAgentCmd(pane, agentID string) tea.Cmd {
	return func() tea.Msg {
		for _, d := range []time.Duration{200 * time.Millisecond, 500 * time.Millisecond} {
			time.Sleep(d)
			if !session.PaneExists(pane) {
				return nil
			}
			w, h, err := session.PaneDims(pane)
			if err != nil {
				continue
			}
			session.NudgeResize(agentID, w, h)
		}
		return nil
	}
}

// resizeAllSessions sizes every alive agent's tmux session to match the
// right (agent) pane. Pre-warming avoids the swap-flicker: when SwitchClient
// pulls a session into the client view, its backing grid is already at the
// correct size so the agent's existing render fills the pane immediately
// instead of showing dots during a resize-then-redraw window.
//
// Falls back to bodyDims when the right pane isn't established yet (early
// startup).
func (m Model) resizeAllSessions() tea.Cmd {
	var w, h int
	if m.rightPane != "" && session.PaneExists(m.rightPane) {
		if pw, ph, err := session.PaneDims(m.rightPane); err == nil {
			w, h = pw, ph
		}
	}
	if w == 0 {
		w, h = m.bodyDims()
	}
	snap := m.store.Snapshot()
	return func() tea.Msg {
		for _, p := range snap.Projects {
			for _, a := range p.Agents {
				session.SetSizeManual(a.ID)
				_ = session.Resize(a.ID, w, h)
			}
		}
		return nil
	}
}

// ensureAgentVisible splits or respawns the right tmux pane so the given
// agent's session is shown next to mux. agentID == "" means "no agent" —
// the right pane is killed. Idempotent: calling with the already-shown
// agent is a no-op. Returns a tea.Cmd to capture the nested client name
// when a fresh split was created (else nil).
func (m *Model) ensureAgentVisible(agentID string) tea.Cmd {
	if agentID == "" || !session.Exists(agentID) {
		// no live session to show — kill any leftover pane so the previous
		// project's agent doesn't visually stick around.
		m.clearRightPane()
		return nil
	}
	if m.rightPane != "" && session.PaneExists(m.rightPane) {
		if m.rightPaneAgent == agentID {
			return nil
		}
		// fast path: swap which session the existing nested client views.
		// Backings are pre-warmed to pane dims (resizeAllSessions on first
		// client attach + on every WindowSizeMsg), so the new session's grid
		// is already at the right size. SwitchClient is a pure view swap —
		// no resize, no SIGWINCH, no agent repaint, no flicker.
		if m.rightClient != "" {
			if err := session.SwitchClient(m.rightClient, agentID); err == nil {
				m.rightPaneAgent = agentID
				// zoom (M-z / C-b z) changes the pane's dims without mux
				// getting a WindowSizeMsg — its own pane is hidden while
				// zoomed — so the pre-warm can be stale. Re-check here;
				// Resize is dedup-cached, so this is free when dims match.
				if w, h, derr := session.PaneDims(m.rightPane); derr == nil {
					_ = session.Resize(agentID, w, h)
				}
				return nil
			}
			m.rightClient = "" // client name stale → fall through
		}
		// fallback: respawn the pane with a fresh attach. Used only when the
		// client tty hasn't been captured yet (rare race). Nudge here because
		// respawn does kill+exec which may resize the session.
		if err := session.RespawnPane(m.rightPane, agentID); err == nil {
			m.rightPaneAgent = agentID
			if w, h, err := session.PaneDims(m.rightPane); err == nil {
				session.ForgetResize(agentID)
				_ = session.Resize(agentID, w, h)
			}
			return tea.Batch(captureRightClientCmd(m.rightPane, agentID), nudgeAgentCmd(m.rightPane, agentID))
		}
		m.rightPane = ""
	}
	// pre-size the session to the predicted post-split pane dims (mux keeps
	// muxCols, +1 col for the border) so the attach shows a correctly sized
	// render immediately. The repaint happens before the pane exists, so it
	// is invisible — this is what kills the startup flicker.
	const muxCols = 24
	preW, preH := -1, -1
	if w, h, err := session.PaneDims(session.OuterPane()); err == nil && w > muxCols+10 {
		preW, preH = w-muxCols-1, h
		_ = session.Resize(agentID, preW, preH)
	}
	pane, err := session.SplitRight(session.OuterPane(), agentID, muxCols)
	if err != nil {
		return nil
	}
	m.rightPane = pane
	m.rightPaneAgent = agentID
	m.rightClient = "" // captured async
	if w, h, err := session.PaneDims(pane); err == nil {
		if w == preW && h == preH {
			// prediction hit: the attach is already right-sized. A mature
			// session's render is stable, so the bounce-nudge would only add
			// visible flicker — skip it. Fresh spawns still get nudged: their
			// CLI may have missed the pre-split SIGWINCH while booting.
			if a, _ := m.agentByID(agentID); a != nil && time.Since(time.Unix(a.SpawnedAt, 0)) > 5*time.Second {
				return captureRightClientCmd(pane, agentID)
			}
			return tea.Batch(captureRightClientCmd(pane, agentID), nudgeAgentCmd(pane, agentID))
		}
		// prediction missed (unusual layout) — resize as before.
		session.ForgetResize(agentID)
		_ = session.Resize(agentID, w, h)
	}
	return tea.Batch(captureRightClientCmd(pane, agentID), nudgeAgentCmd(pane, agentID))
}

func (m *Model) clearRightPane() {
	if m.rightPane != "" {
		session.KillPane(m.rightPane)
		m.rightPane = ""
		m.rightPaneAgent = ""
	}
}

// swapToCurrentAgent retargets the right pane to whatever agent is selected
// in the current project (or hides it if there is none). Doesn't touch focus —
// mux already has it in every code path that calls swap (j/k, [/], etc.) and
// an extra select-pane fires a tmux redraw of pane borders that shows up as
// a visible top-of-pane flicker.
func (m *Model) swapToCurrentAgent() tea.Cmd {
	if a := m.currentAgent(); a != nil {
		if p := m.currentProject(); p != nil {
			m.store.SetLastAgent(p.Path, a.ID)
		}
		return m.ensureAgentVisible(a.ID)
	}
	m.clearRightPane()
	return nil
}
