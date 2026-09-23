# demo recording

`demo.gif` is a recording of mux running real shell sessions in disposable git repos. it shows project switching, session switching, a diff, tests, the `Alt+s` shell shortcut and fullscreen mode.

re-record from the repo root:

```sh
go build -o mux ./cmd/mux
python3 docs/record-demo.py
```

requires VHS, ttyd, ffmpeg, tmux, git, bash and Python 3. VHS also needs its browser runtime available.

the script creates temporary repos under `~/realm/bin`, with separate mux config, data and a separate tmux server. it removes those repos and sessions after recording, including when recording fails. set `MUX_DEMO_PARENT` to use a different temporary parent directory.

edit `demo.tape` to change the timing and appearance. the GIF goes to `docs/demo.gif`.
