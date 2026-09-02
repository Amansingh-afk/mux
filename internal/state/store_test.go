package state

import (
	"testing"
)

// openTestStore points XDG_CONFIG_HOME at a temp dir and opens a fresh store.
// Returns the store and the config root so a second Open can hit the same
// state file.
func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	s, err := Open()
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	return s, root
}

func TestOpenEmptyState(t *testing.T) {
	s, _ := openTestStore(t)
	snap := s.Snapshot()
	if len(snap.Projects) != 0 || len(snap.OpenTabs) != 0 || snap.ActiveTab != "" {
		t.Errorf("fresh store not empty: %+v", snap)
	}
}

func TestReconcile(t *testing.T) {
	s, _ := openTestStore(t)
	s.UpsertProject(Project{
		Name: "p1",
		Path: "/p1",
		Agents: []Agent{
			{ID: "a-live-alive", Dead: false}, // live, already alive: no flip
			{ID: "a-live-dead", Dead: true},   // live, was dead: flip true->false
			{ID: "a-gone-alive", Dead: false}, // absent, was alive: flip false->true
			{ID: "a-gone-dead", Dead: true},   // absent, already dead: no flip
		},
	})

	live := map[string]bool{"a-live-alive": true, "a-live-dead": true}
	n := s.Reconcile(live)
	if n != 2 {
		t.Errorf("Reconcile returned %d flips, want 2", n)
	}

	snap := s.Snapshot()
	agents := map[string]Agent{}
	for _, a := range snap.Projects[0].Agents {
		agents[a.ID] = a
	}
	if agents["a-live-alive"].Dead {
		t.Error("a-live-alive should stay alive")
	}
	if agents["a-live-dead"].Dead {
		t.Error("a-live-dead should have been revived")
	}
	if !agents["a-gone-alive"].Dead {
		t.Error("a-gone-alive should have been marked dead")
	}
	if !agents["a-gone-dead"].Dead {
		t.Error("a-gone-dead should stay dead")
	}
	// live agents get LastSeen stamped; absent ones do not
	if agents["a-live-alive"].LastSeen == 0 || agents["a-live-dead"].LastSeen == 0 {
		t.Error("live agents should have LastSeen updated")
	}
	if agents["a-gone-alive"].LastSeen != 0 {
		t.Error("absent agent should not get LastSeen updated")
	}

	// steady state: same live set again -> zero flips
	if n := s.Reconcile(live); n != 0 {
		t.Errorf("second Reconcile returned %d flips, want 0", n)
	}
}

func TestUpsertProjectPreservesAgents(t *testing.T) {
	s, _ := openTestStore(t)
	s.UpsertProject(Project{
		Name:    "proj",
		Path:    "/proj",
		LastUse: 100,
		Agents:  []Agent{{ID: "a1", Name: "agent one"}},
	})

	// metadata-only re-upsert (no agents) must not wipe the agent list
	s.UpsertProject(Project{Name: "proj-renamed", Path: "/proj", Pinned: true})

	snap := s.Snapshot()
	if len(snap.Projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(snap.Projects))
	}
	p := snap.Projects[0]
	if p.Name != "proj-renamed" {
		t.Errorf("Name = %q, want proj-renamed", p.Name)
	}
	if !p.Pinned {
		t.Error("Pinned should be overwritten to true")
	}
	if p.LastUse != 100 {
		t.Errorf("LastUse = %d, want 100 (zero incoming LastUse preserves existing)", p.LastUse)
	}
	if len(p.Agents) != 1 || p.Agents[0].ID != "a1" {
		t.Errorf("agents wiped on re-upsert: %+v", p.Agents)
	}

	// re-upsert WITH agents replaces the list
	s.UpsertProject(Project{Name: "proj", Path: "/proj", Agents: []Agent{{ID: "a2"}}})
	snap = s.Snapshot()
	if len(snap.Projects[0].Agents) != 1 || snap.Projects[0].Agents[0].ID != "a2" {
		t.Errorf("agents not replaced when incoming has agents: %+v", snap.Projects[0].Agents)
	}

	// new path appends
	s.UpsertProject(Project{Name: "other", Path: "/other"})
	if snap = s.Snapshot(); len(snap.Projects) != 2 {
		t.Errorf("got %d projects after inserting new path, want 2", len(snap.Projects))
	}
}

func TestTabSemantics(t *testing.T) {
	s, _ := openTestStore(t)

	s.OpenTab("/a")
	s.OpenTab("/b")
	s.OpenTab("/c")
	snap := s.Snapshot()
	if len(snap.OpenTabs) != 3 || snap.ActiveTab != "/c" {
		t.Fatalf("after opens: tabs=%v active=%q", snap.OpenTabs, snap.ActiveTab)
	}

	// re-opening an existing tab activates it without duplicating
	s.OpenTab("/a")
	snap = s.Snapshot()
	if len(snap.OpenTabs) != 3 {
		t.Errorf("re-open duplicated tab: %v", snap.OpenTabs)
	}
	if snap.ActiveTab != "/a" {
		t.Errorf("re-open did not activate: active=%q", snap.ActiveTab)
	}

	// closing a non-active tab keeps the active tab
	s.CloseTab("/b")
	snap = s.Snapshot()
	if len(snap.OpenTabs) != 2 || snap.ActiveTab != "/a" {
		t.Errorf("after closing non-active: tabs=%v active=%q", snap.OpenTabs, snap.ActiveTab)
	}

	// closing the active tab activates the last remaining tab
	s.CloseTab("/a")
	snap = s.Snapshot()
	if snap.ActiveTab != "/c" {
		t.Errorf("after closing active: active=%q, want /c", snap.ActiveTab)
	}

	// closing the final tab clears ActiveTab
	s.CloseTab("/c")
	snap = s.Snapshot()
	if len(snap.OpenTabs) != 0 || snap.ActiveTab != "" {
		t.Errorf("after closing last: tabs=%v active=%q", snap.OpenTabs, snap.ActiveTab)
	}

	// SetActive is unconditional (does not require the tab to be open)
	s.SetActive("/ghost")
	if snap = s.Snapshot(); snap.ActiveTab != "/ghost" {
		t.Errorf("SetActive: active=%q, want /ghost", snap.ActiveTab)
	}
}

func TestRemoveProjectAlsoClosesTab(t *testing.T) {
	s, _ := openTestStore(t)
	s.UpsertProject(Project{Name: "p", Path: "/p"})
	s.OpenTab("/p")
	s.RemoveProject("/p")
	snap := s.Snapshot()
	if len(snap.Projects) != 0 {
		t.Errorf("project not removed: %+v", snap.Projects)
	}
	if len(snap.OpenTabs) != 0 {
		t.Errorf("tab not removed with project: %v", snap.OpenTabs)
	}
	if snap.ActiveTab != "" {
		t.Errorf("ActiveTab = %q; want cleared after removing its project", snap.ActiveTab)
	}
}

func TestSaveReloadRoundtrip(t *testing.T) {
	s, root := openTestStore(t)
	s.UpsertProject(Project{
		Name:    "proj",
		Path:    "/proj",
		Pinned:  true,
		LastUse: 42,
		Agents: []Agent{
			{
				ID:          "a1",
				Name:        "worker",
				Provider:    "claude",
				SpawnedAt:   1234,
				Dir:         "/proj/subdir",
				SessionUUID: "550e8400-e29b-41d4-a716-446655440000",
				Dead:        true,
				LastSeen:    5678,
			},
		},
	})
	s.SetLastAgent("/proj", "a1")
	s.OpenTab("/proj")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// reopen against the same XDG root
	t.Setenv("XDG_CONFIG_HOME", root)
	s2, err := Open()
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	snap := s2.Snapshot()
	if len(snap.Projects) != 1 {
		t.Fatalf("reloaded %d projects, want 1", len(snap.Projects))
	}
	p := snap.Projects[0]
	if p.Name != "proj" || p.Path != "/proj" || !p.Pinned || p.LastUse != 42 {
		t.Errorf("project metadata lost: %+v", p)
	}
	if p.LastAgentID != "a1" {
		t.Errorf("LastAgentID = %q, want a1", p.LastAgentID)
	}
	if len(p.Agents) != 1 {
		t.Fatalf("reloaded %d agents, want 1", len(p.Agents))
	}
	a := p.Agents[0]
	if a.ID != "a1" || a.Name != "worker" || a.Provider != "claude" || a.SpawnedAt != 1234 {
		t.Errorf("agent basics lost: %+v", a)
	}
	if a.Dir != "/proj/subdir" {
		t.Errorf("Dir = %q, want /proj/subdir", a.Dir)
	}
	if a.SessionUUID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("SessionUUID lost: %q", a.SessionUUID)
	}
	if !a.Dead {
		t.Error("Dead flag lost on roundtrip")
	}
	if a.LastSeen != 5678 {
		t.Errorf("LastSeen = %d, want 5678", a.LastSeen)
	}
	if len(snap.OpenTabs) != 1 || snap.OpenTabs[0] != "/proj" || snap.ActiveTab != "/proj" {
		t.Errorf("tab state lost: tabs=%v active=%q", snap.OpenTabs, snap.ActiveTab)
	}
}

func TestAgentMutators(t *testing.T) {
	s, _ := openTestStore(t)
	s.UpsertProject(Project{Name: "p", Path: "/p"})

	s.AddAgent("/p", Agent{ID: "a1", Name: "one"})
	s.AddAgent("/p", Agent{ID: "a2", Name: "two"})
	s.AddAgent("/nonexistent", Agent{ID: "ghost"}) // silently ignored

	s.RenameAgent("/p", "a1", "renamed")
	s.SetAgentUUID("/p", "a2", "550e8400-e29b-41d4-a716-446655440000")
	s.MarkAgentDead("/p", "a1")

	snap := s.Snapshot()
	p := snap.Projects[0]
	if len(p.Agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(p.Agents))
	}
	if p.Agents[0].Name != "renamed" || !p.Agents[0].Dead {
		t.Errorf("rename/markdead failed: %+v", p.Agents[0])
	}
	if p.Agents[1].SessionUUID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("SetAgentUUID failed: %+v", p.Agents[1])
	}

	s.RemoveAgent("/p", "a1")
	snap = s.Snapshot()
	if len(snap.Projects[0].Agents) != 1 || snap.Projects[0].Agents[0].ID != "a2" {
		t.Errorf("RemoveAgent failed: %+v", snap.Projects[0].Agents)
	}
}
