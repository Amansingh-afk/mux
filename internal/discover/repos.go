package discover

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Repo struct {
	Name string
	Path string
	Kind string // "repo" (has .git) or "dir" (plain folder)
}

// Repos walks the given roots up to maxDepth deep and reports every visited
// directory. A dir with a .git entry is tagged Kind="repo" and descent stops
// there; all other dirs are tagged Kind="dir". Roots themselves are not
// reported. Hidden dirs (other than .git) and common heavy dirs are skipped.
func Repos(roots []string, maxDepth int) []Repo {
	seen := map[string]struct{}{}
	var out []Repo
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			continue
		}
		scan(abs, 0, maxDepth, seen, &out)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

var skipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
	"build":        true,
	".cache":       true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
}

func scan(dir string, depth, maxDepth int, seen map[string]struct{}, out *[]Repo) {
	if depth > maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	isRepo := false
	for _, e := range entries {
		if e.Name() == ".git" {
			isRepo = true
			break
		}
	}
	// emit this dir (skip the root itself at depth 0)
	if depth > 0 {
		if _, dup := seen[dir]; !dup {
			seen[dir] = struct{}{}
			kind := "dir"
			if isRepo {
				kind = "repo"
			}
			*out = append(*out, Repo{Name: filepath.Base(dir), Path: dir, Kind: kind})
		}
	}
	if isRepo {
		return // don't descend into repos
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if skipDirs[name] {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue
		}
		scan(filepath.Join(dir, name), depth+1, maxDepth, seen, out)
	}
}

// DefaultRoots returns likely repo parent dirs that exist on this machine.
func DefaultRoots() []string {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	cands := []string{cwd}
	for _, sub := range []string{"code", "repos", "projects", "src", "realm", "dev", "work"} {
		p := filepath.Join(home, sub)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			cands = append(cands, p)
		}
	}
	return cands
}
