package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ashmit/mux/internal/session"
	"github.com/ashmit/mux/internal/state"
)

type mode int

const (
	modeNormal mode = iota
	modeOpenProject
	modeSpawnAgent
	modeRenameAgent
	modeHelp
)

type Model struct {
	store *state.Store
	w, h  int

	mode mode

	activeTab    int
	sidebarCur   int
	providerCur  int
	picker       repoPicker
	renameBuf    string
	statusMsg    string
	previewCache map[string]string
	aliveCache   map[string]bool
	statusRec    map[string]agentStatusRec
	spinnerFrame int
}

type agentStatusRec struct {
	status      session.Status
	hash        uint64
	stableTicks int
}

type tickMsg time.Time
type spinTickMsg time.Time
type previewMsg struct {
	id  string
	out string
}
type statusBatchMsg []previewMsg
type aliveMsg map[string]bool
type uuidCapturedMsg struct {
	projectPath string
	agentID     string
	uuid        string
}

func New(s *state.Store, alive map[string]bool) Model {
	if alive == nil {
		alive = map[string]bool{}
	}
	m := Model{
		store:        s,
		previewCache: map[string]string{},
		aliveCache:   alive,
		statusRec:    map[string]agentStatusRec{},
	}
	snap := s.Snapshot()
	if snap.ActiveTab != "" {
		for i, t := range snap.OpenTabs {
			if t == snap.ActiveTab {
				m.activeTab = i
				break
			}
		}
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tick(), spinTick(), m.refreshPreview(), refreshAlive())
}

func spinTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return spinTickMsg(t) })
}

func refreshAlive() tea.Cmd {
	return func() tea.Msg {
		ids, err := session.List()
		out := map[string]bool{}
		if err == nil {
			for _, id := range ids {
				out[id] = true
			}
		}
		return aliveMsg(out)
	}
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) refreshPreview() tea.Cmd {
	a := m.currentAgent()
	if a == nil {
		return nil
	}
	id := a.ID
	w, h := m.bodyDims()
	return func() tea.Msg {
		_ = session.Resize(id, w, h)
		out, err := session.Capture(id, h+50)
		if err != nil {
			return previewMsg{id: id, out: fmt.Sprintf("(no preview: %v)", err)}
		}
		return previewMsg{id: id, out: out}
	}
}

// captureAllForStatus captures every agent's pane content so we can classify
// state for each. Unlike refreshPreview (current agent only) this touches all
// sessions but fires asynchronously.
func (m Model) captureAllForStatus() tea.Cmd {
	snap := m.store.Snapshot()
	type cap struct {
		id  string
		out string
	}
	var ids []string
	for _, p := range snap.Projects {
		for _, a := range p.Agents {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return func() tea.Msg {
		// return a batch of previewMsg via a slice wrapper
		caps := make([]previewMsg, 0, len(ids))
		for _, id := range ids {
			out, err := session.Capture(id, 40)
			if err != nil {
				continue
			}
			caps = append(caps, previewMsg{id: id, out: out})
		}
		return statusBatchMsg(caps)
	}
}

func (m Model) resizeAllSessions() tea.Cmd {
	w, h := m.bodyDims()
	snap := m.store.Snapshot()
	return func() tea.Msg {
		for _, p := range snap.Projects {
			for _, a := range p.Agents {
				_ = session.Resize(a.ID, w, h)
			}
		}
		return nil
	}
}

// bodyDims returns the content area of the body pane (cols, rows).
func (m Model) bodyDims() (int, int) {
	sidebarW := 26
	if m.w < 80 {
		sidebarW = 22
	}
	sidebarActual := sidebarW + 3 // padding(2) + border(1)
	w := m.w - sidebarActual - 1  // body left padding
	h := m.h - 2                  // tab bar + status bar
	if w < 40 {
		w = 40
	}
	if h < 10 {
		h = 10
	}
	return w, h
}

func (m Model) currentProject() *state.Project {
	snap := m.store.Snapshot()
	if len(snap.OpenTabs) == 0 {
		return nil
	}
	if m.activeTab >= len(snap.OpenTabs) {
		return nil
	}
	path := snap.OpenTabs[m.activeTab]
	for i := range snap.Projects {
		if snap.Projects[i].Path == path {
			return &snap.Projects[i]
		}
	}
	return nil
}

func (m Model) projectPathForAgent(id string) (string, bool) {
	snap := m.store.Snapshot()
	for _, p := range snap.Projects {
		for _, a := range p.Agents {
			if a.ID == id {
				return p.Path, true
			}
		}
	}
	return "", false
}

func (m Model) currentAgent() *state.Agent {
	p := m.currentProject()
	if p == nil || len(p.Agents) == 0 {
		return nil
	}
	if m.sidebarCur >= len(p.Agents) {
		return &p.Agents[len(p.Agents)-1]
	}
	return &p.Agents[m.sidebarCur]
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, m.resizeAllSessions()

	case tickMsg:
		return m, tea.Batch(tick(), m.refreshPreview(), refreshAlive(), m.captureAllForStatus())

	case spinTickMsg:
		m.spinnerFrame++
		return m, spinTick()

	case previewMsg:
		m.previewCache[msg.id] = msg.out
		rec := m.statusRec[msg.id]
		s, h, t := session.Classify(msg.out, rec.hash, rec.status, rec.stableTicks)
		m.statusRec[msg.id] = agentStatusRec{status: s, hash: h, stableTicks: t}
		// stash preview on the agent so dead state still shows something after
		// a reboot. keep it small.
		if path, ok := m.projectPathForAgent(msg.id); ok {
			trimmed := msg.out
			if len(trimmed) > 4000 {
				trimmed = trimmed[len(trimmed)-4000:]
			}
			m.store.SetAgentPreview(path, msg.id, trimmed)
		}
		return m, nil

	case statusBatchMsg:
		for _, p := range msg {
			rec := m.statusRec[p.id]
			s, h, t := session.Classify(p.out, rec.hash, rec.status, rec.stableTicks)
			m.statusRec[p.id] = agentStatusRec{status: s, hash: h, stableTicks: t}
		}
		return m, nil

	case aliveMsg:
		m.aliveCache = msg
		// reconcile state.Dead against live set so dead agents flip on
		// spontaneously (e.g. tmux socket went away mid-session).
		live := map[string]struct{}{}
		for id := range msg {
			live[id] = struct{}{}
		}
		m.store.Reconcile(live)
		return m, nil

	case uuidCapturedMsg:
		if msg.uuid != "" && session.ValidUUID(msg.uuid) {
			m.store.SetAgentUUID(msg.projectPath, msg.agentID, msg.uuid)
			_ = m.store.Save()
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
		return m, tea.Quit

	case "j", "down":
		p := m.currentProject()
		if p != nil && m.sidebarCur < len(p.Agents)-1 {
			m.sidebarCur++
			return m, m.refreshPreview()
		}
		return m, nil

	case "k", "up":
		if m.sidebarCur > 0 {
			m.sidebarCur--
			return m, m.refreshPreview()
		}
		return m, nil

	case "[", "h", "left", "shift+tab":
		return m.prevTab()

	case "]", "l", "right":
		return m.nextTab()

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.jumpTab(int(k.String()[0] - '1'))

	case "o":
		m.mode = modeOpenProject
		snap := m.store.Snapshot()
		m.picker = newRepoPicker(snap.Projects)
		return m, nil

	case "w":
		m.closeCurrentTab(false)
		return m, m.refreshPreview()

	case "W":
		m.closeCurrentTab(true)
		return m, m.refreshPreview()

	case "n":
		if m.currentProject() == nil {
			m.statusMsg = "open a project first (o)"
			return m, nil
		}
		m.mode = modeSpawnAgent
		m.providerCur = 0
		return m, nil

	case "d":
		m.killCurrentAgent()
		return m, m.refreshPreview()

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

	case "?":
		m.mode = modeHelp
		return m, nil
	}
	return m, nil
}

func (m Model) prevTab() (tea.Model, tea.Cmd) {
	if m.activeTab > 0 {
		m.activeTab--
		m.sidebarCur = 0
		m.setActiveFromIndex()
		return m, m.refreshPreview()
	}
	return m, nil
}

func (m Model) nextTab() (tea.Model, tea.Cmd) {
	snap := m.store.Snapshot()
	if m.activeTab < len(snap.OpenTabs)-1 {
		m.activeTab++
		m.sidebarCur = 0
		m.setActiveFromIndex()
		return m, m.refreshPreview()
	}
	return m, nil
}

func (m Model) jumpTab(i int) (tea.Model, tea.Cmd) {
	snap := m.store.Snapshot()
	if i < 0 || i >= len(snap.OpenTabs) {
		return m, nil
	}
	m.activeTab = i
	m.sidebarCur = 0
	m.setActiveFromIndex()
	return m, m.refreshPreview()
}

func (m *Model) setActiveFromIndex() {
	snap := m.store.Snapshot()
	if m.activeTab < len(snap.OpenTabs) {
		m.store.SetActive(snap.OpenTabs[m.activeTab])
	}
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
		return m, m.refreshPreview()
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
			if spec.Hint != "" {
				m.statusMsg = spec.Name + " not installed — " + spec.Hint
			} else {
				m.statusMsg = spec.Name + " not found in PATH"
			}
			return m, nil
		}
		id := pickSessionID(p, provider)
		name := session.NewCodename(codenamesTaken(p))
		w, h := m.bodyDims()
		projectPath := p.Path
		before := session.SnapshotFor(provider, projectPath)
		var spawnErr error
		session.WithSpawnLock(provider, projectPath, func() {
			spawnErr = session.Spawn(id, projectPath, spec, w, h)
		})
		if spawnErr != nil {
			m.statusMsg = "spawn failed: " + spawnErr.Error()
			m.mode = modeNormal
			return m, nil
		}
		m.store.AddAgent(p.Path, state.Agent{
			ID: id, Name: name, Provider: provider,
			SpawnedAt: time.Now().Unix(),
			Dir:       projectPath,
			LastSeen:  time.Now().Unix(),
		})
		m.aliveCache[id] = true
		_ = m.store.Save()
		m.mode = modeNormal
		m.statusMsg = "spawned " + name
		return m, tea.Batch(m.refreshPreview(), captureUUIDCmd(provider, projectPath, id, before))
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
			m.statusMsg = "renamed → " + name
		}
		m.mode = modeNormal
		return m, nil
	case "backspace":
		if len(m.renameBuf) > 0 {
			m.renameBuf = m.renameBuf[:len(m.renameBuf)-1]
		}
		return m, nil
	default:
		s := k.String()
		if len(s) == 1 {
			m.renameBuf += s
		}
		return m, nil
	}
}

func (m *Model) closeCurrentTab(killAgents bool) {
	snap := m.store.Snapshot()
	if m.activeTab >= len(snap.OpenTabs) {
		return
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
}

func (m *Model) killCurrentAgent() {
	a := m.currentAgent()
	p := m.currentProject()
	if a == nil || p == nil {
		return
	}
	dead := a.Dead || !m.aliveCache[a.ID]
	if dead {
		// already dead — second d forgets for real.
		m.store.RemoveAgent(p.Path, a.ID)
		if m.sidebarCur > 0 {
			m.sidebarCur--
		}
		m.statusMsg = "forgot " + a.Name
	} else {
		// soft-delete: kill tmux but keep state so enter can resume.
		_ = session.Kill(a.ID)
		m.store.MarkAgentDead(p.Path, a.ID)
		delete(m.aliveCache, a.ID)
		m.statusMsg = a.Name + " killed — enter to resume, d again to forget"
	}
	_ = m.store.Save()
}

func (m Model) attachCurrent() (tea.Model, tea.Cmd) {
	a := m.currentAgent()
	p := m.currentProject()
	if a == nil {
		m.statusMsg = "no agent to attach"
		return m, nil
	}
	// dead agent → respawn with provider resume flag before attaching
	if a.Dead || !m.aliveCache[a.ID] {
		spec, ok := session.Providers[a.Provider]
		if !ok || !spec.Available {
			m.statusMsg = a.Provider + " not available to resume"
			return m, nil
		}
		if len(spec.ResumeFlag) == 0 {
			m.statusMsg = a.Provider + " has no resume flag"
			return m, nil
		}
		w, h := m.bodyDims()
		dir := a.Dir
		if dir == "" && p != nil {
			dir = p.Path
		}
		before := session.SnapshotFor(a.Provider, dir)
		uuid := a.SessionUUID
		if uuid != "" && !session.ValidUUID(uuid) {
			// stale/malformed from earlier buggy capture — strip + fall to picker.
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
			m.statusMsg = "resume failed: " + spawnErr.Error()
			return m, nil
		}
		m.aliveCache[a.ID] = true
		hint := "resumed " + a.Name
		if uuid == "" {
			hint += " (provider picker)"
		}
		m.statusMsg = hint
		// if we didn't have a uuid, try to capture it so next resume is direct
		var captureCmd tea.Cmd
		if uuid == "" && p != nil {
			captureCmd = captureUUIDCmd(a.Provider, dir, a.ID, before)
		}
		session.SetSizeLatest(a.ID)
		cmd := session.AttachCmd(a.ID)
		exec := tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				return previewMsg{id: a.ID, out: "attach err: " + err.Error()}
			}
			return tickMsg(time.Now())
		})
		seq := tea.Sequence(exec, tea.EnterAltScreen, tea.ClearScreen)
		if captureCmd != nil {
			return m, tea.Batch(seq, captureCmd)
		}
		return m, seq
	}
	session.SetSizeLatest(a.ID)
	cmd := session.AttachCmd(a.ID)
	exec := tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return previewMsg{id: a.ID, out: "attach err: " + err.Error()}
		}
		return tickMsg(time.Now())
	})
	return m, tea.Sequence(exec, tea.EnterAltScreen, tea.ClearScreen)
}

// captureUUIDCmd runs UUID discovery in the background and posts the result
// back as a uuidCapturedMsg. Safe to fire for providers with no session dir —
// returns "" quickly in that case.
func captureUUIDCmd(provider, dir, agentID string, before session.Snapshot) tea.Cmd {
	return func() tea.Msg {
		uuid := session.CaptureUUID(provider, dir, before, 10*time.Second)
		return uuidCapturedMsg{projectPath: dir, agentID: agentID, uuid: uuid}
	}
}

func (m Model) View() string {
	if m.w == 0 {
		return "starting..."
	}
	tabs := m.renderTabs()
	status := m.renderStatus()
	bodyH := m.h - lipgloss.Height(tabs) - lipgloss.Height(status)
	if bodyH < 3 {
		bodyH = 3
	}
	sidebarW := 26
	if m.w < 80 {
		sidebarW = 22
	}
	sidebar := styleSidebar.Width(sidebarW).Height(bodyH).Render(m.renderSidebar(bodyH))
	actualSW := lipgloss.Width(sidebar)
	bodyTotalW := m.w - actualSW
	if bodyTotalW < 10 {
		bodyTotalW = 10
	}
	bodyContentW := bodyTotalW - 1 // left padding
	body := styleBody.Width(bodyTotalW).Height(bodyH).Render(m.renderBody(bodyContentW, bodyH))

	row := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, body)
	view := lipgloss.JoinVertical(lipgloss.Left, tabs, row, status)

	if m.mode == modeOpenProject {
		return m.overlay(view, m.renderRepoPicker())
	}
	if m.mode == modeSpawnAgent {
		return m.overlay(view, m.renderProviderPicker())
	}
	if m.mode == modeRenameAgent {
		return m.overlay(view, m.renderRenameModal())
	}
	if m.mode == modeHelp {
		return m.overlay(view, m.renderHelp())
	}
	return view
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

func (m Model) overlay(base, content string) string {
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, content)
}

func (m Model) renderTabs() string {
	snap := m.store.Snapshot()
	if len(snap.OpenTabs) == 0 {
		return clipLine(styleTab.Render("no tabs — press o to open project"), m.w)
	}
	budget := m.w - 6 // reserve for overflow indicator
	if budget < 10 {
		budget = m.w
	}
	var parts []string
	used := 0
	shown := 0
	for i, path := range snap.OpenTabs {
		name := truncName(filepath.Base(path), 12)
		dot := " "
		var rendered string
		if i == m.activeTab {
			rendered = styleTabActive.Render(dot + name)
		} else {
			rendered = styleTab.Render(dot + name)
		}
		w := lipgloss.Width(rendered)
		if used+w > budget && i != m.activeTab {
			continue
		}
		if used+w > m.w {
			break
		}
		parts = append(parts, rendered)
		used += w
		shown++
	}
	joined := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	if shown < len(snap.OpenTabs) {
		more := styleTab.Render(fmt.Sprintf(" +%d", len(snap.OpenTabs)-shown))
		joined = lipgloss.JoinHorizontal(lipgloss.Top, joined, more)
	}
	// Fill remainder with tab-bar background so the row is visible end-to-end.
	tabW := lipgloss.Width(joined)
	if tabW < m.w {
		pad := styleTabBar.Render(strings.Repeat(" ", m.w-tabW))
		joined = lipgloss.JoinHorizontal(lipgloss.Top, joined, pad)
	}
	return clipLine(joined, m.w)
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

func (m Model) renderSidebar(h int) string {
	p := m.currentProject()
	if p == nil {
		return styleStatus.Render("no project")
	}
	title := lipgloss.NewStyle().Bold(true).Render("agents")
	if len(p.Agents) == 0 {
		return title + "\n" + styleStatus.Render("press n to spawn")
	}
	lines := []string{title}
	for i, a := range p.Agents {
		icon := m.statusIcon(a.ID)
		label := a.Name
		if label == "" {
			label = fmt.Sprintf("agent-%d", i+1)
		}
		dead := a.Dead || !m.aliveCache[a.ID]
		labelStyle := lipgloss.NewStyle()
		provStyle := lipgloss.NewStyle().Foreground(colorDim)
		if dead {
			labelStyle = labelStyle.Foreground(colorDim).Strikethrough(true)
			provStyle = provStyle.Strikethrough(true)
		}
		provText := provStyle.Render(a.Provider)
		var row string
		if i == m.sidebarCur {
			marker := styleSelMarker.Render("▸")
			if dead {
				row = marker + " " + icon + " " + labelStyle.Render(label) + " " + provText
			} else {
				selLabel := styleListItemSel.Render(label)
				selProv := styleListItemSel.Render(a.Provider)
				row = marker + " " + icon + " " + selLabel + " " + selProv
			}
		} else {
			row = "  " + icon + " " + labelStyle.Render(label) + " " + provText
		}
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
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

func (m Model) renderBody(w, h int) string {
	p := m.currentProject()
	a := m.currentAgent()
	if p == nil {
		return renderSplash(w, h, "open a project to begin", []string{
			"o   open project",
			"?   help",
			"q   quit",
		})
	}
	if a == nil {
		return renderSplash(w, h, "no agents in "+p.Name, []string{
			"n   spawn agent",
			"o   open another project",
			"?   help",
		})
	}
	dead := a.Dead || !m.aliveCache[a.ID]
	if dead {
		banner := lipgloss.NewStyle().Foreground(colorWarn).Bold(true).
			Render("● agent dead — press enter to resume")
		body := a.LastPreview
		if body == "" {
			body = lipgloss.NewStyle().Foreground(colorDim).Italic(true).
				Render("(no captured preview — provider picker will open on resume)")
		}
		body = strings.TrimRight(body, "\n")
		lines := strings.Split(body, "\n")
		avail := h - 2
		if avail > 0 && len(lines) > avail {
			lines = lines[len(lines)-avail:]
		}
		return banner + "\n\n" + strings.Join(lines, "\n")
	}
	out, ok := m.previewCache[a.ID]
	if !ok {
		out = "(loading preview...)"
	}
	out = strings.TrimRight(out, "\n")
	lines := strings.Split(out, "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	return strings.Join(lines, "\n")
}

const asciiMux = `
 ███╗   ███╗██╗   ██╗██╗  ██╗
 ████╗ ████║██║   ██║╚██╗██╔╝
 ██╔████╔██║██║   ██║ ╚███╔╝
 ██║╚██╔╝██║██║   ██║ ██╔██╗
 ██║ ╚═╝ ██║╚██████╔╝██╔╝ ██╗
 ╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═╝
`

func renderSplash(w, h int, subtitle string, keys []string) string {
	art := strings.TrimPrefix(asciiMux, "\n")
	artStyled := lipgloss.NewStyle().Foreground(colorAccent).Render(art)
	sub := lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render(subtitle)
	var keyLines []string
	for _, k := range keys {
		keyLines = append(keyLines, lipgloss.NewStyle().Foreground(colorDim).Render(k))
	}
	block := lipgloss.JoinVertical(lipgloss.Center,
		artStyled,
		"",
		sub,
		"",
		strings.Join(keyLines, "\n"),
	)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, block)
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
		row("[ / ] or h/l", "prev / next tab"),
		row("1 – 9", "jump to tab N"),
		sec("agents"),
		row("n", "spawn agent in current project"),
		row("enter", "attach to selected agent (C-b d to detach)"),
		row("d", "kill agent (again to forget; enter to resume)"),
		row("r", "rename selected agent"),
		row("j / k", "move agent cursor"),
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

func (m Model) renderStatus() string {
	left := fmt.Sprintf("[%s]", modeStr(m.mode))
	long := "o open · n spawn · ent attach · j/k agent · [/] tab · 1-9 jump · w close · d kill · ? help · q quit"
	short := "o open · n spawn · [/] tab · w close · ? help · q quit"
	tiny := "? help · q quit"
	right := long
	if lipgloss.Width(left)+lipgloss.Width(right)+3 > m.w {
		right = short
	}
	if lipgloss.Width(left)+lipgloss.Width(right)+3 > m.w {
		right = tiny
	}
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + right
	if m.statusMsg != "" {
		prefix := m.statusMsg + "  ·  "
		line = prefix + line
	}
	return clipLine(styleStatus.Render(line), m.w)
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

	help := styleStatus.Render("↓↑ select  enter open  esc cancel  (type to fuzzy filter)")
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
	var lines []string
	lines = append(lines, title, "")
	for i, n := range names {
		spec := session.Providers[n]
		label := n
		hint := ""
		if !spec.Available {
			hint = lipgloss.NewStyle().Foreground(colorErr).Render(" · not installed")
		}
		text := label + hint
		if i == m.providerCur {
			if spec.Available {
				lines = append(lines, styleListItemSel.Render("▸ "+label)+hint)
			} else {
				lines = append(lines, styleListItemSel.Render("▸ "+label)+hint)
			}
		} else {
			if spec.Available {
				lines = append(lines, "  "+text)
			} else {
				lines = append(lines, "  "+lipgloss.NewStyle().Foreground(colorDim).Render(label)+hint)
			}
		}
	}
	// install hint for current selection
	cur := session.Providers[names[m.providerCur]]
	extra := ""
	if !cur.Available && cur.Hint != "" {
		extra = "\n" + lipgloss.NewStyle().Foreground(colorWarn).Render("install: "+cur.Hint)
	}
	lines = append(lines, "", styleStatus.Render("↓↑ select  enter spawn  esc cancel")+extra)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(1, 2).
		Width(52).
		Render(strings.Join(lines, "\n"))
}

func pickSessionID(p *state.Project, provider string) string {
	used := map[string]struct{}{}
	for _, a := range p.Agents {
		used[a.ID] = struct{}{}
	}
	for n := 1; n < 10000; n++ {
		id := session.SessionID(p.Name, provider, n)
		if _, dup := used[id]; dup {
			continue
		}
		if session.Exists(id) {
			continue
		}
		return id
	}
	return session.SessionID(p.Name, provider, int(time.Now().Unix()))
}

func clipLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "")
}

func modeStr(m mode) string {
	switch m {
	case modeOpenProject:
		return "open"
	case modeSpawnAgent:
		return "spawn"
	case modeHelp:
		return "help"
	case modeRenameAgent:
		return "rename"
	}
	return "normal"
}

