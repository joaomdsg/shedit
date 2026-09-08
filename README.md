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
per non-empty pile; clicking opens a panel with the drop zone, the top items
per pile, and count chips. The full board is a separate overlay window,
opened from the panel's count chips and "+N more" links.

```sh
go build -o ~/.local/bin/shedit ./cmd/shedit
ln -sfn "$PWD/plugin" ~/.config/omarchy/plugins/jgonc.shedit
omarchy-shell shell rescanPlugins
omarchy plugin enable jgonc.shedit
```

Files under `plugin/` hot-reload on save. In the panel: Enter dumps,
Shift+Enter breaks a line, Ctrl+V pastes a clipboard image as an attachment,
dropped files stage as pills. Rows: click a row to edit; drag a row onto a
pile header or a count chip to move it (dropping on DONE completes it);
hover reveals only a done button. Enter on the text field focuses it, Escape
closes editors. Everything else (move, done, reopen, retry, attach, detach,
delete) lives in the board overlay.

### Board overlay

`Board.qml`, a fullscreen layer-shell window registered as the `overlay`
kind alongside `bar-widget`. A pile rail on the left (`2min`, `deadline`,
`eventually`, `unsorted`, `done`) with counts, the selected pile's full item
list next to it, and a detail pane on the right: editable title/summary/
deadline, the attachment list with per-attachment detach and text previews,
and controls to move, mark done, reopen, retry ("Re-extract"), attach,
detach, and delete (two-step confirm). Escape dismisses it. It opens from
the panel's count chips and "+N more" links, which shell out to
`omarchy-shell shell toggle jgonc.shedit '{"pile":"…"}'`, and it runs its
own `shedit watch` process, separate from the panel's.

Title and summary render read-only with `http(s)` and `www.` URLs
autolinked; an Edit button swaps in the editable fields. `url` attachment
rows show and open their real URL (the attachment's name is always
`link.txt`; the URL is the blob content). Host-labelled chips under the
summary cover stored URLs the summary text doesn't already mention. Links
open via `xdg-open`, restricted to `http(s)`.

## Test

```sh
go test -race ./...
markdownlint -r .markdownlint/no-tables.js -c .markdownlint.yaml '**/*.md'
```

No test calls the model. The model boundary is `internal/model.Runner`; tests
use a scripted fake or a fake `claude` script on disk.
