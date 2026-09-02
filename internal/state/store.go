package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func timeNow() int64 { return time.Now().Unix() }

type Agent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	SpawnedAt   int64  `json:"spawned_at"`
	Dir         string `json:"dir,omitempty"`
	SessionUUID string `json:"session_uuid,omitempty"`
	LastSeen    int64  `json:"last_seen,omitempty"`
	Dead        bool   `json:"dead,omitempty"`
}

// RenameAgent sets Name for the agent with the given ID in the given project.
func (s *Store) RenameAgent(projectPath, agentID, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Projects {
		if s.data.Projects[i].Path != projectPath {
			continue
		}
		for j := range s.data.Projects[i].Agents {
			if s.data.Projects[i].Agents[j].ID == agentID {
				s.data.Projects[i].Agents[j].Name = name
				return
			}
		}
	}
}

type Project struct {
	Name        string  `json:"name"`
	Path        string  `json:"path"`
	Agents      []Agent `json:"agents"`
	Pinned      bool    `json:"pinned"`
	LastUse     int64   `json:"last_use"`
	LastAgentID string  `json:"last_agent_id,omitempty"`
}

type State struct {
	Projects  []Project `json:"projects"`
	OpenTabs  []string  `json:"open_tabs"` // project paths in tab order
	ActiveTab string    `json:"active_tab"`
}

type Store struct {
	path string
	mu   sync.Mutex // guards data
	ioMu sync.Mutex // serializes writes to disk so concurrent Saves can't race on rename
	data State
}

func Open() (*Store, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "state.json")
	s := &Store{path: p}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func configDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "mux"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "mux"), nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		s.data = State{}
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &s.data)
}

func (s *Store) Save() error {
	// Marshal under state lock so we get a consistent snapshot, then release
	// before fsync so concurrent mutations aren't blocked on disk IO.
	s.mu.Lock()
	b, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	// Serialize disk writes so two concurrent Saves can't interleave rename.
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		// best-effort: don't leave a stale .tmp around if rename failed (disk
		// full, perms, etc). ignore remove error — the write error is the
		// one worth returning.
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Snapshot returns a deep-copied view of State safe to read outside the lock.
// Callers who iterate Projects/Agents while the store mutates would otherwise
// race on the shared backing array.
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := State{
		OpenTabs:  append([]string(nil), s.data.OpenTabs...),
		ActiveTab: s.data.ActiveTab,
		Projects:  make([]Project, len(s.data.Projects)),
	}
	for i, p := range s.data.Projects {
		cp := p
		cp.Agents = append([]Agent(nil), p.Agents...)
		out.Projects[i] = cp
	}
	return out
}

// UpsertProject inserts or updates a Project by Path. Preserves existing
// Agents when the incoming Project has no Agents of its own — the common
// open-existing-repo path sends only metadata and must not wipe the sidebar.
func (s *Store) UpsertProject(p Project) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Projects {
		if s.data.Projects[i].Path != p.Path {
			continue
		}
		existing := s.data.Projects[i]
		existing.Name = p.Name
		if p.LastUse != 0 {
			existing.LastUse = p.LastUse
		}
		existing.Pinned = p.Pinned
		if len(p.Agents) > 0 {
			existing.Agents = p.Agents
		}
		s.data.Projects[i] = existing
		return
	}
	s.data.Projects = append(s.data.Projects, p)
}

func (s *Store) RemoveProject(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.data.Projects[:0]
	for _, p := range s.data.Projects {
		if p.Path != path {
			out = append(out, p)
		}
	}
	s.data.Projects = out
	tabs := s.data.OpenTabs[:0]
	for _, t := range s.data.OpenTabs {
		if t != path {
			tabs = append(tabs, t)
		}
	}
	s.data.OpenTabs = tabs
	if s.data.ActiveTab == path {
		if len(tabs) > 0 {
			s.data.ActiveTab = tabs[len(tabs)-1]
		} else {
			s.data.ActiveTab = ""
		}
	}
}

func (s *Store) OpenTab(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.data.OpenTabs {
		if t == path {
			s.data.ActiveTab = path
			return
		}
	}
	s.data.OpenTabs = append(s.data.OpenTabs, path)
	s.data.ActiveTab = path
}

func (s *Store) CloseTab(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.data.OpenTabs[:0]
	for _, t := range s.data.OpenTabs {
		if t != path {
			out = append(out, t)
		}
	}
	s.data.OpenTabs = out
	if s.data.ActiveTab == path && len(out) > 0 {
		s.data.ActiveTab = out[len(out)-1]
	} else if len(out) == 0 {
		s.data.ActiveTab = ""
	}
}

func (s *Store) SetActive(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.ActiveTab = path
}

func (s *Store) AddAgent(projectPath string, a Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Projects {
		if s.data.Projects[i].Path == projectPath {
			s.data.Projects[i].Agents = append(s.data.Projects[i].Agents, a)
			return
		}
	}
}

func (s *Store) RemoveAgent(projectPath, agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Projects {
		if s.data.Projects[i].Path != projectPath {
			continue
		}
		out := s.data.Projects[i].Agents[:0]
		for _, a := range s.data.Projects[i].Agents {
			if a.ID != agentID {
				out = append(out, a)
			}
		}
		s.data.Projects[i].Agents = out
		return
	}
}

// Reconcile marks agents dead when they are missing from the live set, and
// clears Dead + updates LastSeen when present. Returns number of Dead-flag
// flips (in either direction) so callers can Save only when state actually
// changed. LastSeen updates alone do not trigger a flip.
func (s *Store) Reconcile(live map[string]bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := timeNow()
	n := 0
	for i := range s.data.Projects {
		for j := range s.data.Projects[i].Agents {
			a := &s.data.Projects[i].Agents[j]
			if live[a.ID] {
				if a.Dead {
					a.Dead = false
					n++
				}
				a.LastSeen = now
			} else if !a.Dead {
				a.Dead = true
				n++
			}
		}
	}
	return n
}

// MarkAgentDead flips an agent's Dead flag without removing it. Used by
// soft-delete so the user can resume from the same row.
func (s *Store) MarkAgentDead(projectPath, agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Projects {
		if s.data.Projects[i].Path != projectPath {
			continue
		}
		for j := range s.data.Projects[i].Agents {
			if s.data.Projects[i].Agents[j].ID == agentID {
				s.data.Projects[i].Agents[j].Dead = true
				return
			}
		}
	}
}

// SetLastAgent records which agent was most recently focused in the given
// project so tab switches can restore the prior selection instead of jumping
// to index 0.
func (s *Store) SetLastAgent(projectPath, agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Projects {
		if s.data.Projects[i].Path == projectPath {
			s.data.Projects[i].LastAgentID = agentID
			return
		}
	}
}

// SetAgentUUID records the provider's native session UUID for a given agent.
func (s *Store) SetAgentUUID(projectPath, agentID, uuid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Projects {
		if s.data.Projects[i].Path != projectPath {
			continue
		}
		for j := range s.data.Projects[i].Agents {
			if s.data.Projects[i].Agents[j].ID == agentID {
				s.data.Projects[i].Agents[j].SessionUUID = uuid
				return
			}
		}
	}
}
