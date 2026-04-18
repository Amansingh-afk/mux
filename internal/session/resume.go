package session

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// canonical uuid v4-ish match: 8-4-4-4-12 hex with dashes.
var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// ValidUUID returns true if s looks like a real provider session uuid.
// Used to reject stale malformed uuids from earlier buggy captures.
func ValidUUID(s string) bool {
	return uuidRe.MatchString(s)
}

// providerSessionDir maps provider name to a function that returns the
// directory where the provider stores conversation logs for a given project
// cwd. Empty string means the provider does not expose per-project logs we
// can scan (no uuid capture possible).
var providerSessionDir = map[string]func(cwd string) string{
	"claude": claudeSessionDir,
	"codex":  codexSessionDir,
	// gemini / cursor: tbd — add when log layout is known
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// claudeSessionDir: ~/.claude/projects/<slug>/ where slug is cwd with
// non-alnum chars replaced by "-" (claude-code convention).
func claudeSessionDir(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	slug := claudeSlug(abs)
	return filepath.Join(home(), ".claude", "projects", slug)
}

func claudeSlug(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// codexSessionDir: codex writes rollouts to ~/.codex/sessions/.
// Not cwd-scoped, so uuid capture requires mtime diff across the whole dir.
func codexSessionDir(cwd string) string {
	return filepath.Join(home(), ".codex", "sessions")
}

// SessionDir returns the provider's conversation dir for the given cwd, or ""
// if the provider is not supported for uuid capture.
func SessionDir(provider, cwd string) string {
	fn, ok := providerSessionDir[provider]
	if !ok {
		return ""
	}
	return fn(cwd)
}

// spawnLocks serializes concurrent Spawns that land in the same provider
// session dir, so mtime-based uuid capture stays reliable.
var (
	spawnLockMu sync.Mutex
	spawnLocks  = map[string]*sync.Mutex{}
)

func dirLock(key string) *sync.Mutex {
	spawnLockMu.Lock()
	defer spawnLockMu.Unlock()
	m, ok := spawnLocks[key]
	if !ok {
		m = &sync.Mutex{}
		spawnLocks[key] = m
	}
	return m
}

// Snapshot is a snapshot of jsonl filenames -> mtime in a provider's session
// dir. Take one before Spawn, hand it to CaptureUUID after.
type Snapshot map[string]time.Time

func snapshotJsonls(dir string) Snapshot {
	out := Snapshot{}
	if dir == "" {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out[name] = info.ModTime()
	}
	return out
}

// CaptureUUID blocks the caller's goroutine until it can determine the
// provider's native session uuid for the agent just spawned in `cwd`, or
// times out. `before` is the jsonl snapshot taken just before Spawn. Returns
// "" on failure. Safe to call from a goroutine.
func CaptureUUID(provider, cwd string, before Snapshot, timeout time.Duration) string {
	dir := SessionDir(provider, cwd)
	if dir == "" {
		return ""
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		after := snapshotJsonls(dir)
		// prefer entries not in `before` (new file)
		type cand struct {
			name string
			mod  time.Time
		}
		var news []cand
		for name, mod := range after {
			if _, existed := before[name]; !existed {
				news = append(news, cand{name, mod})
				continue
			}
			// also accept files that existed but got touched after spawn
			if !before[name].Equal(mod) && mod.After(before[name]) {
				news = append(news, cand{name, mod})
			}
		}
		if len(news) == 0 {
			continue
		}
		// newest mtime wins — this is spawn-order correlation
		sort.Slice(news, func(i, j int) bool { return news[i].mod.After(news[j].mod) })
		if u := uuidFromFilename(provider, news[0].name); u != "" {
			return u
		}
	}
	return ""
}

// uuidFromFilename extracts the provider's native session uuid from a jsonl
// filename. Returns "" if no valid uuid can be pulled.
//   claude: "<uuid>.jsonl"  (uuid includes dashes)
//   codex:  "rollout-<YYYY>-<MM>-<DD>T<HHMMSS>-<uuid>.jsonl"
// both match uuidRe; the regex handles either layout.
func uuidFromFilename(provider, name string) string {
	name = strings.TrimSuffix(name, ".jsonl")
	if m := uuidRe.FindString(name); m != "" {
		return m
	}
	return ""
}

// WithSpawnLock runs fn while holding the per-dir spawn lock. No-op when the
// provider has no session dir (lock not needed).
func WithSpawnLock(provider, cwd string, fn func()) {
	dir := SessionDir(provider, cwd)
	if dir == "" {
		fn()
		return
	}
	lock := dirLock(dir)
	lock.Lock()
	defer lock.Unlock()
	fn()
}

// SnapshotFor returns the current jsonl snapshot for the given provider+cwd.
// Callers pass this into CaptureUUID after spawn.
func SnapshotFor(provider, cwd string) Snapshot {
	return snapshotJsonls(SessionDir(provider, cwd))
}
