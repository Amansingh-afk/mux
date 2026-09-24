# demo recording

`demo.gif` and `demo.webm` show mux at 1920 x 1080 with real Codex and Claude sessions. each agent answers a short question, then a shell opens lazygit with `lg`, closes it and runs the project tests.

re-record from the repo root:

```sh
go build -o mux ./cmd/mux
python3 docs/record-demo.py
```

requires VHS, ttyd, ffmpeg with GIF and WebM encoding, tmux, git, bash, Python 3, lazygit, Codex and Claude. both agent CLIs must already be signed in. recording sends real prompts and uses your normal provider allowance. VHS also needs its browser runtime available.

the script creates a disposable git repo under `~/realm/bin`, with separate mux config, data and a separate tmux server. it removes the repo, worktrees and tmux sessions after recording, including on failure. provider conversation history stays with the providers. set `MUX_DEMO_PARENT` to change the temporary parent directory.

`lg` is a temporary wrapper for lazygit inside the demo. your shell config is not changed. the only startup approval handled automatically is folder trust for the generated demo worktrees.

edit `demo.tape` to change the timing and appearance. the tape waits for real answers before moving on. the WebM version can be opened full screen; the GIF is embedded in the main README.
