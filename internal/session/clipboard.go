package session

import "strings"

// clipboard.go lets mouse copies in the agent pane reach the system
// clipboard. The agent pane is a nested tmux client: a drag selects in the
// inner session, and the inner client forwards the copy as OSC52 to the
// outer pane. With set-clipboard=external (a common setting) the outer tmux
// drops OSC52 coming from panes, so the copy never leaves tmux. mux sets it
// to "on" while running and restores the user's value on quit.

var savedClipboard = struct {
	captured bool
	value    string
}{}

// CaptureClipboard snapshots the server's set-clipboard option. Call once at
// startup before SetClipboardOn. No-op outside tmux.
func CaptureClipboard() {
	if OuterSocket() == "" || savedClipboard.captured {
		return
	}
	out, err := tmux("show-options", "-vs", "set-clipboard").Output()
	if err != nil {
		return
	}
	savedClipboard.value = strings.TrimSpace(string(out))
	savedClipboard.captured = savedClipboard.value != ""
}

// SetClipboardOn lets applications in panes (the nested agent client) set
// the terminal clipboard via OSC52.
func SetClipboardOn() {
	if OuterSocket() == "" {
		return
	}
	_ = tmux("set-option", "-s", "set-clipboard", "on").Run()
}

// RestoreClipboard puts set-clipboard back to its startup value. Call from
// exit cleanup.
func RestoreClipboard() {
	if OuterSocket() == "" || !savedClipboard.captured {
		return
	}
	_ = tmux("set-option", "-s", "set-clipboard", savedClipboard.value).Run()
}
