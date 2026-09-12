package session

import (
	"os"
	"path/filepath"
	"strings"
)

// quicknav.go installs a prefixless alt-key layer on the outer tmux server so
// mux stays drivable while keyboard focus is inside an agent pane. The alt
// layer IS the keymap — every mux action lives on an alt chord, identical
// whether focus is on mux or an agent:
//
//	M-j / M-k / M-Down / M-Up      next / prev agent
//	M-h / M-l / M-Left / M-Right   prev / next tab
//	M-1 .. M-9                     jump project tab
//	M-n M-x M-r M-i M-v M-M M-z    agent actions
//	M-o M-w M-W                    project actions
//	M-q                            quit
//	M-Space                        toggle focus between mux and agent pane
//
// Forwarding works by binding each key to `send-keys -t <mux-pane> <key>`,
// so the alt keystroke itself lands in mux's Bubble Tea loop (as "alt+x"
// etc.) exactly as if typed with mux focused — one keymap, both ways.
//
// Deliberately NOT bound: M-b/M-f/M-d/M-./M-y/M-p — readline/zsh word nav,
// kill-word, last-arg and yank-pop, which shell agents and provider input
// boxes need intact.
//
// tmux key bindings are server-wide, so like the tab bar this is
// capture/set/restore: existing root-table bindings on our keys are saved at
// install (list-keys output is valid tmux config syntax) and sourced back on
// exit. On mux's auto-wrapped private server this is all moot; nested inside
// the user's own tmux it makes the takeover reversible. The quick_nav config
// flag disables the whole layer.

// forwardKeys are the keys mirrored into the mux pane as themselves.
var forwardKeys = []string{
	"M-j", "M-k", "M-Down", "M-Up",
	"M-h", "M-l", "M-Left", "M-Right",
	"M-1", "M-2", "M-3", "M-4", "M-5",
	"M-6", "M-7", "M-8", "M-9",
	"M-n", "M-x", "M-r", "M-i", "M-v", "M-M", "M-z",
	"M-o", "M-w", "M-W",
	"M-q",
}

const toggleKey = "M-Space"

// savedNavBindings holds the pre-mux root-table bindings for our keys, in
// tmux config syntax, captured once at install.
var savedNavBindings = struct {
	captured bool
	lines    []string
}{}

func quickNavKeys() []string {
	keys := make([]string, 0, len(forwardKeys)+1)
	keys = append(keys, forwardKeys...)
	return append(keys, toggleKey)
}

// InstallQuickNav captures any existing bindings on our keys, then binds the
// alt layer targeting muxPane. No-op outside tmux or with an empty pane id.
func InstallQuickNav(muxPane string) {
	if OuterSocket() == "" || muxPane == "" {
		return
	}
	if !savedNavBindings.captured {
		if out, err := tmux("list-keys", "-T", "root").Output(); err == nil {
			ours := map[string]bool{}
			for _, k := range quickNavKeys() {
				ours[k] = true
			}
			for _, line := range strings.Split(string(out), "\n") {
				// list-keys lines: bind-key [-r] -T root <key> <command...>
				f := strings.Fields(line)
				for i, tok := range f {
					if tok == "root" && i+1 < len(f) && ours[f[i+1]] {
						savedNavBindings.lines = append(savedNavBindings.lines, line)
						break
					}
				}
			}
		}
		savedNavBindings.captured = true
	}
	for _, key := range forwardKeys {
		_ = tmux("bind-key", "-n", key, "send-keys", "-t", muxPane, key).Run()
	}
	// two panes per window (mux + agent), so cycling is a toggle.
	_ = tmux("bind-key", "-n", toggleKey, "select-pane", "-t", ":.+").Run()
}

// RemoveQuickNav unbinds the alt layer and re-sources any bindings that were
// on our keys before mux started. Call from exit cleanup.
func RemoveQuickNav() {
	if OuterSocket() == "" {
		return
	}
	for _, key := range quickNavKeys() {
		_ = tmux("unbind-key", "-n", key).Run()
	}
	if len(savedNavBindings.lines) > 0 {
		// list-keys output is valid config syntax — round-trip via source-file.
		f := filepath.Join(os.TempDir(), "mux-navrestore.conf")
		if err := os.WriteFile(f, []byte(strings.Join(savedNavBindings.lines, "\n")+"\n"), 0o600); err == nil {
			_ = tmux("source-file", f).Run()
			_ = os.Remove(f)
		}
	}
}
