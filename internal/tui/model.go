package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amansingh-afk/mux/internal/session"
	"github.com/Amansingh-afk/mux/internal/state"
)

type mode int

const (
	modeNormal mode = iota
	modeOpenProject
	modeSpawnAgent
	modeRenameAgent
	modeHelp
	modeAdopt
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
	adoptItems   []adoptItem
	adoptCur     int
	adoptLoading bool
	aliveCache   map[string]bool
	statusRec    map[string]agentStatusRec
	spinnerFrame int
	// booted flips on the first WindowSizeMsg — the earliest point with real
	// dimensions, where the right pane for the restored agent is first shown.
	booted bool
	// loading is true during the boot splash: mux still owns the full window
	// (no split yet) and renders a centered wordmark until bootRevealMsg.
	loading bool

	// rightPane is the outer-tmux pane id mux uses to display the active
	// agent next to itself. "" before first split. Lifecycle: created on
	// first agent select via ensureAgentVisible, respawned on subsequent
	// switches, killed on quit or when no agent remains.
	rightPane      string
	rightPaneAgent string
	// rightClient is the tmux client name (tty) of the nested `tmux attach`
	// running in the right pane. Used by SwitchClient so swapping agents
	// doesn't kill+re-attach (no flicker).
	rightClient string
}

type agentStatusRec struct {
	status      session.Status
	hash        uint64
	stableTicks int
	lastNotify  time.Time
}

type tickMsg time.Time
type spinTickMsg time.Time

// bootRevealMsg ends the boot splash: the right pane is split in and the
// normal sidebar view takes over.
type bootRevealMsg struct{}
type captureRec struct {
	id  string
	out string
	// native is the provider-native status signal probed alongside the pane
	// capture; nil when no signal exists for this agent this tick.
	native *session.NativeSignal
}
type statusBatchMsg []captureRec
type aliveMsg map[string]bool
type uuidCapturedMsg struct {
	projectPath string
	agentID     string
	uuid        string
}

// rightClientMsg carries the tmux client name found attached to the right
// pane's session, captured asynchronously a short time after SplitRight so
// the nested attach has time to register.
type rightClientMsg struct {
	pane    string
	session string
	client  string
}

func New(s *state.Store, alive map[string]bool) Model {
	if alive == nil {
		alive = map[string]bool{}
	}
	m := Model{
		store:      s,
		aliveCache: alive,
		statusRec:  map[string]agentStatusRec{},
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
	m.pushTabBar()
	return tea.Batch(tick(), spinTick(), refreshAlive(), m.captureAllForStatus())
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

// bodyDims returns the content area of the body pane (cols, rows).
func (m Model) bodyDims() (int, int) {
	sidebarW := 21
	if m.w < 80 {
		sidebarW = 18
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

func (m Model) agentByID(id string) (*state.Agent, *state.Project) {
	snap := m.store.Snapshot()
	for i := range snap.Projects {
		for j := range snap.Projects[i].Agents {
			if snap.Projects[i].Agents[j].ID == id {
				return &snap.Projects[i].Agents[j], &snap.Projects[i]
			}
		}
	}
	return nil, nil
}
