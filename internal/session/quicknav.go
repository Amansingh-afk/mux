package session

import (
	"os"
	"path/filepath"
	"strings"
)

// quicknav.go installs a prefixless alt-key layer on the outer tmux server so
// mux stays drivable while keyboard focus is inside an agent pane:
//
//	M-j / M-k    next / prev agent   (forwarded to the mux pane)
//	M-1 .. M-9   jump project tab    (forwarded)
//	M-[ / M-]    prev / next tab     (forwarded)
//	M-Space      toggle focus between mux and the agent pane
//
// Forwarding works by binding each key to `send-keys -t <mux-pane> <key>`,
// so the keystroke lands in mux's Bubble Tea loop as if typed there — no new
// input plumbing, the existing handlers do the work.
//
// tmux key bindings are server-wide, so like the tab bar this is
// capture/set/restore: existing root-table bindings on our keys are saved at
// install (list-keys output is valid tmux config syntax) and sourced back on
// exit. On mux's auto-wrapped private server this is all moot; nested inside
// the user's own tmux it makes the takeover reversible. The quick_nav config
// flag disables the whole layer.

// forwardKeys are the keys mirrored into the mux pane, mapped to the literal
// key sent there.
var forwardKeys = map[string]string{
	"M-j": "j", "M-k": "k",
	"M-1": "1", "M-2": "2", "M-3": "3", "M-4": "4", "M-5": "5",
	"M-6": "6", "M-7": "7", "M-8": "8", "M-9": "9",
	"M-[": "[", "M-]": "]",
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
	for k := range forwardKeys {
		keys = append(keys, k)
	}
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
	for key, fwd := range forwardKeys {
		_ = tmux("bind-key", "-n", key, "send-keys", "-t", muxPane, fwd).Run()
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
