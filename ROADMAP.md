# roadmap


living doc. ordered by pull, not by build order. stuff gets reshuffled as ideas survive contact with real use.

## north star

mux is an **agent orchestrator**, not a tmux skin. the tabs + sidebar is the chassis. the features below are what make it worth installing over raw tmux.

---

## the big one

### resume session

survive reboots. today tmux dies with the machine, reconcile prunes agents, conversations orphaned.

- **layer 1** — mark-as-dead instead of prune. on attach, respawn with provider's `--resume` / `continue` flag, let the CLI show its own session picker.
- **layer 2** — capture provider's native session UUID at spawn time (watch `~/.claude/projects/<slug>/` for newest jsonl, parse codex/gemini equivalents). auto-resume by id, no picker. serialize simultaneous spawns in same provider-dir so mtime ordering stays reliable.
- **layer 3 — adopt-on-demand** — user ran `claude` directly in terminal, never through mux. open the repo as a tab, press `i` → overlay lists provider-discovered sessions for that dir (newest first, first-message preview). pick one → mux adopts with codename + uuid, shows as `Dead: true` in sidebar, resumable via the same layer-2 path. orphan count hinted in status bar (`12 unimported conversations · press i to browse`). never touches provider files.

until layer 2 lands, mux is "tmux with tabs." after — real continuity. after layer 3 — unified view of every ai conversation per repo, curated by intent.

---

## tier S — real moat

### 1. cross-agent handoff / piping

agent A finishes → pipe last output as prompt to agent B.

- `y` yank last output of current agent
- `p` paste-as-prompt to another agent
- cli-style: `>claude` from inside codex tab forwards current buffer to claude agent

turns mux from viewer into orchestrator. nobody else does this cleanly.

### 2. shared context drop

drop file / dir / URL once → all agents in the current tab receive it.

- `@` overlay, fuzzy-pick file or paste URL
- injects into each agent's input buffer (tmux `send-keys`)
- one paste per repo instead of 4× copy-paste

kills a real daily pain.

### 3. agent diff-gate

review + gate agent writes. current plan below reflects landscape research
across the multi-agent tool peer group — deferring actual build until more
research on edge cases and provider-specific behavior.

#### landscape (as of 2026-04-18)

**worktree isolation is the dominant primitive** across every multi-agent
tool surveyed: superset, t3.code, cursor 3, crystal/nimbalyst, conduit,
claude squad, ccswarm, parallel-code, agent-deck. sculptor uses docker
containers. only aider + pre-v3 cursor stayed with in-repo postmortem. the
ecosystem already moved — shipping without worktrees = behind peers.

**TUI tools delegate write-review to the wrapped CLI.** agent-deck, claude
squad, conduit, ccswarm all rely on claude/codex/opencode's own approval
prompts; none ship a custom diff viewer. only GUI tools (superset, t3.code,
nimbalyst) add first-class review UI.

**gap in the market**: no TUI tool (go/rust/tmux) has a custom per-turn
diff review. a TUI version of t3.code's turn-stepper would be a real
differentiator.

#### v0.2 — worktree mode (match the market)

- `n` → pick provider → mux creates `.mux-worktrees/<codename>` as a git
  worktree off current HEAD with branch `mux/<codename>`. agent spawns
  there (cwd = worktree, not main repo).
- sidebar row shows diff count vs base branch — `hanuman  claude  +42 -7`.
- `R` opens review panel: per-file diff list, `a` accept / `r` reject /
  `c` squash-merge worktree into base branch + delete worktree.
- auto-add `.mux-worktrees/` to `.gitignore` on first spawn.
- per-agent attribution is free (worktree = branch = one agent's work).
- "cook overnight safely": main repo untouched until explicit merge.

**open design calls, decided:**
- default **worktree**, opt-out with `n -i` flag for in-repo (advanced).
- worktree location: **in-repo** (`.mux-worktrees/`) so editor file
  watchers can see agent work live.
- branch naming: `mux/<agent-codename>` off current HEAD.
- accept default: **squash-merge** (one commit per agent session), with
  full-merge as an option for debugging-heavy sessions.

**still open, needs research before build:**
- what happens when base branch moves during an agent run? rebase worktree
  mid-session or at accept? (cursor 3 rebases on accept.)
- dependency caches (node_modules, target/) — hardlink, symlink, or let
  them regen per worktree? watch disk usage.
- worktree cleanup on `d` (forget agent) — remove branch too, or leave it
  for recovery?

#### v0.3 — per-turn diff review (differentiator)

- tail the provider jsonl we already locate via layer-2 uuid capture.
- parse turn boundaries: user message → tool calls → assistant response.
- review panel shows a stepper: "turn 3 — edited auth.go, wrote test.go".
  expand → diffs. accept/reject *per turn*, not per file.
- matches how agents actually think; way less chatty than per-tool-call
  gating, way more granular than whole-branch review.
- zero TUI tools in the go/tmux space ship this. electron tools (t3.code,
  nimbalyst) do — proving demand exists.

**deferred**: custom review UI shape, which providers get first support,
how we handle turn boundaries when the agent's own output stream doesn't
map 1:1 to jsonl turns (streaming, interruptions, compaction).

#### explicitly rejected

- **protocol gate via hooks / MCP shim** (was tentatively v0.4) — would
  give real write interception for claude via `PreToolUse` hook, but:
  (a) provider-specific, fragile across updates; (b) permission UI inside
  a TUI during agent flow = ugly; (c) per-call prompts don't scale to N
  parallel agents, which is mux's whole reason for existing. skipped for
  the foreseeable future.

---

## tier A — strong, medium lift

### 4. prompt broadcast

type once, send to N selected agents.

- `space` in sidebar multi-selects
- prompt bar sends to all selected
- makes multi-provider actually useful (currently just novelty — race claude / codex / gemini, pick winner)

### 5. cost / token meter per agent

surface spend per-agent in sidebar.

- tail provider jsonl (`~/.claude/projects/<slug>/*.jsonl`, codex equivalent)
- parse token counts, multiply by known per-model price
- show `$0.42 · 12k tok` next to codename

people will install just for this. nobody surfaces per-agent spend cleanly.

### 6. watch mode / notifications

detached agent enters waiting-state → desktop notification or terminal bell.

- pairs with existing status classifier (80% done)
- lets people actually detach without fomo-checking
- optional per-agent toggle

---

## tier B — nice, small lift

### 7. session snapshot / replay

- `capture-pane` full scrollback on demand, save to `~/.config/mux/snapshots/<agent>-<ts>.txt`
- replay as scrollable read-only view
- debuggability + shareable bug reports

### 8. quick-prompt templates

- `:` → fuzzy list of saved prompts (`~/.config/mux/prompts/*.md`)
- "review this PR", "write tests", "fix types"
- sends to current agent via `send-keys`

tiny feature, huge daily use.

### 9. project-scoped env

- `.mux.toml` in repo root
- default provider, default cli args, auto-spawn list
- open repo → N agents spawn with right flags pre-wired

removes spawn friction for serious projects.

---

## polish / infra

- global config file (`~/.config/mux/config.toml`) — custom providers (aider, qwen), per-provider args (`claude --model opus`), extra repo-discovery roots
- **pin / archive projects** — pin favorites to top of picker, archive stale ones out of the way
- **SSH tabs** — open a project over ssh; tmux session runs on remote host, local body previews it
- **logs / scrollback view** — read-only scrollable history without full attach
- **split preview** — two agents side-by-side in body
- themes
- keybinding config
- status-line messages for agent events (errors, token limits, etc.)
  tier 3 — only if time

  10. compact mode at narrow widths — sidebar collapses to icons only under 80 cols. vscode-like.
  11. smooth mode transitions — bubble tea supports fade-in for overlays. 2x perceived quality.
  12. animated splash — pulse MUX ascii on empty state. gum tricks.
---

## shipping gates

- **v0.1** — public launch requires: LICENSE file ✓, github repo live, goreleaser binaries, demo gif, resume layer 1 ✓ + 2 ✓.
- **v0.2** — worktree mode (diff-gate section above) + resume layer 3 (adopt-on-demand).
- **v0.3** — per-turn diff review.
- **v0.4** — cost meter + broadcast + watch notifications.

order within each tier = whatever excites me that week. this is a vibe, not a gantt chart. v0.2 + v0.3 specifically are on hold pending more research into edge cases (base-branch movement, dep-cache sharing, jsonl turn parsing).
