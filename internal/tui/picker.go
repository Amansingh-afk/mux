package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sahilm/fuzzy"

	"github.com/ashmit/mux/internal/discover"
	"github.com/ashmit/mux/internal/state"
)

type pickerItem struct {
	name  string // display label
	path  string
	tag   string // "saved", "repo", "dir"
	score int
}

type repoPicker struct {
	query string
	all   []pickerItem
	view  []pickerItem // filtered/ranked
	cur   int
}

func newRepoPicker(saved []state.Project) repoPicker {
	items := buildItems(saved)
	p := repoPicker{all: items}
	p.refilter()
	return p
}

func buildItems(saved []state.Project) []pickerItem {
	seen := map[string]struct{}{}
	var items []pickerItem

	// saved projects first
	sortedSaved := append([]state.Project(nil), saved...)
	sort.Slice(sortedSaved, func(i, j int) bool {
		if sortedSaved[i].Pinned != sortedSaved[j].Pinned {
			return sortedSaved[i].Pinned
		}
		return sortedSaved[i].LastUse > sortedSaved[j].LastUse
	})
	for _, s := range sortedSaved {
		if _, dup := seen[s.Path]; dup {
			continue
		}
		seen[s.Path] = struct{}{}
		items = append(items, pickerItem{name: s.Name, path: s.Path, tag: "saved"})
	}

	// discovered repos
	for _, r := range discover.Repos(discover.DefaultRoots(), 4) {
		if _, dup := seen[r.Path]; dup {
			continue
		}
		seen[r.Path] = struct{}{}
		items = append(items, pickerItem{name: r.Name, path: r.Path, tag: "repo"})
	}
	return items
}

func (p *repoPicker) refilter() {
	if p.query == "" {
		p.view = p.all
		if p.cur >= len(p.view) {
			p.cur = 0
		}
		return
	}
	// fuzzy match against "name  path-rel" composite for better ranking
	src := make([]string, len(p.all))
	for i, it := range p.all {
		src[i] = it.name + " " + displayPath(it.path)
	}
	matches := fuzzy.Find(p.query, src)
	out := make([]pickerItem, 0, len(matches))
	for _, m := range matches {
		it := p.all[m.Index]
		it.score = m.Score
		out = append(out, it)
	}
	p.view = out
	if p.cur >= len(p.view) {
		p.cur = 0
	}
}

func (p *repoPicker) typed(r string) {
	p.query += r
	p.cur = 0
	p.refilter()
}

func (p *repoPicker) backspace() {
	if len(p.query) > 0 {
		p.query = p.query[:len(p.query)-1]
		p.cur = 0
		p.refilter()
	}
}

func (p *repoPicker) up() {
	if p.cur > 0 {
		p.cur--
	}
}

func (p *repoPicker) down() {
	if p.cur < len(p.view)-1 {
		p.cur++
	}
}

func (p *repoPicker) selected() *pickerItem {
	if p.cur < 0 || p.cur >= len(p.view) {
		return nil
	}
	return &p.view[p.cur]
}

// selectedOrLiteral returns the selected item, or if the query looks like a
// path, fabricate an item from the literal path.
func (p *repoPicker) selectedOrLiteral() *pickerItem {
	if it := p.selected(); it != nil {
		return it
	}
	q := strings.TrimSpace(p.query)
	if q == "" {
		return nil
	}
	if strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~") || strings.HasPrefix(q, ".") {
		abs := expandAbs(q)
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			return &pickerItem{name: filepath.Base(abs), path: abs, tag: "dir"}
		}
	}
	return nil
}

func expandAbs(p string) string {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		cwd, err := os.Getwd()
		if err == nil {
			p = filepath.Join(cwd, p)
		}
	}
	return p
}

// displayPath shortens home dir to ~ for display.
func displayPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
