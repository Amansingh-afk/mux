package tui

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amansingh-afk/mux/internal/session"
	"github.com/Amansingh-afk/mux/internal/state"
)

// worktreeSetup is the configured [worktree] setup command, run in every
// fresh agent worktree before the agent starts. Set once at startup.
var worktreeSetup string

func SetWorktreeSetup(cmd string) { worktreeSetup = cmd }

func (m Model) handleSpawnAgent(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	names := session.ProviderNames()
	switch k.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "j", "down":
		if m.providerCur < len(names)-1 {
			m.providerCur++
		}
		return m, nil
	case "k", "up":
		if m.providerCur > 0 {
			m.providerCur--
		}
		return m, nil
	case "enter":
		p := m.currentProject()
		if p == nil {
			m.mode = modeNormal
			return m, nil
		}
		provider := names[m.providerCur]
		spec := session.Providers[provider]
		if !spec.Available {
			return m, nil
		}
		id := pickSessionID(p, provider)
		name := session.NewCodename(codenamesTaken(p))
		w, h := m.bodyDims()
		projectPath := p.Path
		// worktree-per-agent: every coding agent gets its own worktree +
		// branch; the base tree stays the human's. Shells and non-git dirs
		// run in the base.
		dir := projectPath
		var wt, branch, setup string
		if provider != "shell" && session.IsGitRepo(projectPath) {
			var werr error
			wt, branch, werr = session.CreateWorktree(projectPath, name)
			if werr != nil {
				m.mode = modeNormal
				return m, nil
			}
			dir = wt
			_, _, _ = session.CopyWorktreeInclude(projectPath, wt)
			setup = worktreeSetup
		}
		before := session.SnapshotFor(provider, dir)
		var spawnErr error
		session.WithSpawnLock(provider, dir, func() {
			spawnErr = session.SpawnWithResume(id, dir, spec, w, h, session.ResumeOpts{Setup: setup})
		})
		if spawnErr != nil {
			if wt != "" {
				_ = session.RemoveWorktree(projectPath, wt, branch, true)
			}
			m.mode = modeNormal
			return m, nil
		}
		m.store.AddAgent(p.Path, state.Agent{
			ID: id, Name: name, Provider: provider,
			SpawnedAt: time.Now().Unix(),
			Dir:       dir,
			Worktree:  wt,
			Branch:    branch,
			LastSeen:  time.Now().Unix(),
		})
		m.aliveCache[id] = true
		_ = m.store.Save()
		m.mode = modeNormal
		// move cursor to the just-spawned agent so auto-attach targets it.
		snap := m.store.Snapshot()
		for pi := range snap.Projects {
			if snap.Projects[pi].Path != projectPath {
				continue
			}
			for ai, a := range snap.Projects[pi].Agents {
				if a.ID == id {
					m.sidebarCur = ai
					break
				}
			}
		}
		// show the new agent next to mux and focus it so spawn→type is one step.
		session.SetSizeManual(id)
		visCmd := m.ensureAgentVisible(id)
		_ = session.SelectPane(m.rightPane)
		return m, tea.Batch(captureUUIDCmd(provider, dir, id, before), visCmd)
	}
	return m, nil
}

func codenamesTaken(p *state.Project) map[string]bool {
	out := map[string]bool{}
	for _, a := range p.Agents {
		if a.Name != "" {
			out[a.Name] = true
		}
	}
	return out
}

func (m Model) handleRenameAgent(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "enter":
		p := m.currentProject()
		a := m.currentAgent()
		if p != nil && a != nil {
			name := strings.TrimSpace(m.renameBuf)
			if name == "" {
				name = session.NewCodename(codenamesTaken(p))
			}
			m.store.RenameAgent(p.Path, a.ID, name)
			_ = m.store.Save()
		}
		m.mode = modeNormal
		return m, nil
	case "backspace":
		if len(m.renameBuf) > 0 {
			m.renameBuf = m.renameBuf[:len(m.renameBuf)-1]
		}
		return m, nil
	default:
		// use k.Runes so shift+letter (capitals) and other printable chars land
		// intact. k.String() returns "shift+A" for capitals which len()==1 misses.
		// alt chords (global nav layer) carry runes too — don't type those.
		if len(k.Runes) > 0 && !k.Alt {
			for _, r := range k.Runes {
				if r >= 0x20 && r != 0x7f {
					m.renameBuf += string(r)
				}
			}
		}
		return m, nil
	}
}

// killCurrentAgent kills (or, on a second press, forgets) the selected agent
// and returns the tea.Cmd from retargeting the right pane, which the caller
// must return into the bubbletea runtime.
func (m *Model) killCurrentAgent() tea.Cmd {
	a := m.currentAgent()
	p := m.currentProject()
	if a == nil || p == nil {
		return nil
	}
	wasVisible := m.rightPaneAgent == a.ID
	dead := a.Dead || !m.aliveCache[a.ID]
	if dead {
		// already dead — second d forgets for real. Worktree goes with it;
		// the branch survives unless it's fully merged (unmerged commits are
		// never silently deleted).
		if a.Worktree != "" {
			merged := session.BranchMerged(p.Path, a.Branch)
			_ = session.RemoveWorktree(p.Path, a.Worktree, a.Branch, merged)
		}
		m.store.RemoveAgent(p.Path, a.ID)
		if m.sidebarCur > 0 {
			m.sidebarCur--
		}
	} else {
		// soft-delete: kill tmux but keep state so enter can resume.
		_ = session.Kill(a.ID)
		m.store.MarkAgentDead(p.Path, a.ID)
		delete(m.aliveCache, a.ID)
	}
	_ = m.store.Save()
	// the right-pane attach to a killed session ends with tmux drawing a
	// "[exited]" stub; swap it to the next agent or hide it entirely.
	if wasVisible {
		return m.swapToCurrentAgent()
	}
	return nil
}

func (m Model) attachCurrent() (tea.Model, tea.Cmd) {
	a := m.currentAgent()
	p := m.currentProject()
	if a == nil {
		return m, nil
	}
	// dead agent → respawn with provider resume flag before showing it.
	if a.Dead || !m.aliveCache[a.ID] {
		spec, ok := session.Providers[a.Provider]
		if !ok || !spec.Available {
			return m, nil
		}
		w, h := m.bodyDims()
		dir := a.Dir
		if dir == "" && p != nil {
			dir = p.Path
		}
		var captureCmd tea.Cmd
		if len(spec.ResumeFlag) == 0 {
			var spawnErr error
			session.WithSpawnLock(a.Provider, dir, func() {
				spawnErr = session.Spawn(a.ID, dir, spec, w, h)
			})
			if spawnErr != nil {
				return m, nil
			}
			m.aliveCache[a.ID] = true
		} else {
			before := session.SnapshotFor(a.Provider, dir)
			uuid := a.SessionUUID
			if uuid != "" && !session.ValidUUID(uuid) {
				uuid = ""
				if p != nil {
					m.store.SetAgentUUID(p.Path, a.ID, "")
					_ = m.store.Save()
				}
			}
			var spawnErr error
			session.WithSpawnLock(a.Provider, dir, func() {
				spawnErr = session.SpawnWithResume(a.ID, dir, spec, w, h, session.ResumeOpts{
					Enabled: true,
					UUID:    uuid,
				})
			})
			if spawnErr != nil {
				return m, nil
			}
			m.aliveCache[a.ID] = true
			if uuid == "" && p != nil {
				captureCmd = captureUUIDCmd(a.Provider, dir, a.ID, before)
			}
		}
		session.SetSizeManual(a.ID)
		visCmd := m.ensureAgentVisible(a.ID)
		_ = session.SelectPane(m.rightPane)
		return m, tea.Batch(captureCmd, visCmd)
	}
	session.SetSizeManual(a.ID)
	visCmd := m.ensureAgentVisible(a.ID)
	_ = session.SelectPane(m.rightPane)
	return m, visCmd
}

// captureUUIDCmd runs UUID discovery in the background and posts the result
// back as a uuidCapturedMsg. Safe to fire for providers with no session dir —
// returns "" quickly in that case. Prefers the deterministic .sid spool
// (written by the claude SessionStart hook) over the mtime-diff watch.
func captureUUIDCmd(provider, dir, agentID string, before session.Snapshot) tea.Cmd {
	return func() tea.Msg {
		uuid := session.CaptureUUIDForAgent(agentID, provider, dir, before, 10*time.Second)
		return uuidCapturedMsg{projectPath: dir, agentID: agentID, uuid: uuid}
	}
}

// yankCurrent grabs the selected agent's recent output (plain text, trailing
// blanks trimmed) into the yank buffer for cross-agent handoff via p.
func (m Model) yankCurrent() (tea.Model, tea.Cmd) {
	a := m.currentAgent()
	if a == nil || a.Dead || !m.aliveCache[a.ID] {
		return m, nil
	}
	out, err := session.CapturePlain(a.ID, 200)
	if err != nil {
		return m, nil
	}
	out = strings.TrimRight(out, "\n \t")
	if strings.TrimSpace(out) == "" {
		return m, nil
	}
	m.yankBuf = out
	m.yankFrom = a.Name
	return m, nil
}

// pasteIntoCurrent bracketed-pastes the yank buffer into the selected agent's
// input, prefixed with provenance so the receiving agent knows what it reads.
// The paste lands in the input box unsubmitted — you review, then hit enter.
func (m Model) pasteIntoCurrent() (tea.Model, tea.Cmd) {
	if m.yankBuf == "" {
		return m, nil
	}
	a := m.currentAgent()
	if a == nil || a.Dead || !m.aliveCache[a.ID] {
		return m, nil
	}
	text := fmt.Sprintf("[output from agent %q]\n%s", m.yankFrom, m.yankBuf)
	_ = session.PasteText(a.ID, text)
	return m, nil
}

// projectFragment derives the session-name fragment for a project from its
// path rather than its display name (basename), so two projects with the
// same basename in different directories never collide. The basename keeps
// session ids readable; a short FNV-32a hash of the full path (4 hex chars)
// makes them distinct. session.SessionID sanitizes the fragment, matching
// prior behavior.
func projectFragment(path string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	return fmt.Sprintf("%s_%04x", filepath.Base(path), h.Sum32()&0xffff)
}

func pickSessionID(p *state.Project, provider string) string {
	frag := projectFragment(p.Path)
	used := map[string]struct{}{}
	for _, a := range p.Agents {
		used[a.ID] = struct{}{}
	}
	for n := 1; n < 10000; n++ {
		id := session.SessionID(frag, provider, n)
		if _, dup := used[id]; dup {
			continue
		}
		if session.Exists(id) {
			continue
		}
		return id
	}
	return session.SessionID(frag, provider, int(time.Now().Unix()))
}
