# shedit

> Shed it from your mind. Saved, sorted, and back when it matters.

Dump anything. Something else sorts it. Glance at the board to see what's on
your plate. Nothing is ever lost.

The idea lives in [IDEA.md](IDEA.md); the first slice in
[SCOPE-v0.md](SCOPE-v0.md). The repo holds the core (daemon, pipeline, CLI)
and the Omarchy bar widget under `plugin/`.

## Run

Needs Go and a logged-in `claude` CLI.

```sh
go build -o shedit ./cmd/shedit
./shedit "dentist thursday 3pm, bring insurance card"
./shedit board
./shedit watch
./shedit -h
```

The first client command starts the daemon in the background. Data lives in
`~/.local/share/shedit`; copying that folder is a complete backup. The socket
is `$XDG_RUNTIME_DIR/shedit.sock`.

## Widget

An Omarchy shell plugin (`jgonc.shedit`). The bar shows one glyph and count
per non-empty pile; clicking opens a panel with the drop zone and the board.

```sh
go build -o ~/.local/bin/shedit ./cmd/shedit
ln -sfn "$PWD/plugin" ~/.config/omarchy/plugins/jgonc.shedit
omarchy-shell shell rescanPlugins
omarchy plugin enable jgonc.shedit
```

Files under `plugin/` hot-reload on save. In the panel: Enter dumps,
Shift+Enter breaks a line, Ctrl+V pastes a clipboard image as an attachment,
dropped files stage as pills. Rows: click or `e` edits, right-click or `m`
moves, drag a row onto another pile to move it, hover reveals done/delete.
`j`/`k` move the cursor, Enter on the text field focuses it, `x` deletes.

## Test

```sh
go test -race ./...
markdownlint -r .markdownlint/no-tables.js -c .markdownlint.yaml '**/*.md'
```

No test calls the model. The model boundary is `internal/model.Runner`; tests
use a scripted fake or a fake `claude` script on disk.
