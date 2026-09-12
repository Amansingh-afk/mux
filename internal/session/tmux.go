package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const socketName = "mux"

type Agent struct {
	ID       string
	Project  string
	Provider string
	Path     string
	PID      int
}

// OuterSocket parses $TMUX (format: "<socket-path>,<server-pid>,<session-id>")
// to discover the tmux daemon mux is running inside. Returns "" if not in tmux,
// in which case the legacy `-L mux` private socket is used.
func OuterSocket() string {
	t := os.Getenv("TMUX")
	if t == "" {
		return ""
	}
	parts := strings.SplitN(t, ",", 2)
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}

// OuterPane returns the tmux pane id mux itself is running in (e.g. "%12"),
// or "" if not running inside tmux.
func OuterPane() string {
	return os.Getenv("TMUX_PANE")
}

// tmux builds an exec.Cmd against the right socket. When mux runs inside an
// outer tmux session, all commands target that daemon (-S <path>). Otherwise
// the legacy private "-L mux" socket is used, preserving non-tmux startup.
func tmux(args ...string) *exec.Cmd {
	var base []string
	if sock := OuterSocket(); sock != "" {
		base = []string{"-S", sock}
	} else {
		base = []string{"-L", socketName}
	}
	return exec.Command("tmux", append(base, args...)...)
}

func SessionID(project, provider string, n int) string {
	return fmt.Sprintf("mux_%s_%s_%d", sanitize(project), provider, n)
}

// sanitize produces a tmux-safe session name fragment: whitelist [A-Za-z0-9_],
// everything else → '_'. tmux forbids a few chars (colons, dots, periods are
// ambiguous in target syntax); blacklist approach missed punctuation like '-',
// '(', parentheses, commas, shell-meta.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// ResumeOpts controls whether Spawn launches the provider in resume mode.
// If UUID is set, SessionArg is appended to ResumeFlag (e.g. --resume <uuid>).
// If UUID is empty, only ResumeFlag is passed and the provider typically
// shows its own session picker.
type ResumeOpts struct {
	Enabled bool
	UUID    string
	// Setup is a shell command run in the session's dir before the agent
	// starts (worktree deps install). Runs visibly in the agent pane via a
	// `sh -c 'setup && exec "$@"'` wrapper. "" = direct exec, no wrapper.
	Setup string
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
	// fresh session, drop any stale resize-cache entry from a prior instance.
	ForgetResize(id)
	args := []string{
		"new-session", "-d", "-s", id, "-c", dir,
		"-x", fmt.Sprintf("%d", w), "-y", fmt.Sprintf("%d", h),
	}
	// native-status injection: per-spawn Provider copy with hook/notify
	// flags plus MUX_AGENT_ID in the session env (tmux >= 3.2 `-e`). When
	// native status is off this is a no-op and the spawn is byte-identical.
	p, envArgs := injectNative(id, p)
	if len(envArgs) > 0 {
		args = append(args, envArgs...)
		// drop stale spool state from a prior incarnation of this id so the
		// new session starts with a clean native-signal slate.
		ClearSpool(id)
	}
	argv := buildSpawnArgv(p, r)
	// worktree setup wrapper: run the configured setup command in the fresh
	// worktree, visibly inside the agent's own pane, then exec the agent.
	// `exec "$@"` keeps the agent's argv quoting intact and replaces the
	// shell, so the tmux session IS the agent process once setup is done.
	if r.Setup != "" && len(argv) > 0 {
		argv = append([]string{"sh", "-c", r.Setup + ` && exec "$@"`, "sh"}, argv...)
	}
	args = append(args, argv...)
	out, err := tmux(args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux spawn: %w: %s", err, out)
	}
	// window-size=manual: tmux never auto-resizes on attach. mux owns dims
	// via Resize / resizeAllSessions. Avoids the spurious SIGWINCH on every
	// SwitchClient that causes a visible re-render flicker.
	_ = tmux("set-option", "-t", id, "window-size", "manual").Run()
	return nil
}

// buildSpawnArgv assembles the command argv launched inside the tmux
// session: Cmd, PreArgs, [resume: ResumeFlag, (uuid via SessionArg)], Args.
// PreArgs are emitted in both fresh and resume spawns; Args always trail
// (previously the resume branch dropped them). Returns nil for the shell
// provider (empty Cmd) so tmux falls back to the default shell.
func buildSpawnArgv(p Provider, r ResumeOpts) []string {
	if p.Cmd == "" {
		return nil
	}
	argv := make([]string, 0, 1+len(p.PreArgs)+len(p.ResumeFlag)+2+len(p.Args))
	argv = append(argv, p.Cmd)
	argv = append(argv, p.PreArgs...)
	if r.Enabled && len(p.ResumeFlag) > 0 {
		argv = append(argv, p.ResumeFlag...)
		if r.UUID != "" && p.SessionArg != "" {
			// SessionArg may overlap with ResumeFlag (e.g. claude's
			// "--resume" is both); in that case append the uuid as the
			// value rather than re-emitting the flag.
			if containsString(p.ResumeFlag, p.SessionArg) {
				argv = append(argv, r.UUID)
			} else {
				argv = append(argv, p.SessionArg, r.UUID)
			}
		}
	}
	argv = append(argv, p.Args...)
	return argv
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// resizeCache tracks last applied dims per session so Resize becomes a no-op
// when nothing changed. Avoids a tmux fork per tick per agent.
var (
	resizeCacheMu sync.Mutex
	resizeCache   = map[string][2]int{}
)

func Resize(id string, w, h int) error {
	if w < 20 || h < 5 {
		return nil
	}
	resizeCacheMu.Lock()
	last, ok := resizeCache[id]
	resizeCacheMu.Unlock()
	if ok && last[0] == w && last[1] == h {
		return nil
	}
	if err := tmux("resize-window", "-t", id, "-x", fmt.Sprintf("%d", w), "-y", fmt.Sprintf("%d", h)).Run(); err != nil {
		return err
	}
	resizeCacheMu.Lock()
	resizeCache[id] = [2]int{w, h}
	resizeCacheMu.Unlock()
	return nil
}

// ForgetResize drops the cache entry for id. Call after Kill/respawn so the
// next Resize actually runs tmux.
func ForgetResize(id string) {
	resizeCacheMu.Lock()
	delete(resizeCache, id)
	resizeCacheMu.Unlock()
}

// SetSizeManual locks a session's window-size to manual so it never
// auto-resizes on client attach. Called after respawn/resume because
// tmux can reset the option in some flows. Idempotent — Spawn also sets it.
func SetSizeManual(id string) {
	_ = tmux("set-option", "-t", id, "window-size", "manual").Run()
}

// NudgeResize forces a SIGWINCH on the agent process by transiently resizing
// the session to (w-1, h) then back to (w, h). Plain Resize is a no-op when
// cached dims already match, which leaves TUIs (claude/codex) frozen on
// their initial loading buffer. Bounce guarantees a real resize event.
func NudgeResize(id string, w, h int) {
	if w < 21 || h < 6 {
		return
	}
	_ = tmux("resize-window", "-t", id, "-x", fmt.Sprintf("%d", w-1), "-y", fmt.Sprintf("%d", h)).Run()
	_ = tmux("resize-window", "-t", id, "-x", fmt.Sprintf("%d", w), "-y", fmt.Sprintf("%d", h)).Run()
	resizeCacheMu.Lock()
	resizeCache[id] = [2]int{w, h}
	resizeCacheMu.Unlock()
}

func Kill(id string) error {
	ForgetResize(id)
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

// Capture returns the current visible pane content with ANSI preserved.
// `lines` is the caller's hint for how many rows they'll render; tmux returns
// the whole visible pane regardless, we trim at the caller. Scrollback is
// deliberately excluded — TUI agents (claude, codex) push transcript into
// scrollback on detach and re-render on attach, which would double-print
// in the body preview if we captured it.
//
// Bounded by a 1s timeout so a dead tmux socket can't wedge the status tick.
// RunTransient starts a detached session running shellCmd in dir. When the
// command exits (pager quit, shell exit), the session explicitly switches the
// viewing client back to returnTo before dying. NEVER rely on tmux's
// detach-on-destroy hop here: "most recent session" can be mux's own UI
// session, and a nested client attaching to it recursively clamps the whole
// window to the pane's size (the 1x2 collapse). Used for diff views and
// conflict shells. An existing session with the same id is replaced.
func RunTransient(id, dir, shellCmd, returnTo string) error {
	_ = tmux("kill-session", "-t", id).Run()
	if returnTo != "" {
		shellCmd = fmt.Sprintf("{ %s; }; tmux switch-client -t %q", shellCmd, returnTo)
	}
	out, err := tmux("new-session", "-d", "-s", id, "-c", dir, "sh", "-c", shellCmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux transient: %w: %s", err, out)
	}
	_ = tmux("set-option", "-t", id, "window-size", "manual").Run()
	return nil
}

// CapturePlain captures the pane's content as plain text (no ANSI), including
// the last `history` lines of scrollback. Used by yank — pasted prompts must
// not carry escape sequences.
func CapturePlain(id string, history int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	args := []string{"capture-pane", "-p", "-t", id, "-S", fmt.Sprintf("-%d", history)}
	var base []string
	if sock := OuterSocket(); sock != "" {
		base = []string{"-S", sock}
	} else {
		base = []string{"-L", socketName}
	}
	out, err := exec.CommandContext(ctx, "tmux", append(base, args...)...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// PasteText bracketed-pastes text into the session's input. load-buffer +
// paste-buffer -p keeps multi-line text as a single pasted block: TUIs that
// support bracketed paste (claude/codex/gemini) receive it without treating
// embedded newlines as submits. The buffer is deleted after the paste (-d).
func PasteText(id, text string) error {
	load := tmux("load-buffer", "-b", "muxyank", "-")
	load.Stdin = strings.NewReader(text)
	if err := load.Run(); err != nil {
		return err
	}
	return tmux("paste-buffer", "-p", "-d", "-b", "muxyank", "-t", id).Run()
}

func Capture(id string, lines int) (string, error) {
	_ = lines
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	args := []string{"capture-pane", "-p", "-t", id, "-e"}
	var base []string
	if sock := OuterSocket(); sock != "" {
		base = []string{"-S", sock}
	} else {
		base = []string{"-L", socketName}
	}
	cmd := exec.CommandContext(ctx, "tmux", append(base, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SplitRight creates a horizontal split to the right of targetPane and
// attaches the agent session inside it. `muxCols` is the width (cols) the
// caller wants the LEFT pane (mux) to keep; the new agent pane gets the
// remainder. Falls back to a 50/50 split if the current pane width can't
// be measured or is too narrow to spare muxCols.
//
// `TMUX=` in attachShell unsets the inherited tmux client env so the nested
// attach doesn't trip tmux's "sessions should be nested with care" warning.
func SplitRight(targetPane, agentSession string, muxCols int) (string, error) {
	args := []string{
		"split-window", "-h", "-d", "-t", targetPane,
		"-P", "-F", "#{pane_id}",
	}
	if paneW, err := paneWidth(targetPane); err == nil && paneW > muxCols+10 {
		agentW := paneW - muxCols
		args = append(args, "-l", fmt.Sprintf("%d", agentW))
	}
	args = append(args, attachShell(agentSession))
	out, err := tmux(args...).Output()
	if err != nil {
		return "", fmt.Errorf("tmux split-window: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// paneWidth returns the column width of the given tmux pane.
func paneWidth(pane string) (int, error) {
	out, err := tmux("display-message", "-p", "-t", pane, "#{pane_width}").Output()
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, err
	}
	return n, nil
}

// PaneDims returns (cols, rows) of the given pane.
func PaneDims(pane string) (int, int, error) {
	out, err := tmux("display-message", "-p", "-t", pane, "#{pane_width} #{pane_height}").Output()
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("paneDims: unexpected output %q", string(out))
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return w, h, nil
}

// RespawnPane kills whatever's running in pane and replaces it with an attach
// to agentSession. Used when swapping the right-side display from one agent
// to another. Idempotent — if pane no longer exists, returns an error.
func RespawnPane(pane, agentSession string) error {
	out, err := tmux("respawn-pane", "-k", "-t", pane, attachShell(agentSession)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux respawn-pane: %w: %s", err, out)
	}
	return nil
}

// attachShell returns the shell command run inside a fresh tmux pane to
// attach to the given agent session. TMUX= unsets the nesting guard.
func attachShell(agentSession string) string {
	sock := OuterSocket()
	if sock == "" {
		return fmt.Sprintf("tmux -L %s attach-session -t %s", socketName, agentSession)
	}
	return fmt.Sprintf("TMUX= tmux -S %s attach-session -t %s", sock, agentSession)
}

// KillPane kills the given pane id. Safe to call on a pane that no longer
// exists — error is swallowed.
func KillPane(pane string) {
	_ = tmux("kill-pane", "-t", pane).Run()
}

// PaneExists reports whether a tmux pane with the given id is alive.
func PaneExists(pane string) bool {
	if pane == "" {
		return false
	}
	return tmux("display-message", "-p", "-t", pane, "ok").Run() == nil
}

// SelectPane focuses the given pane (moves the cursor / input focus there).
func SelectPane(pane string) error {
	return tmux("select-pane", "-t", pane).Run()
}

// SetPaneZoom sets the pane's zoom state to want. Queries tmux's
// window_zoomed_flag first so calls are idempotent — repeated invocations
// with the same target state are no-ops, avoiding the visible toggle a
// blind resize-pane -Z would cause.
func SetPaneZoom(pane string, want bool) {
	if pane == "" {
		return
	}
	out, err := tmux("display-message", "-p", "-t", pane, "#{window_zoomed_flag}").Output()
	if err != nil {
		return
	}
	cur := strings.TrimSpace(string(out)) == "1"
	if cur == want {
		return
	}
	_ = tmux("resize-pane", "-Z", "-t", pane).Run()
}

// ZoomPane toggles tmux's zoom on the given pane. Selects the pane first
// so resize-pane -Z (which acts on the active pane) targets the right one.
func ZoomPane(pane string) error {
	if err := tmux("select-pane", "-t", pane).Run(); err != nil {
		return fmt.Errorf("select-pane: %w", err)
	}
	out, err := tmux("resize-pane", "-Z", "-t", pane).CombinedOutput()
	if err != nil {
		return fmt.Errorf("resize-pane -Z: %w: %s", err, out)
	}
	return nil
}

// FindClientForSession returns the tmux client name (tty path) of the most
// recently active client attached to the given session. "" if no client.
// Used to track the nested tmux client mux launched in the right pane so
// we can switch its viewed session without respawning the pane.
func FindClientForSession(sess string) string {
	out, err := tmux("list-clients", "-F", "#{client_name}|#{client_activity}", "-t", sess).Output()
	if err != nil {
		return ""
	}
	var best string
	var bestT int64 = -1
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		t, _ := strconv.ParseInt(parts[1], 10, 64)
		if t > bestT {
			best = parts[0]
			bestT = t
		}
	}
	return best
}

// SwitchClient changes which session a tmux client views. The client process
// keeps running — only the displayed session changes. Avoids the kill+attach
// flicker that respawn-pane causes.
func SwitchClient(client, newSession string) error {
	if client == "" {
		return fmt.Errorf("SwitchClient: empty client")
	}
	return tmux("switch-client", "-c", client, "-t", newSession).Run()
}
