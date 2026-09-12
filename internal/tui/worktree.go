package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amansingh-afk/mux/internal/session"
)

// worktreeSetup is the configured [worktree] setup command, run in every
// fresh agent worktree before the agent starts. Set once at startup.
var worktreeSetup string

func SetWorktreeSetup(cmd string) { worktreeSetup = cmd }

// mergeDoneMsg reports the result of an async M merge.
type mergeDoneMsg struct {
	name     string
	base     string
	conflict bool
	out      string
	err      error
}

// viewDiffCurrent shows the selected agent's full delta — committed branch
// work vs the base plus uncommitted worktree changes — in a transient pager
// session swapped into the right pane. Quitting the pager hops the pane back
// to the agent (detach-on-destroy off).
func (m Model) viewDiffCurrent() (tea.Model, tea.Cmd) {
	a := m.currentAgent()
	p := m.currentProject()
	if a == nil || p == nil {
		return m, nil
	}
	if a.Worktree == "" || a.Branch == "" {
		return m, nil // shell / non-git — nothing to diff
	}
	if m.rightClient == "" {
		return m, nil // no agent pane to show the diff in
	}
	// mux is its own diff pager: full-screen review UI with merge prediction
	// in the header, styled to match. Handles the empty case itself.
	exe, err := os.Executable()
	if err != nil {
		return m, nil
	}
	cmd := fmt.Sprintf("%q --diff %q %q %q", exe, p.Path, a.Worktree, a.Branch)
	viewID := "mux_view_" + projectFragment(p.Path)
	if err := session.RunTransient(viewID, p.Path, cmd, a.ID); err != nil {
		return m, nil
	}
	if err := session.SwitchClient(m.rightClient, viewID); err != nil {
		return m, nil
	}
	// focus the pager so q/j/k drive it — without this, q lands on mux and
	// quits the whole app.
	_ = session.SelectPane(m.rightPane)
	return m, nil
}

// mergeCurrent merges the selected agent's branch into the base tree,
// asynchronously (merges can run hooks / take seconds).
func (m Model) mergeCurrent() (tea.Model, tea.Cmd) {
	a := m.currentAgent()
	p := m.currentProject()
	if a == nil || p == nil {
		return m, nil
	}
	if a.Worktree == "" || a.Branch == "" {
		return m, nil
	}
	base, wt, branch, name := p.Path, a.Worktree, a.Branch, a.Name
	return m, func() tea.Msg {
		res := session.MergeAgent(base, wt, branch)
		return mergeDoneMsg{name: name, base: base, conflict: res.Conflict, out: res.Out, err: res.Err}
	}
}

// mergeFailReason pulls git's actual explanation (error:/fatal: line) out of
// the merge output — "exit status 1" alone tells the user nothing.
func mergeFailReason(out string, err error) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "fatal:") {
			return l
		}
	}
	if s := strings.TrimSpace(out); s != "" {
		if i := strings.IndexByte(s, '\n'); i > 0 {
			s = s[:i]
		}
		return s
	}
	return err.Error()
}

// handleMergeDone folds the merge result in; on conflict it swaps the right
// pane to a shell in the base tree so the human resolves with plain git.
func (m Model) handleMergeDone(msg mergeDoneMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.conflict:
		// resolver of choice: lazygit's merge-conflict UI in the base tree
		// (pick hunks side by side, commit, done — q returns to the agent);
		// fallback is a plain shell for manual git. mux itself never renders
		// conflicts.
		resolver := os.Getenv("SHELL")
		if resolver == "" {
			resolver = "sh"
		}
		if _, err := exec.LookPath("lazygit"); err == nil {
			resolver = "lazygit"
		}
		if m.rightClient != "" {
			id := "mux_conflict_" + projectFragment(msg.base)
			// no `exec` — the return-switch must run after the resolver exits.
			if err := session.RunTransient(id, msg.base, resolver, m.rightPaneAgent); err == nil {
				_ = session.SwitchClient(m.rightClient, id)
				_ = session.SelectPane(m.rightPane)
			}
		}
	case msg.err != nil:
		// failure detail renders as a styled page in the review pane — the
		// sidebar stays chrome-free.
		if m.rightClient != "" {
			if exe, eerr := os.Executable(); eerr == nil {
				id := "mux_notice_" + projectFragment(msg.base)
				cmd := fmt.Sprintf("%q --notice %q %q", exe,
					"merge failed: "+msg.name, mergeFailReason(msg.out, msg.err))
				if err := session.RunTransient(id, msg.base, cmd, m.rightPaneAgent); err == nil {
					_ = session.SwitchClient(m.rightClient, id)
					_ = session.SelectPane(m.rightPane)
				}
			}
		}
	}
	return m, nil
}
