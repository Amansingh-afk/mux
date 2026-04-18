package main

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ashmit/mux/internal/session"
	"github.com/ashmit/mux/internal/state"
	"github.com/ashmit/mux/internal/tui"
)

func main() {
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Fprintln(os.Stderr, "mux: tmux not found in PATH")
		os.Exit(1)
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
	p := tea.NewProgram(tui.New(store, aliveSeed), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "mux:", err)
		os.Exit(1)
	}
}
