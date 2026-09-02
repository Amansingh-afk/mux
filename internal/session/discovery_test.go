package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryParseClaudePreviewStringContent(t *testing.T) {
	in := `{"type":"summary","summary":"stuff"}
{"type":"user","message":{"role":"user","content":"  fix the   flaky test\nplease  "},"uuid":"x"}
{"type":"assistant","message":{"role":"assistant","content":"ok"}}`
	got := parseClaudePreview(strings.NewReader(in))
	want := "fix the flaky test please"
	if got != want {
		t.Fatalf("preview = %q, want %q", got, want)
	}
}

func TestDiscoveryParseClaudePreviewBlockContent(t *testing.T) {
	in := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello from blocks"}]}}`
	if got := parseClaudePreview(strings.NewReader(in)); got != "hello from blocks" {
		t.Fatalf("preview = %q, want %q", got, "hello from blocks")
	}
}

func TestDiscoveryParseClaudePreviewSkipsToolResultAndMeta(t *testing.T) {
	in := `{"type":"user","isMeta":true,"message":{"role":"user","content":"Caveat: injected"}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"file listing"}]}}
{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"}}
{"type":"user","message":{"role":"user","content":"the real question"}}`
	if got := parseClaudePreview(strings.NewReader(in)); got != "the real question" {
		t.Fatalf("preview = %q, want %q", got, "the real question")
	}
}

func TestDiscoveryParseClaudePreviewMissingUserLine(t *testing.T) {
	in := `{"type":"summary","summary":"s"}
{"type":"assistant","message":{"role":"assistant","content":"hi"}}
not even json`
	if got := parseClaudePreview(strings.NewReader(in)); got != "" {
		t.Fatalf("preview = %q, want empty", got)
	}
}

func TestDiscoveryParseClaudePreviewCapsLength(t *testing.T) {
	long := strings.Repeat("a ", 200)
	in := `{"type":"user","message":{"role":"user","content":"` + long + `"}}`
	got := parseClaudePreview(strings.NewReader(in))
	if got == "" || len([]rune(got)) > 80 {
		t.Fatalf("preview length = %d (%q), want 1..80 runes", len([]rune(got)), got)
	}
}

func TestDiscoveryRolloutMatchesCwd(t *testing.T) {
	header := `{"timestamp":"2026-08-22T17:39:56Z","type":"session_meta","payload":{"session_id":"01a02a8d-b995-72e0-82fc-0cefe0d50091","cwd":"/home/u/proj","originator":"codex-tui"}}`
	if !rolloutMatchesCwd(strings.NewReader(header), "/home/u/proj") {
		t.Fatal("expected match for exact cwd")
	}
	if rolloutMatchesCwd(strings.NewReader(header), "/home/u/pro") {
		t.Fatal("prefix of cwd must not match")
	}
	if rolloutMatchesCwd(strings.NewReader(header), "/home/u/proj2") {
		t.Fatal("different dir must not match")
	}
	if rolloutMatchesCwd(strings.NewReader(""), "/home/u/proj") {
		t.Fatal("empty file must not match")
	}
}

func TestDiscoveryParseCodexPreview(t *testing.T) {
	in := `{"type":"session_meta","payload":{"cwd":"/home/u/proj"}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"<environment_context>\n<cwd>/home/u/proj</cwd>\n</environment_context>"}]}}
{"type":"event_msg","payload":{"type":"user_message","message":"refactor the parser"}}`
	if got := parseCodexPreview(strings.NewReader(in)); got != "refactor the parser" {
		t.Fatalf("preview = %q, want %q", got, "refactor the parser")
	}

	blocks := `{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"real input via blocks"}]}}`
	if got := parseCodexPreview(strings.NewReader(blocks)); got != "real input via blocks" {
		t.Fatalf("preview = %q, want %q", got, "real input via blocks")
	}

	if got := parseCodexPreview(strings.NewReader(`{"type":"turn_context","payload":{}}`)); got != "" {
		t.Fatalf("preview = %q, want empty", got)
	}
}

func TestDiscoverySortSessions(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := []SessionMeta{
		{UUID: "b", ModTime: t0},
		{UUID: "c", ModTime: t0.Add(2 * time.Hour)},
		{UUID: "a", ModTime: t0.Add(time.Hour)},
		{UUID: "aa", ModTime: t0.Add(time.Hour)}, // tie with "a"
	}
	sortSessions(s)
	got := []string{s[0].UUID, s[1].UUID, s[2].UUID, s[3].UUID}
	want := []string{"c", "a", "aa", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestDiscoveryListSessionsUnknownProvider(t *testing.T) {
	out, err := ListSessions("gemini", "/nowhere")
	if out != nil || err != nil {
		t.Fatalf("unknown provider: got (%v, %v), want (nil, nil)", out, err)
	}
}

// overrideSessionDir points a provider's session dir at a temp dir for the
// duration of the test, restoring the real resolver on cleanup.
func overrideSessionDir(t *testing.T, provider, dir string) {
	t.Helper()
	old, had := providerSessionDir[provider]
	providerSessionDir[provider] = func(string) string { return dir }
	t.Cleanup(func() {
		if had {
			providerSessionDir[provider] = old
		} else {
			delete(providerSessionDir, provider)
		}
	})
}

func TestDiscoveryListClaudeSessionsIntegration(t *testing.T) {
	tmp := t.TempDir()
	overrideSessionDir(t, "claude", tmp)

	older := "11111111-1111-4111-8111-111111111111"
	newer := "22222222-2222-4222-8222-222222222222"
	write := func(uuid, content string, mod time.Time) {
		p := filepath.Join(tmp, uuid+".jsonl")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-time.Hour)
	write(older, `{"type":"user","message":{"role":"user","content":"older session"}}`+"\n", base)
	write(newer, `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"newer session"}]}}`+"\n", base.Add(30*time.Minute))
	// non-jsonl and non-uuid files are ignored
	if err := os.WriteFile(filepath.Join(tmp, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "junk.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListSessions("claude", "/any/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (%+v)", len(got), got)
	}
	if got[0].UUID != newer || got[1].UUID != older {
		t.Fatalf("order = [%s %s], want newest first", got[0].UUID, got[1].UUID)
	}
	if got[0].Preview != "newer session" || got[1].Preview != "older session" {
		t.Fatalf("previews = [%q %q]", got[0].Preview, got[1].Preview)
	}
}

func TestDiscoveryListClaudeSessionsMissingDir(t *testing.T) {
	tmp := t.TempDir()
	overrideSessionDir(t, "claude", filepath.Join(tmp, "does-not-exist"))
	got, err := ListSessions("claude", "/any/project")
	if got != nil || err != nil {
		t.Fatalf("missing dir: got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestDiscoveryListCodexSessionsIntegration(t *testing.T) {
	tmp := t.TempDir()
	overrideSessionDir(t, "codex", tmp)

	proj := "/home/u/proj"
	day := filepath.Join(tmp, "2026", "09", "01")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	match := "33333333-3333-4333-8333-333333333333"
	other := "44444444-4444-4444-8444-444444444444"
	writeRollout := func(dir, uuid, cwd string) {
		content := `{"type":"session_meta","payload":{"session_id":"` + uuid + `","cwd":"` + cwd + `"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"user_message","message":"do the thing"}}` + "\n"
		p := filepath.Join(dir, "rollout-2026-09-01T10-00-00-"+uuid+".jsonl")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeRollout(day, match, proj)
	writeRollout(day, other, "/somewhere/else")
	// too-deep dir is skipped by the depth bound
	deep := filepath.Join(tmp, "a", "b", "c", "d", "e")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRollout(deep, "55555555-5555-4555-8555-555555555555", proj)

	got, err := ListSessions("codex", proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (%+v)", len(got), got)
	}
	if got[0].UUID != match {
		t.Fatalf("uuid = %s, want %s", got[0].UUID, match)
	}
	if got[0].Preview != "do the thing" {
		t.Fatalf("preview = %q, want %q", got[0].Preview, "do the thing")
	}
}
