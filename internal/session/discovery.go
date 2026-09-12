package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionMeta describes one provider-native session log discovered on disk.
type SessionMeta struct {
	UUID    string
	ModTime time.Time
	Preview string // first user message, trimmed, may be ""
}

const (
	// previewMaxLine caps a single jsonl line read while scanning for a
	// preview; longer lines abort the scan for that file (preview stays "").
	previewMaxLine = 1 << 20 // ~1MB
	// previewMaxRunes caps the rendered preview length.
	previewMaxRunes = 80
	// claudeScanLines bounds how deep we look for the first user message in a
	// claude conversation log.
	claudeScanLines = 200
	// codexScanLines bounds the per-file read of a codex rollout.
	codexScanLines = 50
	// codexHeaderLines bounds how many leading lines we check for the
	// session-meta cwd header in a codex rollout.
	codexHeaderLines = 5
	// codexWalkDepth bounds directory nesting under ~/.codex/sessions
	// (rollouts live in year/month/day subdirs).
	codexWalkDepth = 4
)

// BackfillUUID late-attributes a session uuid to an agent whose spawn-time
// capture missed (e.g. codex nesting bug, or mux restarted mid-capture):
// newest provider session for the cwd whose mtime is at/after the agent's
// spawn time and not already claimed by another agent. Sessions are
// newest-first, so the first too-old entry ends the search.
func BackfillUUID(provider, dir string, spawnedAt int64, claimed map[string]bool) string {
	metas, err := ListSessions(provider, dir)
	if err != nil {
		return ""
	}
	for _, sm := range metas {
		if sm.ModTime.Unix() < spawnedAt-5 {
			return ""
		}
		if claimed[sm.UUID] {
			continue
		}
		return sm.UUID
	}
	return ""
}

// ListSessions returns provider-native sessions recorded for project dir,
// newest first. Read-only; never writes provider files. Unknown providers and
// missing session dirs yield (nil, nil), not an error.
func ListSessions(provider, dir string) ([]SessionMeta, error) {
	switch provider {
	case "claude":
		return listClaudeSessions(dir)
	case "codex":
		return listCodexSessions(dir)
	default:
		return nil, nil
	}
}

// listClaudeSessions scans ~/.claude/projects/<slug>/*.jsonl for the project.
func listClaudeSessions(dir string) ([]SessionMeta, error) {
	root := SessionDir("claude", dir)
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SessionMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		uuid := uuidFromFilename("claude", e.Name())
		if uuid == "" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		preview := ""
		if f, ferr := os.Open(filepath.Join(root, e.Name())); ferr == nil {
			preview = parseClaudePreview(f)
			f.Close()
		}
		out = append(out, SessionMeta{UUID: uuid, ModTime: info.ModTime(), Preview: preview})
	}
	sortSessions(out)
	return out, nil
}

// listCodexSessions walks ~/.codex/sessions (bounded depth) for
// rollout-*-<uuid>.jsonl files whose session-meta header records this
// project's cwd.
func listCodexSessions(dir string) ([]SessionMeta, error) {
	root := SessionDir("codex", dir)
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); err != nil {
		return nil, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	var out []SessionMeta
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
		}
		if d.IsDir() {
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return fs.SkipDir
			}
			if rel != "." && strings.Count(rel, string(filepath.Separator))+1 > codexWalkDepth {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		uuid := uuidFromFilename("codex", name)
		if uuid == "" {
			return nil
		}
		f, ferr := os.Open(path)
		if ferr != nil {
			return nil
		}
		defer f.Close()
		if !rolloutMatchesCwd(f, abs) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		preview := ""
		if _, serr := f.Seek(0, io.SeekStart); serr == nil {
			preview = parseCodexPreview(f)
		}
		out = append(out, SessionMeta{UUID: uuid, ModTime: info.ModTime(), Preview: preview})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sortSessions(out)
	return out, nil
}

// parseClaudePreview scans a claude conversation jsonl for the first real
// user message and returns it trimmed for display. Claude user lines look
// like:
//
//	{"type":"user","message":{"role":"user","content":"..."}}
//	{"type":"user","message":{"role":"user","content":[{"type":"text","text":"..."}]}}
//
// tool_result-only user lines and meta/command lines are skipped.
func parseClaudePreview(r io.Reader) string {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), previewMaxLine)
	for n := 0; n < claudeScanLines && sc.Scan(); n++ {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"type":"user"`)) {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			IsMeta  bool   `json:"isMeta"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &rec); err != nil || rec.Type != "user" || rec.IsMeta {
			continue
		}
		text := contentText(rec.Message.Content)
		if text == "" || isSyntheticUserText(text) {
			continue
		}
		return trimPreview(text)
	}
	return ""
}

// contentText extracts display text from a message content field that is
// either a plain JSON string or an array of blocks with "text"-bearing
// entries ({"type":"text","text":...} / {"type":"input_text","text":...}).
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	for _, b := range blocks {
		if (b.Type == "text" || b.Type == "input_text") && strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return ""
}

// isSyntheticUserText reports whether a user-role message is harness-injected
// context rather than something the human typed.
func isSyntheticUserText(t string) bool {
	t = strings.TrimSpace(t)
	for _, p := range []string{
		"<command-",       // claude slash-command records
		"<local-command-", // claude local command output
		"<environment_context>",
		"<user_instructions>",
		"<ENVIRONMENT_CONTEXT>",
	} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// rolloutMatchesCwd reports whether a codex rollout's leading session-meta
// header records exactly the given project dir as its cwd. Exact string match
// on the JSON-encoded path — no prefix matching.
func rolloutMatchesCwd(r io.Reader, dir string) bool {
	quoted, err := json.Marshal(dir)
	if err != nil {
		return false
	}
	needle := append([]byte(`"cwd":`), quoted...)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), previewMaxLine)
	for n := 0; n < codexHeaderLines && sc.Scan(); n++ {
		if bytes.Contains(sc.Bytes(), needle) {
			return true
		}
	}
	return false
}

// parseCodexPreview best-effort extracts the first human message from a codex
// rollout. Formats vary across codex versions; handled shapes:
//
//	{"type":"event_msg","payload":{"type":"user_message","message":"..."}}
//	{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"..."}]}}
//
// Synthetic env/instruction wrappers are skipped. Returns "" when nothing
// cheap is found within codexScanLines lines.
func parseCodexPreview(r io.Reader) string {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), previewMaxLine)
	for n := 0; n < codexScanLines && sc.Scan(); n++ {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"user_message"`)) && !bytes.Contains(line, []byte(`"role":"user"`)) {
			continue
		}
		var rec struct {
			Payload struct {
				Type    string          `json:"type"`
				Message string          `json:"message"`
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		var text string
		switch {
		case rec.Payload.Type == "user_message" && rec.Payload.Message != "":
			text = rec.Payload.Message
		case rec.Payload.Role == "user":
			text = contentText(rec.Payload.Content)
		}
		if text == "" || isSyntheticUserText(text) {
			continue
		}
		return trimPreview(text)
	}
	return ""
}

// trimPreview collapses whitespace and caps the preview length.
func trimPreview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > previewMaxRunes {
		s = string(runes[:previewMaxRunes])
	}
	return s
}

// sortSessions orders sessions newest ModTime first, uuid as a deterministic
// tie-break.
func sortSessions(s []SessionMeta) {
	sort.SliceStable(s, func(i, j int) bool {
		if !s[i].ModTime.Equal(s[j].ModTime) {
			return s[i].ModTime.After(s[j].ModTime)
		}
		return s[i].UUID < s[j].UUID
	})
}
