package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amansingh-afk/mux/internal/session"
)

func (m Model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.loading {
		return m.renderBootSplash()
	}
	gapTop := ""
	gapBot := ""
	// project tabs live in tmux's full-width status line (pushTabBar).
	// mux pane is just the sidebar — no in-mux top tabs, no bottom bar.
	sidebarH := m.h - 2
	if sidebarH < 3 {
		sidebarH = 3
		gapTop, gapBot = "", ""
	} else {
		gapTop = strings.Repeat(" ", m.w)
		gapBot = gapTop
	}
	sidebarW := 24
	if sidebarW > m.w {
		sidebarW = m.w
	}
	sidebarInnerW := sidebarW - 2 // styleSidebar Padding(0, 1)
	if sidebarInnerW < 8 {
		sidebarInnerW = 8
	}
	sidebar := styleSidebar.Width(sidebarW).Height(sidebarH).Render(m.renderSidebar(sidebarInnerW, sidebarH))
	view := lipgloss.JoinVertical(lipgloss.Left, gapTop, sidebar, gapBot)

	if m.mode == modeOpenProject {
		return m.overlay(m.renderRepoPicker())
	}
	if m.mode == modeSpawnAgent {
		return m.overlay(m.renderProviderPicker())
	}
	if m.mode == modeRenameAgent {
		return m.overlay(m.renderRenameModal())
	}
	if m.mode == modeHelp {
		return m.overlay(m.renderHelp())
	}
	if m.mode == modeAdopt {
		return m.overlay(m.renderAdoptPicker())
	}
	return view
}

// renderBootSplash paints the startup screen: mux owns the whole window until
// bootRevealMsg splits the agent pane in, so the wordmark gets center stage.
// The spinner rides the existing 120ms spinTick.
func (m Model) renderBootSplash() string {
	wordmark := []string{
		"█▀▄▀█ █░█ ▀▄▀",
		"█░▀░█ █▄█ █░█",
	}
	art := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
		Render(strings.Join(wordmark, "\n"))
	frame := session.SpinnerFrames[m.spinnerFrame%len(session.SpinnerFrames)]
	sub := lipgloss.NewStyle().Foreground(colorDim).
		Render(frame + " waking agents")
	return m.overlay(lipgloss.JoinVertical(lipgloss.Center, art, "", sub))
}

func (m Model) renderRenameModal() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("rename agent")
	prompt := "› " + m.renameBuf + "▎"
	hint := styleStatus.Render("enter save  esc cancel  (empty = new codename)")
	inner := strings.Join([]string{title, "", prompt, "", hint}, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(1, 2).
		Width(46).
		Render(inner)
}

func (m Model) overlay(content string) string {
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, content)
}

func truncName(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// providerBadge renders a provider name in its brand color. Dead agents
// can override via the `dim` flag for strikethrough/greyed styling.
func providerBadge(name string, dim bool) string {
	spec, ok := session.Providers[name]
	col := colorDim
	if ok && spec.Color != "" {
		col = lipgloss.Color(spec.Color)
	}
	st := lipgloss.NewStyle().Foreground(col)
	if dim {
		st = lipgloss.NewStyle().Foreground(colorDim).Strikethrough(true)
	}
	return st.Render(name)
}

// providerGlyphs are single-cell marks for the sidebar's right column — the
// brand color carries the identity, the glyph just distinguishes at a glance.
var providerGlyphs = map[string]string{
	"claude": "✳",
	"codex":  "⬡",
	"gemini": "✦",
	"cursor": "▮",
	"shell":  "$",
}

// providerIcon renders a provider's single-glyph mark in its brand color.
// Unknown (config-added) providers fall back to their first letter.
func providerIcon(name string, dim bool) string {
	g, ok := providerGlyphs[name]
	if !ok {
		g = "?"
		for _, r := range name {
			g = string(r)
			break
		}
	}
	col := colorDim
	if spec, ok := session.Providers[name]; ok && spec.Color != "" && !dim {
		col = lipgloss.Color(spec.Color)
	}
	return lipgloss.NewStyle().Foreground(col).Render(g)
}

func (m Model) renderSidebar(w, h int) string {
	p := m.currentProject()
	if p == nil {
		return styleSectionHeader.Render("NO PROJECT") + "\n" +
			styleDivider.Render(strings.Repeat("─", min(w, 20))) + "\n\n" +
			lipgloss.NewStyle().Foreground(colorDim).Render("press o to open")
	}

	header := styleSectionHeader.Render("AGENTS")
	divider := styleDivider.Render(strings.Repeat("─", min(w, 20)))

	var agentRows []string
	if len(p.Agents) == 0 {
		agentRows = []string{
			lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("  press n to spawn"),
		}
	} else {
		agentRows = make([]string, 0, len(p.Agents))
		for i, a := range p.Agents {
			icon := m.statusIcon(a.ID)
			label := a.Name
			if label == "" {
				label = fmt.Sprintf("agent-%d", i+1)
			}
			dead := a.Dead || !m.aliveCache[a.ID]

			// fixed columns: marker(2) status(1) gap(1) name(flex) icon(1),
			// provider icon right-aligned so rows line up regardless of name
			// length.
			marker := "  "
			labelStyle := lipgloss.NewStyle().Foreground(colorFg)
			if dead {
				labelStyle = labelStyle.Foreground(colorDim).Strikethrough(true)
			}
			if i == m.sidebarCur {
				marker = styleSelMarker.Render("▸ ")
				labelStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
				if dead {
					labelStyle = lipgloss.NewStyle().Foreground(colorDim).Bold(true).Strikethrough(true)
				}
			}
			pIcon := providerIcon(a.Provider, dead)
			nameW := w - 6 // marker 2 + status 1 + gap 1 + gap 1 + icon 1
			if nameW < 4 {
				nameW = 4
			}
			left := marker + icon + " " + labelStyle.Render(truncName(label, nameW))
			pad := w - lipgloss.Width(left) - lipgloss.Width(pIcon)
			if pad < 1 {
				pad = 1
			}
			agentRows = append(agentRows, clipLine(left+strings.Repeat(" ", pad)+pIcon, w))
		}
	}

	// viewport windowing (reserve: 3 for agents header block + 4 for project footer)
	headerBlock := 3 // HEADER, divider, blank
	footerBlock := 5 // blank, PROJECT header, divider, path, branch
	available := h - headerBlock - footerBlock
	if available < 1 {
		available = h - headerBlock
		if available < 1 {
			available = 1
		}
		footerBlock = 0
	}

	start, end := 0, len(agentRows)
	if len(agentRows) > available {
		start = m.sidebarCur - available/2
		if start < 0 {
			start = 0
		}
		if start+available > len(agentRows) {
			start = len(agentRows) - available
		}
		end = start + available
	}

	var out []string
	out = append(out, clipLine(header, w), clipLine(divider, w), "")
	out = append(out, agentRows[start:end]...)

	if footerBlock > 0 {
		projectName := lipgloss.NewStyle().Foreground(colorFg).Render(truncName(p.Name, w-2))
		projectPath := lipgloss.NewStyle().Foreground(colorDim).Render(truncName(displayPath(p.Path), w-2))
		// pad agent area so project block hugs the bottom
		pad := available - (end - start)
		for k := 0; k < pad; k++ {
			out = append(out, "")
		}
		out = append(out, "")
		out = append(out, clipLine(styleSectionHeader.Render("PROJECT"), w))
		out = append(out, clipLine(divider, w))
		out = append(out, clipLine(projectName, w))
		out = append(out, clipLine(projectPath, w))
	}

	return strings.Join(out, "\n")
}

func (m Model) renderHelp() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("mux — keybindings")
	sec := func(h string) string {
		return lipgloss.NewStyle().Bold(true).Foreground(colorAccent).MarginTop(1).Render(h)
	}
	key := lipgloss.NewStyle().Foreground(colorAccent).Width(14)
	desc := lipgloss.NewStyle().Foreground(colorDim)
	row := func(k, d string) string {
		return key.Render(k) + desc.Render(d)
	}

	lines := []string{
		title,
		sec("projects"),
		row("o", "open project (fzf)"),
		row("w", "close tab (agents survive)"),
		row("W", "close tab + kill all agents"),
		row("[ / ]", "prev / next tab"),
		row("1 – 9", "jump to tab N"),
		sec("agents"),
		row("n", "spawn agent in current project"),
		row("enter", "show + focus selected agent (C-b ← to return)"),
		row("d", "kill agent (again to forget; enter to resume)"),
		row("r", "rename selected agent"),
		row("i", "adopt existing provider session"),
		row("j / k", "move agent cursor"),
		row("z", "zen mode (zoom agent fullscreen, C-b z to exit)"),
		sec("anywhere (even inside an agent)"),
		row("M-j / M-k", "next / prev agent"),
		row("M-1 – M-9", "jump to tab N"),
		row("M-[ / M-]", "prev / next tab"),
		row("M-space", "toggle focus mux ↔ agent"),
		sec("misc"),
		row("?", "toggle this help"),
		row("q / C-c", "quit"),
		"",
		lipgloss.NewStyle().Foreground(colorDim).Render("esc or ? to close"),
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(1, 2).
		Width(52).
		Render(strings.Join(lines, "\n"))
}

func (m Model) renderRepoPicker() string {
	boxW := m.w * 2 / 3
	if boxW < 50 {
		boxW = 50
	}
	if boxW > 90 {
		boxW = 90
	}
	maxRows := m.h - 10
	if maxRows < 8 {
		maxRows = 8
	}
	if maxRows > 18 {
		maxRows = 18
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("open project")
	prompt := "› " + m.picker.query + "▎"

	rows := make([]string, 0, maxRows)
	start := 0
	if m.picker.cur >= maxRows {
		start = m.picker.cur - maxRows + 1
	}
	end := start + maxRows
	if end > len(m.picker.view) {
		end = len(m.picker.view)
	}
	for i := start; i < end; i++ {
		it := m.picker.view[i]
		tag := lipgloss.NewStyle().Foreground(colorDim).Render("[" + it.tag + "]")
		line := it.name + "  " + lipgloss.NewStyle().Foreground(colorDim).Render(displayPath(it.path)) + "  " + tag
		if i == m.picker.cur {
			line = styleListItemSel.Render("▸ " + it.name + "  " + displayPath(it.path) + "  " + it.tag)
		} else {
			line = "  " + line
		}
		rows = append(rows, line)
	}
	if len(rows) == 0 {
		rows = append(rows, styleStatus.Render("  no matches"))
	}

	help := styleStatus.Render("↓↑ select  enter open  esc cancel  · type to filter, or paste any path")
	inner := strings.Join([]string{title, prompt, strings.Repeat("─", boxW-4), strings.Join(rows, "\n"), help}, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(1, 2).
		Width(boxW).
		Render(inner)
}

func (m Model) renderProviderPicker() string {
	names := session.ProviderNames()
	title := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("spawn agent")

	rows := []string{title, ""}
	for i, n := range names {
		spec := session.Providers[n]
		selected := i == m.providerCur

		// name in brand color when available, dim otherwise. selected gets
		// the marker + bold.
		col := colorDim
		if spec.Available && spec.Color != "" {
			col = lipgloss.Color(spec.Color)
		}
		nameSt := lipgloss.NewStyle().Foreground(col)
		if selected {
			nameSt = nameSt.Bold(true)
		}
		name := nameSt.Render(spec.Name)

		marker := "  "
		if selected {
			marker = styleSelMarker.Render("▸ ")
		}
		line := marker + name
		if !spec.Available {
			line += lipgloss.NewStyle().Foreground(colorErr).Render("  · not installed")
		}
		rows = append(rows, line)
	}

	cur := session.Providers[names[m.providerCur]]
	footer := styleStatus.Render("↓↑ select  enter spawn  esc cancel")
	if !cur.Available && cur.Hint != "" {
		footer = lipgloss.NewStyle().Foreground(colorWarn).Render("install: "+cur.Hint) + "\n" + footer
	}
	rows = append(rows, "", footer)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFaint).
		Padding(1, 2).
		Width(52).
		Render(strings.Join(rows, "\n"))
}

func clipLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "")
}
