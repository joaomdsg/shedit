# shedit

> Shed it from your mind. Saved, sorted, and back when it matters.

Dump anything. Something else sorts it. Glance at the board to see what's on
your plate. Nothing is ever lost.

The idea lives in [IDEA.md](IDEA.md); the first slice in
[SCOPE-v0.md](SCOPE-v0.md). This repo currently holds the core only: daemon,
pipeline, and CLI. No UI yet.

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

## Test

```sh
go test -race ./...
markdownlint -r .markdownlint/no-tables.js -c .markdownlint.yaml '**/*.md'
```

No test calls the model. The model boundary is `internal/model.Runner`; tests
use a scripted fake or a fake `claude` script on disk.
