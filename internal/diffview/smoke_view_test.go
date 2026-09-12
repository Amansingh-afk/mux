package diffview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestViewSmoke renders the model at a fixed size and checks layout invariants:
// exact line count, no line wider than the terminal, header/footer present.
func TestViewSmoke(t *testing.T) {
	files := parseUnified(multiFileDiff, SectionCommitted)
	files = append(files, parseUnified(newFileDiff, SectionUntracked)...)
	d := &data{branch: "mux/saffron", files: files, ahead: 1,
		merge: MergeInfo{Supported: true, Conflicts: []string{"cmd/root.go"}}}
	m := newModel(d)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := mm.(*model).View()
	lines := strings.Split(view, "\n")
	if len(lines) != 24 {
		t.Errorf("view has %d lines, want 24", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 80 {
			t.Errorf("line %d width %d > 80: %q", i, w, l)
		}
	}
	if !strings.Contains(view, "mux/saffron") || !strings.Contains(view, "will conflict") {
		t.Errorf("header missing pieces")
	}
	if !strings.Contains(view, "q close") {
		t.Errorf("footer hint missing")
	}

	// empty state
	e := newModel(&data{branch: "mux/x", merge: MergeInfo{Supported: true, Clean: true}})
	em, _ := e.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	ev := em.(*model).View()
	if !strings.Contains(ev, "no changes yet") {
		t.Errorf("empty state missing message")
	}

	// notice page
	n := &noticeModel{title: "merge failed", body: strings.Repeat("word ", 40)}
	nm, _ := n.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	nv := nm.(*noticeModel).View()
	if !strings.Contains(nv, "merge failed") || !strings.Contains(nv, "q close") {
		t.Errorf("notice missing pieces")
	}
	for i, l := range strings.Split(nv, "\n") {
		if w := ansi.StringWidth(l); w > 100 {
			t.Errorf("notice line %d width %d > 100", i, w)
		}
	}
}
