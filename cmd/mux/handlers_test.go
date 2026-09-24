package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Amansingh-afk/mux/internal/session"
)

const testUUID = "550e8400-e29b-41d4-a716-446655440000"

func setup(t *testing.T, agentID string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MUX_AGENT_ID", agentID)
}

func spooledStatus(t *testing.T, agentID string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(session.EventFilePath(agentID))
	if err != nil {
		return "", false
	}
	var ev struct {
		Status string `json:"status"`
		TS     int64  `json:"ts"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("spool not valid JSON: %v", err)
	}
	return ev.Status, true
}

func TestRunClaudeHookStdinMapping(t *testing.T) {
	tests := []struct {
		event      string
		wantStatus string
		wantSpool  bool
	}{
		{"UserPromptSubmit", "active", true},
		{"Stop", "waiting", true},
		{"SubagentStop", "waiting", true},
		{"Notification", "waiting", true},
		{"SessionEnd", "idle", true},
		{"SessionStart", "", false}, // no status change
		{"PreToolUse", "", false},   // unknown event ignored
	}
	for _, tc := range tests {
		t.Run(tc.event, func(t *testing.T) {
			const id = "mux_p_claude_1"
			setup(t, id)
			runClaudeHook(strings.NewReader(
				`{"hook_event_name":"` + tc.event + `","session_id":"` + testUUID + `"}`))
			st, ok := spooledStatus(t, id)
			if ok != tc.wantSpool || st != tc.wantStatus {
				t.Errorf("event %s: spool=(%q,%v), want (%q,%v)",
					tc.event, st, ok, tc.wantStatus, tc.wantSpool)
			}
			// every event carrying a session_id captures the uuid.
			if got := session.SpoolSessionID(id); got != testUUID {
				t.Errorf("event %s: sid = %q, want %q", tc.event, got, testUUID)
			}
		})
	}
}

func TestRunClaudeHookRobustness(t *testing.T) {
	const id = "mux_p_claude_2"

	// malformed JSON: silent no-op.
	setup(t, id)
	runClaudeHook(strings.NewReader("{not json"))
	if _, ok := spooledStatus(t, id); ok {
		t.Error("malformed JSON wrote a spool event")
	}

	// no MUX_AGENT_ID: silent no-op.
	setup(t, id)
	t.Setenv("MUX_AGENT_ID", "")
	runClaudeHook(strings.NewReader(`{"hook_event_name":"Stop"}`))
	if _, ok := spooledStatus(t, id); ok {
		t.Error("missing agent id wrote a spool event")
	}

	// invalid session_id: no sid write, status still spools.
	setup(t, id)
	runClaudeHook(strings.NewReader(`{"hook_event_name":"Stop","session_id":"nope"}`))
	if st, ok := spooledStatus(t, id); !ok || st != "waiting" {
		t.Errorf("status = (%q,%v), want (waiting,true)", st, ok)
	}
	if got := session.SpoolSessionID(id); got != "" {
		t.Errorf("invalid session_id spooled: %q", got)
	}
}

func TestRunNotifyHandler(t *testing.T) {
	const id = "mux_p_codex_1"

	setup(t, id)
	runNotifyHandler([]string{`{"type":"agent-turn-complete","turn-id":"x"}`})
	if st, ok := spooledStatus(t, id); !ok || st != "waiting" {
		t.Errorf("turn-complete: spool=(%q,%v), want (waiting,true)", st, ok)
	}

	// other notification types ignored.
	setup(t, id)
	runNotifyHandler([]string{`{"type":"something-else"}`})
	if _, ok := spooledStatus(t, id); ok {
		t.Error("unknown notify type wrote a spool event")
	}

	// malformed / missing arg: silent no-op.
	setup(t, id)
	runNotifyHandler([]string{"{bad"})
	runNotifyHandler(nil)
	if _, ok := spooledStatus(t, id); ok {
		t.Error("malformed notify wrote a spool event")
	}
}
