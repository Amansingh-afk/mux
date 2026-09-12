package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	tokUUIDClaude = "aaaaaaaa-1111-4111-8111-111111111111"
	tokUUIDCodex  = "bbbbbbbb-2222-4222-8222-222222222222"
)

func claudeUsageLine(id string, in, out, cr, cc int64) string {
	n := strconv.FormatInt
	return `{"type":"assistant","message":{"id":"` + id + `","role":"assistant",` +
		`"usage":{"input_tokens":` + n(in, 10) +
		`,"output_tokens":` + n(out, 10) +
		`,"cache_read_input_tokens":` + n(cr, 10) +
		`,"cache_creation_input_tokens":` + n(cc, 10) + `}}}`
}

func TestTokensForClaudeSumsAndDedupes(t *testing.T) {
	tmp := t.TempDir()
	overrideSessionDir(t, "claude", tmp)

	lines := []string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		claudeUsageLine("msg_a", 2, 75, 26351, 12458),
		// streamed chunks: same message id repeated with identical usage -> counted once
		claudeUsageLine("msg_b", 101, 356, 42457, 939),
		claudeUsageLine("msg_b", 101, 356, 42457, 939),
		claudeUsageLine("msg_b", 101, 356, 42457, 939),
		// non-assistant line mentioning usage must be ignored
		`{"type":"user","message":{"role":"user","content":"talk about \"usage\":{} here"}}`,
		`not even json {"usage"`,
		claudeUsageLine("msg_c", 7, 10, 0, 0),
	}
	p := filepath.Join(tmp, tokUUIDClaude+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := TokensFor("claude", "/any/project", tokUUIDClaude)
	if !ok {
		t.Fatal("TokensFor = !ok, want ok")
	}
	want := TokenUsage{Input: 2 + 101 + 7, Output: 75 + 356 + 10, CacheRead: 26351 + 42457, CacheCreate: 12458 + 939}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	if got.Total() != want.Input+want.Output+want.CacheRead+want.CacheCreate {
		t.Fatalf("Total = %d", got.Total())
	}
}

func TestTokensForMissingOrInvalid(t *testing.T) {
	tmp := t.TempDir()
	overrideSessionDir(t, "claude", tmp)

	if u, ok := TokensFor("claude", "/any", "cccccccc-3333-4333-8333-333333333333"); ok || u != (TokenUsage{}) {
		t.Fatalf("missing file: got (%+v, %v), want (zero, false)", u, ok)
	}
	if _, ok := TokensFor("claude", "/any", "not-a-uuid"); ok {
		t.Fatal("invalid uuid: want false")
	}
	if _, ok := TokensFor("gemini", "/any", tokUUIDClaude); ok {
		t.Fatal("unsupported provider: want false")
	}
}

func TestTokensForCodexCumulative(t *testing.T) {
	tmp := t.TempDir()
	overrideSessionDir(t, "codex", tmp)

	day := filepath.Join(tmp, "2026", "09", "03")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"session_meta","payload":{"session_id":"` + tokUUIDCodex + `","cwd":"/home/u/proj"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":14218,"cached_input_tokens":11008,"cache_write_input_tokens":0,"output_tokens":131,"total_tokens":14349},"last_token_usage":{"input_tokens":14218,"cached_input_tokens":11008,"cache_write_input_tokens":0,"output_tokens":131,"total_tokens":14349}}}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"..."}}`,
		// the LAST total wins; the intermediate one above must be superseded
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":479224,"cached_input_tokens":411904,"cache_write_input_tokens":16,"output_tokens":4505,"total_tokens":483729},"last_token_usage":{"input_tokens":76766,"cached_input_tokens":64000,"cache_write_input_tokens":0,"output_tokens":2217,"total_tokens":78983}}}}`,
	}
	p := filepath.Join(day, "rollout-2026-09-03T01-19-39-"+tokUUIDCodex+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := TokensFor("codex", "/home/u/proj", tokUUIDCodex)
	if !ok {
		t.Fatal("TokensFor = !ok, want ok")
	}
	want := TokenUsage{Input: 479224 - 411904, Output: 4505, CacheRead: 411904, CacheCreate: 16}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	// codex total_tokens = input + output; normalized Total must match
	// (plus cache writes, split out separately).
	if got.Total() != 483729+16 {
		t.Fatalf("Total = %d, want %d", got.Total(), 483729+16)
	}
}

func TestTokensForCodexSummedWhenNoTotal(t *testing.T) {
	tmp := t.TempDir()
	overrideSessionDir(t, "codex", tmp)

	uuid := "dddddddd-4444-4444-8444-444444444444"
	lines := []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"cache_write_input_tokens":10,"output_tokens":50}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":2000,"cached_input_tokens":1400,"cache_write_input_tokens":0,"output_tokens":150}}}}`,
	}
	// flat file directly under the session root also resolves
	p := filepath.Join(tmp, "rollout-2026-09-03T02-00-00-"+uuid+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := TokensFor("codex", "/home/u/proj", uuid)
	if !ok {
		t.Fatal("TokensFor = !ok, want ok")
	}
	want := TokenUsage{Input: 3000 - 2000, Output: 200, CacheRead: 2000, CacheCreate: 10}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}

func TestTokensForCacheHit(t *testing.T) {
	tmp := t.TempDir()
	overrideSessionDir(t, "claude", tmp)

	uuid := "eeeeeeee-5555-4555-8555-555555555555"
	p := filepath.Join(tmp, uuid+".jsonl")
	if err := os.WriteFile(p, []byte(claudeUsageLine("msg_1", 10, 20, 30, 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// pin a stable mtime so the append below is guaranteed to change it
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(p, past, past); err != nil {
		t.Fatal(err)
	}

	before := tokenParses.Load()
	first, ok := TokensFor("claude", "/proj", uuid)
	if !ok {
		t.Fatal("first call: !ok")
	}
	if got := tokenParses.Load() - before; got != 1 {
		t.Fatalf("parses after first call = %d, want 1", got)
	}

	second, ok := TokensFor("claude", "/proj", uuid)
	if !ok || second != first {
		t.Fatalf("second call: (%+v, %v), want cached %+v", second, ok, first)
	}
	if got := tokenParses.Load() - before; got != 1 {
		t.Fatalf("parses after cached call = %d, want 1 (no re-read)", got)
	}

	// growing the file (new size + mtime) must invalidate the cache
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(claudeUsageLine("msg_2", 1, 2, 3, 4) + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	third, ok := TokensFor("claude", "/proj", uuid)
	if !ok {
		t.Fatal("third call: !ok")
	}
	want := TokenUsage{Input: 11, Output: 22, CacheRead: 33, CacheCreate: 44}
	if third != want {
		t.Fatalf("after append: %+v, want %+v", third, want)
	}
	if got := tokenParses.Load() - before; got != 2 {
		t.Fatalf("parses after append = %d, want 2", got)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{-5, "0"},
		{941, "941"},
		{999, "999"},
		{1000, "1k"},
		{1049, "1k"},
		{1500, "1.5k"},
		{9949, "9.9k"},
		{12000, "12k"},
		{999999, "999k"},
		{1000000, "1M"},
		{1200000, "1.2M"},
		{15000000, "15M"},
		{999999999, "999M"},
		{2500000000, "2.5G"},
	}
	for _, c := range cases {
		if got := FormatTokens(c.n); got != c.want {
			t.Errorf("FormatTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
