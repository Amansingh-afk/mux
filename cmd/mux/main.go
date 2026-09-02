package main

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amansingh-afk/mux/internal/config"
	"github.com/Amansingh-afk/mux/internal/session"
	"github.com/Amansingh-afk/mux/internal/state"
	"github.com/Amansingh-afk/mux/internal/tui"
)

func main() {
	// handler modes: the providers call mux back (claude hooks / codex
	// notify). Dispatch before anything TUI- or tmux-related; handlers must
	// stay silent and exit 0 so they never break the host agent.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--claude-hook":
			runClaudeHook(os.Stdin)
			return
		case "--notify-handler":
			runNotifyHandler(os.Args[2:])
			return
		}
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mux:", err)
		os.Exit(1)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintln(os.Stderr, "mux: warning:", w)
	}
	session.ApplyConfig(cfg)
	session.SetNativeStatus(cfg.NativeStatus)
	tui.SetDiscoveryRoots(cfg.DiscoveryRoots)
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Fprintln(os.Stderr, "mux: tmux not found in PATH")
		fmt.Fprintln(os.Stderr, "      install tmux >= 3.2 and re-run.")
		os.Exit(1)
	}
	if session.OuterSocket() == "" {
		// not inside tmux. auto-wrap so the user lands in a tmux session
		// running mux, with $TMUX set for the next invocation.
		fmt.Fprintln(os.Stderr, "mux: launching inside tmux...")
		self, err := os.Executable()
		if err != nil {
			self = "mux"
		}
		cmd := exec.Command("tmux", "new-session", "-A", "-s", "mux", self)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "mux: failed to launch tmux:", err)
			fmt.Fprintln(os.Stderr, "      run manually: tmux new -s mux 'mux'")
			os.Exit(1)
		}
		return
	}
	store, err := state.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mux: state:", err)
		os.Exit(1)
	}
	aliveSeed := map[string]bool{}
	if ids, err := session.List(); err == nil {
		for _, id := range ids {
			aliveSeed[id] = true
		}
		store.Reconcile(aliveSeed)
		_ = store.Save()
	}
	// snapshot user's current tmux status-format[0] so we can put it back on
	// exit. mux owns the top tab bar while running.
	session.CaptureTabBar()
	session.SetStatusPositionTop()
	session.SetTopBarLayout()
	session.CapturePaneBorder()
	session.SetPaneBorder()
	if cfg.QuickNav {
		session.InstallQuickNav(session.OuterPane())
	}
	cleanup := func() {
		session.RestoreTabBar()
		session.RestorePaneBorder()
		if cfg.QuickNav {
			session.RemoveQuickNav()
		}
	}
	p := tea.NewProgram(tui.New(store, aliveSeed), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		cleanup()
		fmt.Fprintln(os.Stderr, "mux:", err)
		os.Exit(1)
	}
	cleanup()
}
