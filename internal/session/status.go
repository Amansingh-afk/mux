package session

import (
	"hash/fnv"
	"strings"
)

type Status string

const (
	StatusUnknown Status = ""
	StatusDead    Status = "dead"
	StatusActive  Status = "active"
	StatusWaiting Status = "waiting"
	StatusIdle    Status = "idle"
)

// spinner chars CLI agents commonly use
var spinnerChars = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏', '⠟', '⠯', '⠷', '⠾', '⠽', '⠻'}

// SpinnerFrames returns the animation frames used to indicate activity.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Hash returns a fast 64-bit fingerprint of content for change detection.
func Hash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// Classify returns the inferred status for a pane capture.
// prevHash / prevStatus carry state from the last classification so we can
// tell "stable" from "just changed".
func Classify(content string, prevHash uint64, prevStatus Status, stableTicks int) (Status, uint64, int) {
	curHash := Hash(content)
	changed := curHash != prevHash

	if changed {
		return StatusActive, curHash, 0
	}

	ticks := stableTicks + 1
	// detect spinner chars even if unchanged (some apps render on same cell)
	if containsAny(content, spinnerChars) && ticks < 3 {
		return StatusActive, curHash, ticks
	}

	if looksWaiting(content) {
		return StatusWaiting, curHash, ticks
	}
	return StatusIdle, curHash, ticks
}

func containsAny(s string, runes []rune) bool {
	for _, r := range runes {
		if strings.ContainsRune(s, r) {
			return true
		}
	}
	return false
}

// looksWaiting checks whether the tail of the output looks like an idle input
// prompt waiting for the user (e.g. lines starting with ">", "❯", "$").
func looksWaiting(content string) bool {
	content = strings.TrimRight(content, "\n \t")
	lines := strings.Split(content, "\n")
	// check last few non-empty lines
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-5; i-- {
		l := stripANSI(lines[i])
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		for _, p := range []string{"> ", "❯ ", "$ ", "» ", "│ >", "▌ >"} {
			if strings.HasPrefix(l, p) || strings.Contains(l, p) {
				return true
			}
		}
	}
	return false
}

// stripANSI removes ANSI escape sequences for prompt matching.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if (r >= '@' && r <= '~') || r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == 0x1b {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
