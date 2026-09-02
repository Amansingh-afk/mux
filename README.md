# mux

a clean tmux-backed TUI for running and managing CLI coding agents across projects.

one window, one list of tabs, one sidebar of agents per tab. spawn claude / codex / gemini / cursor in any repo, jump between them like browser tabs, attach when you want to drive, detach to let them cook.

built because i was tired of hand-rolling tmux layouts every time i wanted three agents on the same repo. superset and t3 are nice but they're GUIs; agent-deck is a TUI but does too much. this does one thing.

## install

```
go install github.com/Amansingh-afk/mux/cmd/mux@latest
```

requires `tmux >= 3.2` on PATH. that's it.

then:

```
mux
```

## the idea

- **tabs** = projects (one tab per open repo), rendered in tmux's own status line
- **sidebar** = agents in the current project (mux's pane, left)
- **agent pane** = the selected agent's real tmux pane, right — not a preview, the actual session
- **enter** = focus the agent and type; `M-Space` (alt+space) bounces you back

mux doesn't render another TUI inside your TUI. it splits a real tmux pane next to itself and steps out of the way when you want to talk to an agent.

## quick start

1. `mux` (wraps itself in tmux if you're not in one)
2. press `o`, fuzzy-pick a repo (discovers git repos under `~`, `~/code`, `~/src`, cwd, etc.)
3. press `n`, pick a provider — the agent appears in the right pane
4. press `enter` to focus it and type; `M-Space` to hop back
5. `M-[` / `M-]` or `M-1–9` to jump tabs, `M-j` / `M-k` to switch agents — from anywhere

## keys

### projects
| key        | action                              |
| ---------- | ----------------------------------- |
| `o`        | open project (fuzzy picker)         |
| `w`        | close tab (agents keep running)     |
| `W`        | close tab + kill all its agents     |
| `[` / `]`  | prev / next tab                     |
| `h` / `l`  | prev / next tab                     |
| `1 – 9`    | jump to tab N                       |

### agents
| key        | action                              |
| ---------- | ----------------------------------- |
| `n`        | spawn agent in current project      |
| `enter`    | attach to selected agent            |
| `d`        | kill agent (again to forget)        |
| `r`        | rename selected agent               |
| `i`        | adopt an existing provider session  |
| `j` / `k`  | move agent cursor                   |
| `z`        | zen mode (zoom agent fullscreen)    |

### anywhere — even while typing in an agent
| key           | action                           |
| ------------- | -------------------------------- |
| `M-j` / `M-k` | next / prev agent                |
| `M-1 – M-9`   | jump to tab N                    |
| `M-[` / `M-]` | prev / next tab                  |
| `M-space`     | toggle focus mux ↔ agent         |

the alt layer is a set of prefixless tmux bindings mux installs at startup and removes on quit (your original bindings on those keys are restored). switching agents or tabs from inside a chat is one keystroke — no `C-b` needed. disable with `quick_nav = false` in the config.

### misc
| key        | action                              |
| ---------- | ----------------------------------- |
| `?`        | help                                |
| `q` / `C-c`| quit                                |

## providers

out of the box:

| provider | command        | install                                     |
| -------- | -------------- | ------------------------------------------- |
| claude   | `claude`       | `npm i -g @anthropic-ai/claude-code`        |
| codex    | `codex`        | `npm i -g @openai/codex`                    |
| gemini   | `gemini`       | `npm i -g @google/gemini-cli`               |
| cursor   | `cursor-agent` | —                                           |
| shell    | —              | —                                           |

missing binaries are dimmed in the spawn picker with an install hint. no silent failures.

## agent status

each agent shows a live status icon:

- animated spinner — active (generating, streaming, thinking)
- `◐` amber — waiting for your input
- `○` dim — idle
- `✕` red — dead (tmux session gone)

detection uses provider-native signals where possible, pane heuristics otherwise:

- **claude** — mux injects a `--settings` file at spawn that registers lightweight hooks (`Stop`, `Notification`, `UserPromptSubmit`, …) writing a one-line event file mux reads on its 2s tick. exact, no guessing. additive: your own claude hooks keep working.
- **codex** — mux injects `-c notify=[...]` pointing at itself (`mux --notify-handler`); turn-complete events land in the same event file.
- **both** — transcript jsonl mtime freshness as a passive activity signal.
- **everything else** (gemini / cursor / shell, or `native_status = false`) — content heuristics: hash the pane every 2s; changed hash → active, stable + prompt-shaped tail → waiting, stable + no prompt → idle.

native signals win when fresh; pane motion overrides a stale native signal; with nothing native, behavior is exactly the heuristic path. turn off all injection with `native_status = false` in the config file — spawns are then byte-identical to plain `claude` / `codex`.

## resume & adopt

agents survive reboots. a dead agent (tmux gone) stays in the sidebar as `✕` — press `enter` and mux respawns it with the provider's resume flag and the captured session uuid (`claude --resume <uuid>`, `codex resume <uuid>`, …). no uuid known → the provider's own session picker opens instead.

press `i` to adopt: mux lists provider-native sessions recorded for the current repo (`~/.claude/projects/<slug>/`, `~/.codex/sessions/`) that it isn't tracking yet — newest first, with a first-message preview. adopting adds the session as a dead agent; `enter` resumes it. read-only: mux never touches provider files.

## config

optional, at `~/.config/mux/config.toml` (honours `$XDG_CONFIG_HOME`):

```toml
[general]
discovery_roots = ["~/work", "~/oss"]  # extra roots for the repo picker
native_status = true                    # provider-native status signals (default true)
quick_nav = true                        # prefixless alt-key layer (default true)

[providers.claude]                      # tweak a builtin
args = ["--model", "opus"]              # appended, also on resume

[providers.aider]                       # add your own
cmd  = "aider"
args = ["--watch-files"]
hint = "pip install aider-chat"
color = "39"
```

missing file = defaults. malformed file = loud error at startup. unknown keys = warning, not failure.

## codenames

agents are auto-named from a pool of ~200 aesthetic single words: celestial, mythology (greek / norse / hindi), nature, minerals, french, spanish, cartoon characters (shaktiman, bheem, motu, patlu). rename any time with `r`.

## hooks

mux can shell out to user-defined scripts on specific events. drop an executable file under `~/.config/mux/hooks/<event>.sh` (honours `$XDG_CONFIG_HOME`). any language — it's just an exec. no hook present = no change, built-in behavior runs.

### events

| event | when | stdout | blocking |
| ---------- | ----------------------------------------- | ------------------- | ------------ |
| `on_waiting` | agent flips `active` → `waiting`        | ignored             | fire-and-forget, 5s cap, 30s/agent cooldown |

### context

scripts receive a JSON payload on stdin and a set of env vars for quick access:

```
MUX_EVENT              on_waiting
MUX_AGENT_ID           tmux session id (on_waiting)
MUX_AGENT_NAME         codename (on_waiting)
MUX_AGENT_PROVIDER     claude | codex | gemini | cursor | shell (on_waiting)
MUX_PROJECT_NAME       project display name
MUX_PROJECT_PATH       absolute repo path
MUX_STATUS_PREV        previous status (on_waiting)
MUX_STATUS_NOW         new status (on_waiting)
```

### example — notification

`~/.config/mux/hooks/on_waiting.sh`:

```bash
#!/bin/bash
notify-send -a mux -u normal -i utilities-terminal \
    "mux · $MUX_AGENT_NAME waiting" \
    "$MUX_AGENT_PROVIDER in $MUX_PROJECT_NAME"
```

needs a notification daemon running (mako / dunst / swaync / gnome-shell). `chmod +x` the file.

### caveats

- shebang must be at byte 0 — no leading whitespace.
- script must be executable (`chmod +x`).
- `on_waiting` has a 30s per-agent cooldown to dampen flap storms.
- a script that times out is silently dropped.

## architecture

```
cmd/mux               entrypoint + provider callback handlers (--claude-hook, --notify-handler)
internal/tui          bubble tea models (sidebar, overlays, pane control)
internal/session      tmux wrapper + provider registry + status (native + classifier) + resume/discovery
internal/state        ~/.config/mux/state.json
internal/discover     repo scanner (git dir walker)
internal/config       ~/.config/mux/config.toml
internal/hooks        user event hooks (~/.config/mux/hooks/)
```

stack: [Bubble Tea](https://github.com/charmbracelet/bubbletea) + [Lipgloss](https://github.com/charmbracelet/lipgloss) + [sahilm/fuzzy](https://github.com/sahilm/fuzzy) + shell-out to tmux.

tmux because reinventing a terminal emulator inside bubble tea is a bad time. tmux handles PTYs, scrollback, resize, detach / reattach; mux is a nice frontend over it.

## roadmap

short-term
- **pin / archive projects** — pin favorites to top of picker, archive stale ones out of the way
- **native status for gemini / cursor** — session-log layouts tbd

medium-term
- **SSH tabs** — open a project over ssh; tmux session runs on remote host, local body previews it
- **logs / scrollback view** — read-only scrollable history without full attach
- **split preview** — two agents side-by-side in body

polish
- themes
- keybinding config
- status-line messages for agent events (errors, token limits, etc.)

## why not X

- **tmux alone** — works, but hand-rolling layouts for every new repo is friction. mux is one keystroke to open a repo, another to spawn an agent.
- **agent-deck** — capable but does a lot. mux is deliberately narrow: a tab bar, a list, a preview, attach on demand.
- **superset / t3.code** — GUI. i live in the terminal.

## license

MIT.
