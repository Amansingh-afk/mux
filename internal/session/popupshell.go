package session

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// popupshell.go backs M-s: a floating shell (tmux display-popup) that opens
// in the selected agent's directory — its worktree, so paths the agent
// mentions resolve as-is. Each popup attaches a nested client to a
// persistent "muxsh_" session, so hiding the popup keeps the shell, its
// history and any running command. The prefix is deliberately not "mux_":
// List() treats those as agents.

// ShellSessionID returns the popup shell session name for an agent (or
// project) key such as an agent ID.
func ShellSessionID(key string) string {
	return "muxsh_" + sanitize(strings.TrimPrefix(key, "mux_"))
}

// ToggleShellPopup shows the popup shell for key in dir, creating its
// session on first use. If the popup is already showing it is hidden
// instead: detaching its nested client ends the popup's command (-E closes
// it) while the shell session lives on. title labels the popup border.
func ToggleShellPopup(key, dir, title string) error {
	if OuterSocket() == "" {
		return fmt.Errorf("popup shell needs tmux")
	}
	sess := ShellSessionID(key)
	if Exists(sess) && FindClientForSession(sess) != "" {
		return tmux("detach-client", "-s", exact(sess)).Run()
	}
	if !Exists(sess) {
		if out, err := tmux("new-session", "-d", "-s", sess, "-c", dir).CombinedOutput(); err != nil {
			return fmt.Errorf("tmux new-session: %w: %s", err, out)
		}
		// the popup border already labels it; a nested status bar would
		// only repeat the session name.
		_ = tmux("set-option", "-t", sess, "status", "off").Run()
	}
	client := outerClient()
	if client == "" {
		return fmt.Errorf("no outer tmux client")
	}
	out, err := tmux("display-popup", "-c", client, "-E",
		"-w", "85%", "-h", "80%", "-d", dir,
		"-T", " "+title+" ", attachShell(sess)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux display-popup: %w: %s", err, out)
	}
	return nil
}

// KillShell removes key's popup shell session, if any.
func KillShell(key string) {
	sess := ShellSessionID(key)
	if Exists(sess) {
		_ = tmux("kill-session", "-t", exact(sess)).Run()
	}
}

// outerClient returns the tmux client viewing mux's own pane.
func outerClient() string {
	out, err := tmux("display-message", "-p", "-t", OuterPane(), "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return FindClientForSession(strings.TrimSpace(string(out)))
}

// OpenDir opens dir in the desktop file manager, detached from mux.
func OpenDir(dir string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	cmd := exec.Command(opener, dir)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
