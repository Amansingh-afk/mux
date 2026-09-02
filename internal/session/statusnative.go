// Native status probes: provider-emitted signals that augment (and outrank)
// the pane-capture heuristics in status.go.
//
// Two independent sources, merged by NativeProbe:
//
//  1. Spool events. Handler modes of the mux binary itself (`mux
//     --claude-hook`, `mux --notify-handler`) are invoked BY the provider
//     (claude settings hooks / codex notify) and write a single-line JSON
//     status file per agent under $XDG_STATE_HOME/mux/events/. Overwrite
//     semantics — the file always holds only the latest event.
//  2. Transcript mtime. A provider actively streaming a turn rewrites its
//     session jsonl continuously; an mtime younger than a few seconds is a
//     reliable "active right now" signal even when no hook fired.
//
// Everything here is poll-integrated: no sockets, no fsnotify, no new deps.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// NativeSignal is one provider-native status observation.
type NativeSignal struct {
	Status Status
	At     time.Time
}

// AgentRef carries the identity NativeProbe needs to locate an agent's spool
// file and transcript.
type AgentRef struct {
	ID          string // mux agent id (== tmux session name)
	Provider    string
	Dir         string // project cwd the agent was spawned in
	SessionUUID string // provider-native session uuid, "" if not yet captured
}

// nativeStatusEnabled is the master gate (config: [general] native_status).
// Guarded by a mutex only for -race hygiene; it is set once at startup.
var (
	nativeStatusMu      sync.RWMutex
	nativeStatusEnabled = true
)

// SetNativeStatus flips the native-status master switch. Called once at
// startup with the loaded config value.
func SetNativeStatus(on bool) {
	nativeStatusMu.Lock()
	nativeStatusEnabled = on
	nativeStatusMu.Unlock()
}

// NativeStatusEnabled reports the master switch state.
func NativeStatusEnabled() bool {
	nativeStatusMu.RLock()
	defer nativeStatusMu.RUnlock()
	return nativeStatusEnabled
}

// nativeProviders lists providers native probing understands. Var (not const
// map literal in a func) so tests can register synthetic providers.
var nativeProviders = map[string]bool{
	"claude": true,
	"codex":  true,
}

// eventsDir returns $XDG_STATE_HOME/mux/events (fallback
// ~/.local/state/mux/events). Computed per call so tests can flip
// XDG_STATE_HOME with t.Setenv.
func eventsDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(home(), ".local", "state")
	}
	return filepath.Join(base, "mux", "events")
}

// EventFilePath returns the spool file for an agent's latest status event.
// The id is sanitized defensively — tmux session names mux generates are
// already [A-Za-z0-9_]-only, but the id arrives via env in handler modes.
func EventFilePath(agentID string) string {
	return filepath.Join(eventsDir(), sanitize(agentID)+".json")
}

// SidFilePath returns the spool file holding an agent's provider session
// uuid, captured deterministically by the claude SessionStart hook.
func SidFilePath(agentID string) string {
	return filepath.Join(eventsDir(), sanitize(agentID)+".sid")
}

// spoolEvent is the on-disk JSON shape: {"status":"...","ts":<unix>}.
type spoolEvent struct {
	Status string `json:"status"`
	TS     int64  `json:"ts"`
}

// WriteSpoolEvent records the agent's latest native status. Overwrite
// semantics — never append. Atomic (temp + rename) so a concurrent reader
// never sees a torn line.
func WriteSpoolEvent(agentID string, st Status) error {
	data, err := json.Marshal(spoolEvent{Status: string(st), TS: time.Now().Unix()})
	if err != nil {
		return err
	}
	return atomicWrite(EventFilePath(agentID), append(data, '\n'))
}

// WriteSpoolSessionID records the provider-native session uuid for an agent.
// Overwrite semantics.
func WriteSpoolSessionID(agentID, sid string) error {
	return atomicWrite(SidFilePath(agentID), []byte(sid+"\n"))
}

// SpoolSessionID returns the spooled session uuid for an agent, or "" if
// absent/invalid.
func SpoolSessionID(agentID string) string {
	data, err := os.ReadFile(SidFilePath(agentID))
	if err != nil {
		return ""
	}
	sid := strings.TrimSpace(string(data))
	if !ValidUUID(sid) {
		return ""
	}
	return sid
}

// ClearSpool removes an agent's spool files. Called before a fresh spawn so
// a stale event/sid from a previous incarnation of the same id can't leak
// into the new session.
func ClearSpool(agentID string) {
	_ = os.Remove(EventFilePath(agentID))
	_ = os.Remove(SidFilePath(agentID))
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// readSpool loads the latest spooled status event for an agent.
func readSpool(agentID string) (NativeSignal, bool) {
	data, err := os.ReadFile(EventFilePath(agentID))
	if err != nil {
		return NativeSignal{}, false
	}
	var ev spoolEvent
	if json.Unmarshal(data, &ev) != nil {
		return NativeSignal{}, false
	}
	switch Status(ev.Status) {
	case StatusActive, StatusWaiting, StatusIdle:
	default:
		return NativeSignal{}, false
	}
	if ev.TS <= 0 {
		return NativeSignal{}, false
	}
	return NativeSignal{Status: Status(ev.Status), At: time.Unix(ev.TS, 0)}, true
}

// transcriptFreshWindow: a session jsonl touched within this window means the
// provider is writing a turn right now.
const transcriptFreshWindow = 3 * time.Second

// transcriptSignal checks the agent's session jsonl mtime; a fresh mtime is
// an active-now signal (At = mtime).
func transcriptSignal(a AgentRef) (NativeSignal, bool) {
	if a.SessionUUID == "" || !ValidUUID(a.SessionUUID) {
		return NativeSignal{}, false
	}
	dir := SessionDir(a.Provider, a.Dir)
	if dir == "" {
		return NativeSignal{}, false
	}
	path := transcriptPath(a.Provider, dir, a.SessionUUID)
	if path == "" {
		return NativeSignal{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return NativeSignal{}, false
	}
	mt := info.ModTime()
	if time.Since(mt) >= transcriptFreshWindow {
		return NativeSignal{}, false
	}
	return NativeSignal{Status: StatusActive, At: mt}, true
}

// transcriptPath locates the session jsonl inside dir for the given uuid.
// claude names files "<uuid>.jsonl" (direct join, no ReadDir). Other layouts
// (codex "rollout-<ts>-<uuid>.jsonl") embed the uuid in the name, so scan.
func transcriptPath(provider, dir, uuid string) string {
	if provider == "claude" {
		return filepath.Join(dir, uuid+".jsonl")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".jsonl") && strings.Contains(name, uuid) {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// NativeProbe returns the best provider-native status signal for an agent,
// or false when none exists (unsupported provider, gate off, no spool, no
// fresh transcript). Merge rule: the spool event wins unless the transcript
// mtime is fresher — the transcript signal always means activity, so a
// newer mtime overrides an older waiting/idle event.
func NativeProbe(a AgentRef) (NativeSignal, bool) {
	if !NativeStatusEnabled() || !nativeProviders[a.Provider] {
		return NativeSignal{}, false
	}
	spool, hasSpool := readSpool(a.ID)
	mt, hasMt := transcriptSignal(a)
	switch {
	case hasSpool && hasMt:
		if mt.At.After(spool.At) {
			return mt, true
		}
		return spool, true
	case hasSpool:
		return spool, true
	case hasMt:
		return mt, true
	}
	return NativeSignal{}, false
}

// ---- spawn-time injection -------------------------------------------------

// claudeSettingsOnce guards the once-per-process settings file write.
var (
	claudeSettingsOnce sync.Once
	claudeSettingsPath string
)

// claudeSettingsFile writes (once, idempotently) the shared claude settings
// file whose hooks call this mux binary back with --claude-hook, and returns
// its path. "" if the write failed or the binary path is unknown.
func claudeSettingsFile() string {
	claudeSettingsOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		path, err := writeClaudeSettings(exe)
		if err != nil {
			return
		}
		claudeSettingsPath = path
	})
	return claudeSettingsPath
}

// claudeHookEvents are the hook events wired into the shared settings file.
var claudeHookEvents = []string{
	"SessionStart", "UserPromptSubmit", "Stop", "SubagentStop", "Notification", "SessionEnd",
}

// writeClaudeSettings renders the settings JSON to
// $XDG_STATE_HOME/mux/claude-settings.json and returns the path.
func writeClaudeSettings(exe string) (string, error) {
	cmd := fmt.Sprintf("%q --claude-hook", exe)
	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookMatcher struct {
		Hooks []hookEntry `json:"hooks"`
	}
	hooks := map[string][]hookMatcher{}
	for _, ev := range claudeHookEvents {
		hooks[ev] = []hookMatcher{{Hooks: []hookEntry{{Type: "command", Command: cmd}}}}
	}
	data, err := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(eventsDir()), "claude-settings.json")
	if err := atomicWrite(path, append(data, '\n')); err != nil {
		return "", err
	}
	return path, nil
}

// nativePreArgs returns the per-spawn root flags that wire a provider's
// native signals back into mux, or nil when injection is off / unsupported /
// impossible. Never mutates the shared Provider table.
func nativePreArgs(providerName string) []string {
	if !NativeStatusEnabled() {
		return nil
	}
	switch providerName {
	case "claude":
		path := claudeSettingsFile()
		if path == "" {
			return nil
		}
		return []string{"--settings", path}
	case "codex":
		exe, err := os.Executable()
		if err != nil {
			return nil
		}
		return []string{"-c", fmt.Sprintf(`notify=[%q,"--notify-handler"]`, exe)}
	}
	return nil
}

// injectNative returns a per-spawn Provider copy with native-status flags
// appended to PreArgs, plus the extra tmux new-session args (-e env) the
// spawn needs. When native status is off or the provider is unsupported it
// returns the provider untouched and nil extra args — the spawn is
// byte-identical to a non-native one.
func injectNative(agentID string, p Provider) (Provider, []string) {
	inj := nativePreArgs(p.Name)
	if len(inj) == 0 {
		return p, nil
	}
	pre := make([]string, 0, len(p.PreArgs)+len(inj))
	pre = append(pre, p.PreArgs...)
	pre = append(pre, inj...)
	p.PreArgs = pre
	return p, []string{"-e", "MUX_AGENT_ID=" + agentID}
}

// MapClaudeHookEvent maps a claude settings hook event name to the mux
// status it implies. ok=false for events that carry no status transition
// (SessionStart, unknown events).
func MapClaudeHookEvent(name string) (Status, bool) {
	switch name {
	case "UserPromptSubmit":
		return StatusActive, true
	case "Stop", "SubagentStop", "Notification":
		return StatusWaiting, true
	case "SessionEnd":
		return StatusIdle, true
	}
	return StatusUnknown, false
}

// CaptureUUIDForAgent is CaptureUUID plus the deterministic fast path: the
// claude SessionStart hook spools the session uuid to <agent>.sid, so poll
// that file alongside the mtime-diff scan. Falls back to pure mtime capture
// (and eventually "") exactly like CaptureUUID.
func CaptureUUIDForAgent(agentID, provider, cwd string, before Snapshot, timeout time.Duration) string {
	// sid spooling only happens when native status is on; with the gate off
	// (or an unsupported provider) an old sid file must not leak in.
	useSid := NativeStatusEnabled() && nativeProviders[provider]
	if useSid {
		if sid := SpoolSessionID(agentID); sid != "" {
			return sid
		}
	}
	dir := SessionDir(provider, cwd)
	if dir == "" && !useSid {
		return ""
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if useSid {
			if sid := SpoolSessionID(agentID); sid != "" {
				return sid
			}
		}
		if u := scanNewJsonl(provider, dir, before); u != "" {
			return u
		}
	}
	return ""
}
