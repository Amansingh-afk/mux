package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setSpoolDir points the spool at a temp dir for the duration of the test.
func setSpoolDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return dir
}

// registerNativeProvider registers a synthetic provider as native-probe
// capable, pointing its session dir at dir. Restored on cleanup.
func registerNativeProvider(t *testing.T, name, dir string) {
	t.Helper()
	registerFakeProvider(t, name, dir)
	prev, existed := nativeProviders[name]
	nativeProviders[name] = true
	t.Cleanup(func() {
		if existed {
			nativeProviders[name] = prev
		} else {
			delete(nativeProviders, name)
		}
	})
}

// writeSpoolRaw writes raw spool-file content, creating the events dir the
// way a handler's WriteSpoolEvent would.
func writeSpoolRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, content)
}

func TestSpoolEventRoundtrip(t *testing.T) {
	setSpoolDir(t)
	const id = "mux_proj_claude_1"

	if _, ok := readSpool(id); ok {
		t.Fatal("readSpool on missing file should report not-ok")
	}

	before := time.Now().Add(-time.Second)
	if err := WriteSpoolEvent(id, StatusActive); err != nil {
		t.Fatal(err)
	}
	sig, ok := readSpool(id)
	if !ok {
		t.Fatal("readSpool = !ok after write")
	}
	if sig.Status != StatusActive {
		t.Errorf("status = %q, want active", sig.Status)
	}
	if sig.At.Before(before) || sig.At.After(time.Now().Add(time.Second)) {
		t.Errorf("At = %v, want ~now", sig.At)
	}

	// overwrite semantics: second write replaces, never appends.
	if err := WriteSpoolEvent(id, StatusWaiting); err != nil {
		t.Fatal(err)
	}
	sig, ok = readSpool(id)
	if !ok || sig.Status != StatusWaiting {
		t.Fatalf("after overwrite: sig=%+v ok=%v, want waiting", sig, ok)
	}
	data, err := os.ReadFile(EventFilePath(id))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimRight(string(data), "\n"), "\n"); n != 0 {
		t.Errorf("spool file has %d embedded newlines, want single-line JSON: %q", n, data)
	}
}

func TestReadSpoolRejectsGarbage(t *testing.T) {
	setSpoolDir(t)
	const id = "mux_bad_1"
	writeSpoolRaw(t, EventFilePath(id), `{"status":"levitating","ts":123}`)
	if _, ok := readSpool(id); ok {
		t.Error("readSpool accepted unknown status")
	}
	writeSpoolRaw(t, EventFilePath(id), `not json`)
	if _, ok := readSpool(id); ok {
		t.Error("readSpool accepted malformed JSON")
	}
	writeSpoolRaw(t, EventFilePath(id), `{"status":"idle","ts":0}`)
	if _, ok := readSpool(id); ok {
		t.Error("readSpool accepted zero ts")
	}
}

// TestReadSpoolStaleness verifies At reflects the spooled ts, so the merge
// layer can distinguish fresh (≤5s) from stale native signals.
func TestReadSpoolStaleness(t *testing.T) {
	setSpoolDir(t)
	const id = "mux_stale_1"
	old := time.Now().Add(-90 * time.Second).Unix()
	writeSpoolRaw(t, EventFilePath(id), fmt.Sprintf(`{"status":"waiting","ts":%d}`, old))
	sig, ok := readSpool(id)
	if !ok {
		t.Fatal("readSpool = !ok")
	}
	if sig.At.Unix() != old {
		t.Errorf("At = %v, want unix %d", sig.At, old)
	}
	if time.Since(sig.At) <= 5*time.Second {
		t.Errorf("signal should read as stale, At=%v", sig.At)
	}
}

func TestSpoolSessionID(t *testing.T) {
	setSpoolDir(t)
	const id = "mux_sid_1"
	if got := SpoolSessionID(id); got != "" {
		t.Errorf("missing sid file: got %q, want empty", got)
	}
	if err := WriteSpoolSessionID(id, testUUID); err != nil {
		t.Fatal(err)
	}
	if got := SpoolSessionID(id); got != testUUID {
		t.Errorf("SpoolSessionID = %q, want %q", got, testUUID)
	}
	// invalid contents rejected
	writeSpoolRaw(t, SidFilePath(id), "not-a-uuid\n")
	if got := SpoolSessionID(id); got != "" {
		t.Errorf("invalid sid: got %q, want empty", got)
	}
}

func TestNativeProbeMtimeFreshness(t *testing.T) {
	setSpoolDir(t)
	dir := t.TempDir()
	registerNativeProvider(t, "faketest-native", dir)
	ref := AgentRef{
		ID: "mux_p_faketest_native_1", Provider: "faketest-native",
		Dir: "/whatever", SessionUUID: testUUID,
	}

	// no transcript, no spool -> no signal
	if _, ok := NativeProbe(ref); ok {
		t.Fatal("probe with no sources should report not-ok")
	}

	// fresh transcript -> active signal at mtime
	path := filepath.Join(dir, testUUID+".jsonl")
	writeFile(t, path, "line")
	sig, ok := NativeProbe(ref)
	if !ok || sig.Status != StatusActive {
		t.Fatalf("fresh mtime: sig=%+v ok=%v, want active", sig, ok)
	}

	// stale transcript -> no signal
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := NativeProbe(ref); ok {
		t.Error("stale mtime should not signal")
	}

	// uuid unknown -> mtime source unavailable
	noUUID := ref
	noUUID.SessionUUID = ""
	writeFile(t, path, "line2") // fresh again
	if _, ok := NativeProbe(noUUID); ok {
		t.Error("probe without uuid should not use transcript mtime")
	}
}

func TestNativeProbeSpoolVsMtimePrecedence(t *testing.T) {
	setSpoolDir(t)
	dir := t.TempDir()
	registerNativeProvider(t, "faketest-prec", dir)
	ref := AgentRef{
		ID: "mux_p_faketest_prec_1", Provider: "faketest-prec",
		Dir: "/whatever", SessionUUID: testUUID,
	}
	path := filepath.Join(dir, testUUID+".jsonl")

	// spool only
	if err := WriteSpoolEvent(ref.ID, StatusWaiting); err != nil {
		t.Fatal(err)
	}
	sig, ok := NativeProbe(ref)
	if !ok || sig.Status != StatusWaiting {
		t.Fatalf("spool only: sig=%+v ok=%v, want waiting", sig, ok)
	}

	// mtime fresher than spool AND indicating activity -> mtime wins.
	// WriteSpoolEvent has 1s ts granularity; make the transcript strictly newer.
	writeFile(t, path, "x")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	sig, ok = NativeProbe(ref)
	if !ok || sig.Status != StatusActive {
		t.Fatalf("fresher mtime: sig=%+v ok=%v, want active", sig, ok)
	}

	// spool newer than mtime -> spool wins even though mtime is fresh.
	past := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if err := WriteSpoolEvent(ref.ID, StatusIdle); err != nil {
		t.Fatal(err)
	}
	sig, ok = NativeProbe(ref)
	if !ok || sig.Status != StatusIdle {
		t.Fatalf("newer spool: sig=%+v ok=%v, want idle", sig, ok)
	}
}

func TestNativeProbeGates(t *testing.T) {
	setSpoolDir(t)
	ref := AgentRef{ID: "mux_x_1", Provider: "gemini", Dir: "/w"}
	if _, ok := NativeProbe(ref); ok {
		t.Error("unsupported provider should not probe")
	}

	dir := t.TempDir()
	registerNativeProvider(t, "faketest-gate", dir)
	gated := AgentRef{ID: "mux_g_1", Provider: "faketest-gate", Dir: "/w", SessionUUID: testUUID}
	if err := WriteSpoolEvent(gated.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	SetNativeStatus(false)
	t.Cleanup(func() { SetNativeStatus(true) })
	if _, ok := NativeProbe(gated); ok {
		t.Error("probe should be off when native status is disabled")
	}
}

func TestClearSpool(t *testing.T) {
	setSpoolDir(t)
	const id = "mux_clear_1"
	if err := WriteSpoolEvent(id, StatusActive); err != nil {
		t.Fatal(err)
	}
	if err := WriteSpoolSessionID(id, testUUID); err != nil {
		t.Fatal(err)
	}
	ClearSpool(id)
	if _, ok := readSpool(id); ok {
		t.Error("event spool survived ClearSpool")
	}
	if got := SpoolSessionID(id); got != "" {
		t.Errorf("sid spool survived ClearSpool: %q", got)
	}
}

func TestMapClaudeHookEvent(t *testing.T) {
	tests := []struct {
		event string
		want  Status
		ok    bool
	}{
		{"UserPromptSubmit", StatusActive, true},
		{"Stop", StatusWaiting, true},
		{"SubagentStop", StatusWaiting, true},
		{"Notification", StatusWaiting, true},
		{"SessionEnd", StatusIdle, true},
		{"SessionStart", StatusUnknown, false},
		{"PreToolUse", StatusUnknown, false},
		{"", StatusUnknown, false},
	}
	for _, tc := range tests {
		got, ok := MapClaudeHookEvent(tc.event)
		if got != tc.want || ok != tc.ok {
			t.Errorf("MapClaudeHookEvent(%q) = (%q, %v), want (%q, %v)",
				tc.event, got, ok, tc.want, tc.ok)
		}
	}
}

func TestWriteClaudeSettingsSchema(t *testing.T) {
	setSpoolDir(t)
	path, err := writeClaudeSettings("/opt/bin/mux binary")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("settings not valid JSON: %v", err)
	}
	for _, ev := range claudeHookEvents {
		ms, ok := doc.Hooks[ev]
		if !ok || len(ms) != 1 || len(ms[0].Hooks) != 1 {
			t.Fatalf("event %s missing or malformed: %s", ev, data)
		}
		h := ms[0].Hooks[0]
		if h.Type != "command" {
			t.Errorf("%s hook type = %q, want command", ev, h.Type)
		}
		if !strings.Contains(h.Command, "--claude-hook") ||
			!strings.Contains(h.Command, `"/opt/bin/mux binary"`) {
			t.Errorf("%s hook command = %q, want quoted exe + --claude-hook", ev, h.Command)
		}
	}
	if len(doc.Hooks) != len(claudeHookEvents) {
		t.Errorf("settings has %d events, want %d", len(doc.Hooks), len(claudeHookEvents))
	}
}

func TestInjectNative(t *testing.T) {
	setSpoolDir(t)
	claude := Provider{Name: "claude", Cmd: "claude", PreArgs: []string{"--keep"}}
	codex := Provider{Name: "codex", Cmd: "codex"}
	gemini := Provider{Name: "gemini", Cmd: "gemini"}

	// gate off: byte-identical provider, no env args.
	SetNativeStatus(false)
	p, env := injectNative("mux_a_1", claude)
	if env != nil || len(p.PreArgs) != 1 || p.PreArgs[0] != "--keep" {
		t.Fatalf("gate off: p=%+v env=%v, want untouched", p, env)
	}
	SetNativeStatus(true)
	t.Cleanup(func() { SetNativeStatus(true) })

	// unsupported provider: untouched even with gate on.
	if p, env := injectNative("mux_a_1", gemini); env != nil || len(p.PreArgs) != 0 {
		t.Fatalf("gemini: p=%+v env=%v, want untouched", p, env)
	}

	// claude: --settings <file> appended after existing PreArgs, env set.
	p, env = injectNative("mux_a_1", claude)
	if len(p.PreArgs) != 3 || p.PreArgs[0] != "--keep" || p.PreArgs[1] != "--settings" {
		t.Fatalf("claude PreArgs = %v, want [--keep --settings <path>]", p.PreArgs)
	}
	if _, err := os.Stat(p.PreArgs[2]); err != nil {
		t.Errorf("settings file %q not written: %v", p.PreArgs[2], err)
	}
	wantEnv := []string{"-e", "MUX_AGENT_ID=mux_a_1"}
	if len(env) != 2 || env[0] != wantEnv[0] || env[1] != wantEnv[1] {
		t.Errorf("claude env = %v, want %v", env, wantEnv)
	}
	// shared table must not be mutated
	if len(claude.PreArgs) != 1 {
		t.Errorf("original provider mutated: %v", claude.PreArgs)
	}

	// codex: -c notify=[...] root flags.
	p, env = injectNative("mux_b_2", codex)
	if len(p.PreArgs) != 2 || p.PreArgs[0] != "-c" ||
		!strings.HasPrefix(p.PreArgs[1], `notify=["`) ||
		!strings.Contains(p.PreArgs[1], `","--notify-handler"]`) {
		t.Fatalf("codex PreArgs = %v, want -c notify=[<exe>,--notify-handler]", p.PreArgs)
	}
	if len(env) != 2 || env[1] != "MUX_AGENT_ID=mux_b_2" {
		t.Errorf("codex env = %v", env)
	}
}

func TestCaptureUUIDForAgentSidFastPath(t *testing.T) {
	setSpoolDir(t)
	dir := t.TempDir()
	registerNativeProvider(t, "faketest-sid", dir)
	const agentID = "mux_p_faketest_sid_1"

	if err := WriteSpoolSessionID(agentID, testUUID); err != nil {
		t.Fatal(err)
	}
	got := CaptureUUIDForAgent(agentID, "faketest-sid", "/whatever", Snapshot{}, 2*time.Second)
	if got != testUUID {
		t.Errorf("sid fast path = %q, want %q", got, testUUID)
	}

	// with the gate off the sid must be ignored; falls back to mtime scan.
	SetNativeStatus(false)
	t.Cleanup(func() { SetNativeStatus(true) })
	before := SnapshotFor("faketest-sid", "/whatever")
	other := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeFile(t, filepath.Join(dir, other+".jsonl"), "new")
	got = CaptureUUIDForAgent(agentID, "faketest-sid", "/whatever", before, 2*time.Second)
	if got != other {
		t.Errorf("gate off = %q, want mtime-scan result %q", got, other)
	}
}

func TestCaptureUUIDForAgentMtimeFallback(t *testing.T) {
	setSpoolDir(t)
	dir := t.TempDir()
	registerNativeProvider(t, "faketest-fall", dir)
	const agentID = "mux_p_faketest_fall_1"

	before := SnapshotFor("faketest-fall", "/whatever")
	writeFile(t, filepath.Join(dir, testUUID+".jsonl"), "new")
	got := CaptureUUIDForAgent(agentID, "faketest-fall", "/whatever", before, 2*time.Second)
	if got != testUUID {
		t.Errorf("mtime fallback = %q, want %q", got, testUUID)
	}
}
