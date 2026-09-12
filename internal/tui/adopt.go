package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Amansingh-afk/mux/internal/session"
	"github.com/Amansingh-afk/mux/internal/state"
)

// adoptItem is one provider-native session offered for adoption, tagged with
// the provider it came from (SessionMeta itself is provider-agnostic).
type adoptItem struct {
	provider string
	meta     session.SessionMeta
}

type adoptListMsg struct {
	projectPath string
	items       []adoptItem
}

// adoptProviders are the providers whose native session logs we can discover.
// Mirrors what session.ListSessions supports.
var adoptProviders = []string{"claude", "codex"}

// listAdoptableCmd scans provider session dirs off the update loop — the codex
// scan walks a whole directory tree and must not block a keypress.
func listAdoptableCmd(projectPath string, known map[string]bool) tea.Cmd {
	return func() tea.Msg {
		var items []adoptItem
		for _, prov := range adoptProviders {
			metas, err := session.ListSessions(prov, projectPath)
			if err != nil {
				continue
			}
			for _, sm := range metas {
				if known[sm.UUID] {
					continue
				}
				items = append(items, adoptItem{provider: prov, meta: sm})
			}
		}
		// per-provider lists are newest-first already; merge order across
		// providers by mtime.
		for i := 1; i < len(items); i++ {
			for j := i; j > 0 && items[j].meta.ModTime.After(items[j-1].meta.ModTime); j-- {
				items[j], items[j-1] = items[j-1], items[j]
			}
		}
		return adoptListMsg{projectPath: projectPath, items: items}
	}
}

// startAdopt enters the adopt overlay and kicks off the async session scan.
func (m Model) startAdopt() (tea.Model, tea.Cmd) {
	p := m.currentProject()
	if p == nil {
		return m, nil
	}
	known := map[string]bool{}
	for _, a := range p.Agents {
		if a.SessionUUID != "" {
			known[a.SessionUUID] = true
		}
	}
	m.mode = modeAdopt
	m.adoptItems = nil
	m.adoptCur = 0
	m.adoptLoading = true
	return m, listAdoptableCmd(p.Path, known)
}

func (m Model) handleAdopt(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "i", "q":
		m.mode = modeNormal
		return m, nil
	case "j", "down":
		if m.adoptCur < len(m.adoptItems)-1 {
			m.adoptCur++
		}
		return m, nil
	case "k", "up":
		if m.adoptCur > 0 {
			m.adoptCur--
		}
		return m, nil
	case "enter":
		p := m.currentProject()
		if p == nil || m.adoptCur >= len(m.adoptItems) {
			m.mode = modeNormal
			return m, nil
		}
		it := m.adoptItems[m.adoptCur]
		id := pickSessionID(p, it.provider)
		name := session.NewCodename(codenamesTaken(p))
		m.store.AddAgent(p.Path, state.Agent{
			ID: id, Name: name, Provider: it.provider,
			SpawnedAt:   it.meta.ModTime.Unix(),
			Dir:         p.Path,
			SessionUUID: it.meta.UUID,
			Dead:        true, // no tmux session yet — enter resumes it
		})
		_ = m.store.Save()
		m.mode = modeNormal
		// move cursor onto the adopted agent so enter targets it.
		snap := m.store.Snapshot()
		for pi := range snap.Projects {
			if snap.Projects[pi].Path != p.Path {
				continue
			}
			for ai, a := range snap.Projects[pi].Agents {
				if a.ID == id {
					m.sidebarCur = ai
					m.store.SetLastAgent(p.Path, a.ID)
					break
				}
			}
		}
		return m, nil
	}
	return m, nil
}

// adoptAge renders a compact relative age for the session list.
func adoptAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (m Model) renderAdoptPicker() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("adopt session")
	rows := []string{title, ""}

	const boxW = 72
	const maxRows = 12

	switch {
	case m.adoptLoading:
		rows = append(rows, styleStatus.Render("scanning provider sessions…"))
	case len(m.adoptItems) == 0:
		rows = append(rows, styleStatus.Render("no unadopted sessions found for this project"))
	default:
		// window the list around the cursor.
		start := 0
		if m.adoptCur >= maxRows {
			start = m.adoptCur - maxRows + 1
		}
		end := start + maxRows
		if end > len(m.adoptItems) {
			end = len(m.adoptItems)
		}
		for i := start; i < end; i++ {
			it := m.adoptItems[i]
			marker := "  "
			if i == m.adoptCur {
				marker = styleSelMarker.Render("▸ ")
			}
			age := lipgloss.NewStyle().Foreground(colorDim).Width(4).Render(adoptAge(it.meta.ModTime))
			prev := it.meta.Preview
			if prev == "" {
				prev = "(no preview)"
			}
			line := marker + providerBadge(it.provider, false) + " " + age + " " +
				lipgloss.NewStyle().Foreground(colorFg).Render(truncName(prev, boxW-20))
			rows = append(rows, clipLine(line, boxW-6))
		}
		if len(m.adoptItems) > maxRows {
			rows = append(rows, styleStatus.Render(fmt.Sprintf("%d/%d", m.adoptCur+1, len(m.adoptItems))))
		}
	}

	rows = append(rows, "", styleStatus.Render("↓↑ select  enter adopt  esc cancel"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFaint).
		Padding(1, 2).
		Width(boxW).
		Render(strings.Join(rows, "\n"))
}
