# shedit v0 scope

The bare minimum that is usable every day: an Omarchy bar widget backed by a
local daemon, with real two-stage sorting. Everything here is decided; anything
not here is not in v0.

## What you get

- A dot cluster in the bar, always visible: green = items in `2min`, red =
  items in `deadline`, grey = everything else open. Each dot carries its count.
  Done and archived are not counted.
- Click the cluster and a panel opens (click again, Esc, or click-outside
  closes). Same convention as the other Omarchy panels.
- The panel has a drop zone at the top and the board below.

## Drop zone

Accepts, in one dump: typed or pasted text, dropped files, pasted images from
the clipboard. URLs are just text for now; the extractor fetches them. Several
things dropped together are one item with several attachments.

The raw input is written to disk before anything else happens. If that fails,
the panel says so and nothing is dropped silently.

## Board (in the panel)

Items grouped by pile: `2min`, `deadline` (by date), `eventually`, `unsorted`.
Failed extractions sit under `unsorted` with a retry. Each item shows its title
and its one-line reason.

Per item you can:

- mark done, and reopen if you were wrong
- move to another pile (recorded as a correction)
- edit the title or summary (stored as your override, not a new extraction)
- set or clear the deadline date (moves to or from `deadline` accordingly)
- add or remove attachments (triggers re-extraction)
- archive
- delete, after confirming (`-y` on the CLI); removes the item and every
  attachment file

After you move something, a small, dismissable invite appears on that item
asking for a one-sentence reason. Optional. If given, the sorter sees it.

## Sorting

Two stages, as designed, both run through headless Claude Code (`claude -p`
with structured JSON output):

1. **Extractor** reads the attachments (text, images, PDFs, fetched URLs) and
   returns title, summary, dates, people, links, actions, size guess,
   confidence. Pile-blind. Result versioned and kept.
2. **Sorter** takes the extraction, a compact board view, and your recent
   corrections (moves plus reasons) and returns pile, effort estimate, tags,
   one-line reason. May propose a relation to another item; v0 shows the
   proposal but does not act on it.

Hard rules run first and win: an explicit pile hint in the dump text, and a
confidently parsed date, which always means `deadline` (a past one is shown as
overdue). The sorter may not put an item in `deadline` without a date, and a
re-extraction never overrides a pile you chose by hand.

Both stages use Sonnet. Haiku was considered for the sorter and rejected: it
has to tell your intent apart from the content of what you dumped.

## Not in v0

Morning pass. Snooze. Done log. Cost per day (only the running daemon's
spend, via `shedit status`). Duplicate detection. Export. Relations acted on
(merge). Any ingest path other than the panel and the CLI. Health line.

## Daemon

Go, single binary. SQLite for items, extractions, moves, reasons. Attachment
files verbatim on disk under the user data directory. Both live in one folder
so a plain copy is a backup.

Started on demand by the first client that finds no daemon; a lock file makes
sure only one wins. Stays up until stopped. On start it marks anything left
mid-sort as interrupted and retryable. No systemd unit in v0.

Model calls run in an empty scratch directory with only the tools they need
(Read for files, WebFetch for links), so nothing from your projects leaks in.

Interface:

- Down: a `shedit` CLI. The widget shells out for every action (dump, move,
  done, edit, archive, delete, retry). The same CLI is the future terminal
  ingest path.
- Up: `shedit watch` streams one full board snapshot per line (JSON) to
  stdout for as long as it runs. The widget keeps one `watch` process alive
  and re-renders on every line. Both directions go over a Unix socket in the
  user runtime directory; no TCP port is opened.

## Widget

A user plugin under `~/.config/omarchy/plugins/<user>.shedit/`, kind
`bar-widget`, following the Weather/Agents plugin shape: `BarWidget.qml`
(dots) plus `Panel.qml` (drop zone and board). Lives in this repo and is
symlinked into place.

## Testing

Test-first for the Go daemon: storage, hard rules, prompt building, CLI, SSE,
with a fake `claude` runner so tests never call the model. The QML widget is
verified by hand in the bar.

## Repo layout

```text
cmd/shedit/        CLI and daemon entry point
internal/          storage, extractor, sorter, rules, server
plugin/            Omarchy bar-widget (QML + manifest), not started yet
```
