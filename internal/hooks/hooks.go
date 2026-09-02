// Package hooks dispatches user-defined scripts in response to TUI events.
//
// Scripts live under ~/.config/mux/hooks/<event>.sh (honours XDG_CONFIG_HOME).
// A hook is a regular executable file — any language — that receives a JSON
// Context on stdin and a small set of MUX_* env vars. Notify hooks are
// fire-and-forget.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type AgentCtx struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

type ProjectCtx struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type StatusCtx struct {
	Prev string `json:"prev"`
	Now  string `json:"now"`
}

type Context struct {
	Event   string      `json:"event"`
	Agent   *AgentCtx   `json:"agent,omitempty"`
	Project *ProjectCtx `json:"project,omitempty"`
	Status  *StatusCtx  `json:"status,omitempty"`
}

func Dir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "mux", "hooks")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "mux", "hooks")
}

func scriptPath(event string) string {
	d := Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, event+".sh")
}

func exists(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

const notifyTimeout = 5 * time.Second

// Notify runs the named event hook fire-and-forget in a goroutine with a
// 5s timeout. Exit code and stdout are ignored.
func Notify(event string, c Context) {
	path := scriptPath(event)
	if !exists(path) {
		return
	}
	c.Event = event
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		defer cancel()
		_, _ = run(ctx, path, c)
	}()
}

func run(ctx context.Context, path string, c Context) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path)
	cmd.Stdin = bytes.NewReader(payload)
	env := append(os.Environ(), "MUX_EVENT="+c.Event)
	if c.Agent != nil {
		env = append(env,
			"MUX_AGENT_ID="+c.Agent.ID,
			"MUX_AGENT_NAME="+c.Agent.Name,
			"MUX_AGENT_PROVIDER="+c.Agent.Provider,
		)
	}
	if c.Project != nil {
		env = append(env,
			"MUX_PROJECT_NAME="+c.Project.Name,
			"MUX_PROJECT_PATH="+c.Project.Path,
		)
	}
	if c.Status != nil {
		env = append(env,
			"MUX_STATUS_PREV="+c.Status.Prev,
			"MUX_STATUS_NOW="+c.Status.Now,
		)
	}
	cmd.Env = env
	out, err := cmd.Output()
	return string(out), err
}
