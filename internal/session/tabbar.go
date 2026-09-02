package session

import (
	"fmt"
	"strings"
)

// tabbar.go manages the outer tmux status line so mux can render its
// project tab strip in a full-width line above (or below) the panes. We
// modify status-format[0] only, scoped to the current window, and revert on
// exit so the user's normal status returns when mux quits.
//
// scope choice: `-w` (window-only) — surgical, doesn't touch other windows
// in the user's tmux session. status-position and status itself stay at
// whatever the user already had configured.

// savedFmt0 caches the value of status-format[0] in effect when mux started,
// so RestoreTabBar can put it back on exit. Captured once via
// CaptureTabBar and never overwritten.
var savedFmt0 = struct {
	captured bool
	value    string
}{}

// savedPos caches status-position (session option) so RestoreTabBar can
// put it back. mux pins the bar to top while running.
var savedPos = struct {
	captured bool
	value    string
}{}

// savedLines caches `status` (line count) so RestoreTabBar can put it
// back. mux uses 2 lines (tabs + bottom border) while running.
var savedLines = struct {
	captured bool
	value    string
}{}

// savedFmt1 caches status-format[1]; mux owns line 1 for the bottom border
// while running.
var savedFmt1 = struct {
	captured bool
	value    string
}{}

// CaptureTabBar reads the current status-format[0] for the window mux
// runs in and the session's status-position. Call once at startup, before
// SetTopTabs touches anything. Safe to call when not in tmux — no-op.
func CaptureTabBar() {
	if OuterSocket() == "" {
		return
	}
	if !savedFmt0.captured {
		out, err := tmux("show-options", "-vw", "status-format[0]").Output()
		if err != nil {
			// option may simply not be set on the window; treat as empty so
			// Restore unsets it cleanly.
			savedFmt0.value = ""
		} else {
			savedFmt0.value = strings.TrimRight(string(out), "\n")
		}
		savedFmt0.captured = true
	}
	if !savedPos.captured {
		out, err := tmux("show-options", "-v", "status-position").Output()
		if err != nil {
			savedPos.value = ""
		} else {
			savedPos.value = strings.TrimRight(string(out), "\n")
		}
		savedPos.captured = true
	}
	if !savedLines.captured {
		out, err := tmux("show-options", "-vw", "status").Output()
		if err != nil {
			savedLines.value = ""
		} else {
			savedLines.value = strings.TrimRight(string(out), "\n")
		}
		savedLines.captured = true
	}
	if !savedFmt1.captured {
		out, err := tmux("show-options", "-vw", "status-format[1]").Output()
		if err != nil {
			savedFmt1.value = ""
		} else {
			savedFmt1.value = strings.TrimRight(string(out), "\n")
		}
		savedFmt1.captured = true
	}
}

// SetStatusPositionTop pins the tmux status bar to the top of the window.
// Call once at startup after CaptureTabBar. RestoreTabBar reverts.
func SetStatusPositionTop() {
	if OuterSocket() == "" {
		return
	}
	_ = tmux("set-option", "status-position", "top").Run()
}

// SetTopBarLayout configures a 2-line status bar: line 0 holds the tab
// strip (set by SetTopTabs), line 1 is a horizontal rule that visually
// separates tabs from the panes below. Call once at startup. Window-scoped
// so it survives any per-tick set-option calls also at window scope.
func SetTopBarLayout() {
	if OuterSocket() == "" {
		return
	}
	_ = tmux("set-option", "-w", "status", "2").Run()
	rule := strings.Repeat("─", 500)
	_ = tmux("set-option", "-w", "status-format[1]", "#[fg=colour240]"+rule+"#[default]").Run()
}

// SetTopTabs writes a tmux-format string to status-format[0] of the current
// window. Caller is responsible for content — this fn is just the plumbing.
// Visibility + line count are owned by SetTopBarLayout (one-shot startup).
func SetTopTabs(content string) {
	if OuterSocket() == "" {
		return
	}
	_ = tmux("set-option", "-w", "status-format[0]", content).Run()
}

// RestoreTabBar puts back status-format[0] and status-position to the
// values in effect at startup. If an option wasn't set originally, it's
// unset so the higher scope's value shows. Call from exit cleanup.
func RestoreTabBar() {
	if OuterSocket() == "" {
		return
	}
	if savedFmt0.captured {
		if savedFmt0.value == "" {
			// no per-window override originally — unset so session-level shows.
			_ = tmux("set-option", "-uw", "status-format[0]").Run()
		} else {
			_ = tmux("set-option", "-w", "status-format[0]", savedFmt0.value).Run()
		}
	}
	if savedPos.captured {
		if savedPos.value == "" {
			_ = tmux("set-option", "-u", "status-position").Run()
		} else {
			_ = tmux("set-option", "status-position", savedPos.value).Run()
		}
	}
	if savedFmt1.captured {
		if savedFmt1.value == "" {
			_ = tmux("set-option", "-uw", "status-format[1]").Run()
		} else {
			_ = tmux("set-option", "-w", "status-format[1]", savedFmt1.value).Run()
		}
	}
	if savedLines.captured {
		if savedLines.value == "" {
			_ = tmux("set-option", "-uw", "status").Run()
		} else {
			_ = tmux("set-option", "-w", "status", savedLines.value).Run()
		}
	}
}

// FormatTab renders one project tab in tmux's status-format DSL. active
// tabs get accent color + bold; inactive tabs get the default fg.
func FormatTab(label string, active bool) string {
	if active {
		return fmt.Sprintf("#[fg=colour205,bold] %s #[default]", escapeTmuxFmt(label))
	}
	return fmt.Sprintf("#[fg=colour244] %s #[default]", escapeTmuxFmt(label))
}

// escapeTmuxFmt escapes characters that have meaning in tmux's format DSL.
// `#` introduces directives, so it must be doubled.
func escapeTmuxFmt(s string) string {
	return strings.ReplaceAll(s, "#", "##")
}
