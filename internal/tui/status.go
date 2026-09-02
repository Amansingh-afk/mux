package tui

import (
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Amansingh-afk/mux/internal/hooks"
	"github.com/Amansingh-afk/mux/internal/session"
)

// captureAllForStatus captures every (live) agent's pane content in parallel
// so we can classify state for each. Skips dead agents (no session to
// capture) and bounds concurrency to avoid spawning dozens of tmux processes
// at once when the user has many agents open.
func (m Model) captureAllForStatus() tea.Cmd {
	snap := m.store.Snapshot()
	// copy aliveCache into a local so the returned closure is free of any
	// reference into Model state that Update may mutate later.
	alive := make(map[string]bool, len(m.aliveCache))
	for k, v := range m.aliveCache {
		alive[k] = v
	}
	var refs []session.AgentRef
	for _, p := range snap.Projects {
		for _, a := range p.Agents {
			if a.Dead || !alive[a.ID] {
				continue
			}
			dir := a.Dir
			if dir == "" {
				dir = p.Path
			}
			refs = append(refs, session.AgentRef{
				ID: a.ID, Provider: a.Provider,
				Dir: dir, SessionUUID: a.SessionUUID,
			})
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return func() tea.Msg {
		const maxParallel = 4
		sem := make(chan struct{}, maxParallel)
		var wg sync.WaitGroup
		results := make([]captureRec, len(refs))
		for i, ref := range refs {
			sem <- struct{}{}
			wg.Add(1)
			go func(i int, ref session.AgentRef) {
				defer wg.Done()
				defer func() { <-sem }()
				var native *session.NativeSignal
				if sig, ok := session.NativeProbe(ref); ok {
					native = &sig
				}
				out, err := session.Capture(ref.ID, 40)
				if err != nil {
					return
				}
				results[i] = captureRec{id: ref.ID, out: out, native: native}
			}(i, ref)
		}
		wg.Wait()
		caps := make([]captureRec, 0, len(results))
		for _, r := range results {
			if r.id != "" {
				caps = append(caps, r)
			}
		}
		return statusBatchMsg(caps)
	}
}

// nativeFreshWindow: a native signal at most this old is authoritative and
// wins over the pane-capture heuristic outright.
const nativeFreshWindow = 5 * time.Second

// classifyAndNotify folds a fresh capture into statusRec and fires the
// on_waiting hook on active→waiting transitions. A 30s per-agent cooldown
// dampens flap-storms when an agent rapidly toggles state.
//
// Merge precedence (native = provider-emitted signal from NativeProbe):
//  1. native fresh (≤5s old) — wins outright.
//  2. stale native waiting/idle but the pane hash CHANGED this tick — the
//     agent is visibly doing something the hooks haven't reported yet: active.
//  3. any other native present — trust it (hooks self-correct on the next
//     provider event).
//  4. no native — today's pane-hash heuristic unchanged.
//
// The heuristic Classify always runs so hash/stableTicks bookkeeping stays
// consistent regardless of which branch decides the status: a pane change
// resets stableTicks to 0 exactly as before, and a later native-less tick
// picks up seamlessly.
func (m Model) classifyAndNotify(c captureRec) {
	id, out := c.id, c.out
	prev := m.statusRec[id]
	s, h, t := session.Classify(out, prev.hash, prev.status, prev.stableTicks)
	paneChanged := h != prev.hash
	reason := ""
	if n := c.native; n != nil {
		switch {
		case time.Since(n.At) <= nativeFreshWindow:
			s = n.Status
			reason = n.Reason
		case (n.Status == session.StatusWaiting || n.Status == session.StatusIdle) && paneChanged:
			s = session.StatusActive
		default:
			s = n.Status
			reason = n.Reason
		}
	}
	rec := agentStatusRec{status: s, hash: h, stableTicks: t, lastNotify: prev.lastNotify}
	if prev.status != s && s == session.StatusWaiting && time.Since(prev.lastNotify) > 30*time.Second {
		if a, p := m.agentByID(id); a != nil {
			ctx := hooks.Context{
				Agent:  &hooks.AgentCtx{ID: a.ID, Name: a.Name, Provider: a.Provider},
				Status: &hooks.StatusCtx{Prev: string(prev.status), Now: string(s), Reason: reason},
			}
			if p != nil {
				ctx.Project = &hooks.ProjectCtx{Name: p.Name, Path: p.Path}
			}
			hooks.Notify("on_waiting", ctx)
			rec.lastNotify = time.Now()
		}
	}
	m.statusRec[id] = rec
}

func (m Model) statusIcon(id string) string {
	if !m.aliveCache[id] {
		return styleDot["dead"].Render("✕")
	}
	rec := m.statusRec[id]
	switch rec.status {
	case session.StatusActive:
		frame := session.SpinnerFrames[m.spinnerFrame%len(session.SpinnerFrames)]
		return lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(frame)
	case session.StatusWaiting:
		return lipgloss.NewStyle().Foreground(colorWarn).Render("◐")
	case session.StatusIdle:
		return lipgloss.NewStyle().Foreground(colorDim).Render("○")
	}
	return styleDot["running"].Render("●")
}
