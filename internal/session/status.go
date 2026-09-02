package session

import (
	"hash/fnv"
	"regexp"
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

// promptPrefixes are the leading markers CLI agents render when idle-waiting
// for user input. Must be prefix-only — substring match would false-positive
// on markdown quotes, diff hunks, and shell output.
var promptPrefixes = []string{"> ", "❯ ", "$ ", "» ", "│ > ", "▌ > ", "│ >", "▌ >"}

// looksWaiting scans the tail of the pane for a prompt marker. TUIs like
// Claude Code render chrome below the actual input box ("? for shortcuts",
// "[Request interrupted]"), so checking only the last line misses the real
// `❯ ` prompt. tailScan caps the scan depth so markdown quotes (`> ...`)
// in earlier scrollback can't false-positive as a prompt.
func looksWaiting(content string) bool {
	content = strings.TrimRight(content, "\n \t")
	lines := strings.Split(content, "\n")
	const tailScan = 8
	start := len(lines) - tailScan
	if start < 0 {
		start = 0
	}
	for i := len(lines) - 1; i >= start; i-- {
		l := stripANSI(lines[i])
		trimmed := strings.TrimLeft(l, " \t")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		for _, p := range promptPrefixes {
			// tmux capture (and the whole-content TrimRight above) can eat
			// the trailing space after a bare prompt marker, so a marker
			// alone on the line must also count.
			if strings.HasPrefix(trimmed, p) || trimmed == strings.TrimRight(p, " ") {
				return true
			}
		}
	}
	return false
}

// ansiRe matches the common escape families we see in agent output:
//
//	CSI  ESC [ params final-byte
//	OSC  ESC ] ... BEL or ESC \
//	SS2/SS3 + simple 2-char sequences (ESC + alnum)
//
// narrower than a full vt100 parser, but covers SGR colors, cursor moves, and
// the title/notify sequences claude/codex emit.
var ansiRe = regexp.MustCompile(
	`\x1b\[[0-?]*[ -/]*[@-~]` +
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` +
		`|\x1b[@-Z\\-_]`,
)

// stripANSI removes ANSI escape sequences for prompt matching.
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}
