package session

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

const socketName = "mux"

type Agent struct {
	ID       string
	Project  string
	Provider string
	Path     string
	PID      int
}

func tmux(args ...string) *exec.Cmd {
	base := []string{"-L", socketName}
	return exec.Command("tmux", append(base, args...)...)
}

func SessionID(project, provider string, n int) string {
	return fmt.Sprintf("mux_%s_%s_%d", sanitize(project), provider, n)
}

func sanitize(s string) string {
	r := strings.NewReplacer("/", "_", ".", "_", " ", "_", ":", "_")
	return r.Replace(s)
}

// ResumeOpts controls whether Spawn launches the provider in resume mode.
// If UUID is set, SessionArg is appended to ResumeFlag (e.g. --resume <uuid>).
// If UUID is empty, only ResumeFlag is passed and the provider typically
// shows its own session picker.
type ResumeOpts struct {
	Enabled bool
	UUID    string
}

func Spawn(id, dir string, p Provider, w, h int) error {
	return SpawnWithResume(id, dir, p, w, h, ResumeOpts{})
}

func SpawnWithResume(id, dir string, p Provider, w, h int, r ResumeOpts) error {
	if w < 40 {
		w = 120
	}
	if h < 10 {
		h = 40
	}
	args := []string{
		"new-session", "-d", "-s", id, "-c", dir,
		"-x", fmt.Sprintf("%d", w), "-y", fmt.Sprintf("%d", h),
	}
	if p.Cmd != "" {
		args = append(args, p.Cmd)
		if r.Enabled && len(p.ResumeFlag) > 0 {
			args = append(args, p.ResumeFlag...)
			if r.UUID != "" && p.SessionArg != "" {
				// SessionArg may overlap with ResumeFlag (e.g. claude's
				// "--resume" is both); in that case append the uuid as the
				// value rather than re-emitting the flag.
				if containsString(p.ResumeFlag, p.SessionArg) {
					args = append(args, r.UUID)
				} else {
					args = append(args, p.SessionArg, r.UUID)
				}
			}
		} else {
			args = append(args, p.Args...)
		}
	}
	out, err := tmux(args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux spawn: %w: %s", err, out)
	}
	// window-size=latest: when a client attaches, session resizes to that
	// client; when no client, size stays at last value (we control via Resize).
	_ = tmux("set-option", "-t", id, "window-size", "latest").Run()
	return nil
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func Resize(id string, w, h int) error {
	if w < 20 || h < 5 {
		return nil
	}
	return tmux("resize-window", "-t", id, "-x", fmt.Sprintf("%d", w), "-y", fmt.Sprintf("%d", h)).Run()
}

func SetSizeLatest(id string) {
	_ = tmux("set-option", "-t", id, "window-size", "latest").Run()
}

func Kill(id string) error {
	return tmux("kill-session", "-t", id).Run()
}

func Exists(id string) bool {
	return tmux("has-session", "-t", id).Run() == nil
}

func List() ([]string, error) {
	out, err := tmux("list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.ExitStatus() == 1 {
				return nil, nil
			}
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var ids []string
	for _, l := range lines {
		if strings.HasPrefix(l, "mux_") {
			ids = append(ids, l)
		}
	}
	return ids, nil
}

func Capture(id string, lines int) (string, error) {
	start := fmt.Sprintf("-%d", lines)
	out, err := tmux("capture-pane", "-p", "-t", id, "-S", start, "-e").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func AttachCmd(id string) *exec.Cmd {
	return tmux("attach-session", "-t", id)
}
