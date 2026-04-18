# mux

a clean tmux-backed TUI for running and managing CLI coding agents across projects.

one window, one list of tabs, one sidebar of agents per tab. spawn claude / codex / gemini / cursor in any repo, jump between them like browser tabs, attach when you want to drive, detach to let them cook.

built because i was tired of hand-rolling tmux layouts every time i wanted three agents on the same repo. superset and t3 are nice but they're GUIs; agent-deck is a TUI but does too much. this does one thing.

## install

```
go install github.com/ashmit/mux/cmd/mux@latest
```

requires `tmux >= 3.2` on PATH. that's it.

then:

```
mux
```

## the idea

- **tabs** = projects (one tab per open repo)
- **sidebar** = agents in the current project
- **body** = live preview of the selected agent's pane
- **enter** = hand off to the agent (full tmux attach, `C-b d` to come back)

mux doesn't render another TUI inside your TUI. it just schedules tmux sessions and steps out of the way when you want to talk to an agent.

## quick start

1. `mux`
2. press `o`, fuzzy-pick a repo (discovers git repos under `~`, `~/code`, `~/src`, cwd, etc.)
3. press `n`, pick a provider
4. press `enter` to attach, `C-b d` to detach
5. press `[` / `]` or `1–9` to jump tabs

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
| `d`        | kill selected agent                 |
| `r`        | rename selected agent               |
| `j` / `k`  | move agent cursor                   |

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

detection is content-based: we hash the last chunk of the pane every 2s. changed hash → active. stable + prompt-shaped tail → waiting. stable + no prompt → idle.

## codenames

agents are auto-named from a pool of ~200 aesthetic single words: celestial, mythology (greek / norse / hindi), nature, minerals, french, spanish, cartoon characters (shaktiman, bheem, motu, patlu). rename any time with `r`.

## architecture

```
cmd/mux               entrypoint
internal/tui          bubble tea models (tabs, sidebar, body, overlays)
internal/session      tmux wrapper + provider registry + state classifier
internal/state        ~/.config/mux/state.json
internal/discover     repo scanner (git dir walker)
```

stack: [Bubble Tea](https://github.com/charmbracelet/bubbletea) + [Lipgloss](https://github.com/charmbracelet/lipgloss) + [sahilm/fuzzy](https://github.com/sahilm/fuzzy) + shell-out to tmux.

tmux because reinventing a terminal emulator inside bubble tea is a bad time. tmux handles PTYs, scrollback, resize, detach / reattach; mux is a nice frontend over it.

## roadmap

short-term
- **resume session** — survive reboots. today tmux dies with the machine and state reconcile prunes agents. roadmap:
  - layer 1: mark-as-dead instead of prune; on attach, respawn with provider's `--resume` / `continue` flag and let the CLI show its own session picker
  - layer 2: capture the provider's native session UUID at spawn (watch `~/.claude/projects/<slug>/` for newest jsonl, etc.) and auto-resume by id
- **config file** (`~/.config/mux/config.toml`) — custom providers (aider, qwen), per-provider CLI args (`claude --model opus`), extra search roots for the repo picker
- **pin / archive projects** — pin favorites to top of picker, archive stale ones out of the way

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
