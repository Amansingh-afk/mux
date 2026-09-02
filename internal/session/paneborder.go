package session

import "strings"

// paneborder.go styles the tmux pane split between mux and the agent pane so
// it matches mux's palette instead of tmux's default green. Window-scoped and
// capture/restore, same contract as the tab bar: user's values return on quit.

var savedBorder = struct {
	captured              bool
	style, active, lines  string
	haveStyle, haveActive bool
	haveLines             bool
}{}

func captureOptW(name string) (string, bool) {
	out, err := tmux("show-options", "-vw", name).Output()
	if err != nil {
		return "", false
	}
	v := strings.TrimRight(string(out), "\n")
	return v, v != ""
}

// CapturePaneBorder snapshots the window's pane border options. Call once at
// startup before SetPaneBorder. No-op outside tmux.
func CapturePaneBorder() {
	if OuterSocket() == "" || savedBorder.captured {
		return
	}
	savedBorder.style, savedBorder.haveStyle = captureOptW("pane-border-style")
	savedBorder.active, savedBorder.haveActive = captureOptW("pane-active-border-style")
	savedBorder.lines, savedBorder.haveLines = captureOptW("pane-border-lines")
	savedBorder.captured = true
}

// SetPaneBorder applies mux's border look: near-invisible dim split, accent
// (pink) on the focused pane so M-Space focus jumps read at a glance.
func SetPaneBorder() {
	if OuterSocket() == "" {
		return
	}
	_ = tmux("set-option", "-w", "pane-border-style", "fg=colour236").Run()
	_ = tmux("set-option", "-w", "pane-active-border-style", "fg=colour205").Run()
	_ = tmux("set-option", "-w", "pane-border-lines", "heavy").Run()
}

// RestorePaneBorder reverts the border options to their startup values,
// unsetting any that had no window-level override. Call from exit cleanup.
func RestorePaneBorder() {
	if OuterSocket() == "" || !savedBorder.captured {
		return
	}
	restore := func(name, val string, had bool) {
		if had {
			_ = tmux("set-option", "-w", name, val).Run()
		} else {
			_ = tmux("set-option", "-uw", name).Run()
		}
	}
	restore("pane-border-style", savedBorder.style, savedBorder.haveStyle)
	restore("pane-active-border-style", savedBorder.active, savedBorder.haveActive)
	restore("pane-border-lines", savedBorder.lines, savedBorder.haveLines)
}
