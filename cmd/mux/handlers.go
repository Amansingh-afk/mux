package main

import (
	"encoding/json"
	"io"
	"os"

	"github.com/Amansingh-afk/mux/internal/session"
)

// Handler modes: the mux binary doubles as the hook/notify target the
// providers invoke. Both handlers are best-effort — they exit 0 silently on
// any parse or IO problem so they can never break the host agent.

// claudeHookPayload is the subset of the claude hook stdin JSON mux reads.
type claudeHookPayload struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
}

// runClaudeHook implements `mux --claude-hook`: reads the hook event JSON
// from stdin, spools the implied status, and — for any event carrying a
// session_id — spools the provider session uuid for deterministic capture.
func runClaudeHook(r io.Reader) {
	agentID := os.Getenv("MUX_AGENT_ID")
	if agentID == "" {
		return
	}
	data, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return
	}
	var ev claudeHookPayload
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	if session.ValidUUID(ev.SessionID) {
		_ = session.WriteSpoolSessionID(agentID, ev.SessionID)
	}
	if st, reason, ok := session.MapClaudeHookEvent(ev.HookEventName); ok {
		_ = session.WriteSpoolEvent(agentID, st, reason)
	}
}

// codexNotifyPayload is the subset of codex's notify JSON mux reads.
type codexNotifyPayload struct {
	Type string `json:"type"`
}

// runNotifyHandler implements `mux --notify-handler <json>` (codex `notify`
// invokes the program with the notification JSON as the last argument).
func runNotifyHandler(args []string) {
	agentID := os.Getenv("MUX_AGENT_ID")
	if agentID == "" || len(args) == 0 {
		return
	}
	var ev codexNotifyPayload
	if json.Unmarshal([]byte(args[len(args)-1]), &ev) != nil {
		return
	}
	if ev.Type == "agent-turn-complete" {
		_ = session.WriteSpoolEvent(agentID, session.StatusWaiting, session.ReasonDone)
	}
}
