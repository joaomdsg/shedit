# shedit

> Shed it from your mind. Saved, sorted, and back when it matters.

This document is the idea, not the plan. It states the problem, the principles
derived from it, the concepts that follow, and the decisions already made.
Anything not here is open.

## The problem

Things arrive faster than they can be acted on. Holding them in your head costs
attention; writing them down costs friction; sorting them costs time you don't
have when they arrive. Every existing tool makes you do the sorting at capture
time, so capture gets skipped, and the thing stays in your head.

Two failure modes, opposite directions:

- **Leak.** You don't capture, so you forget, so you carry low-grade anxiety
  about what you forgot.
- **Pile-up.** You capture everything into one undifferentiated list, so the
  list becomes noise, so you stop looking at it.

## First principles

1. **Capture must cost nothing.** Any friction at the moment of capture and the
thing stays in your head. No form, no category, no title. Dump the raw thing and
walk away.
2. **Sorting is not the human's job.** The moment of capture is the worst
possible time to decide what something is. Something else sorts, later, with
more context than you had.
3. **The board is for glancing, not managing.** You look at it to know what's on
your plate right now. If it takes more than a glance, it's failing. Management
(moving, merging, snoozing) is possible but rare.
4. **Nothing is lost.** Whatever you dumped, you can get back — the original,
not a summary of it. Trust in the system depends on this. The system archives,
never deletes. Only you can delete, explicitly, and it asks first.
5. **The sorter earns trust slowly.** It explains every choice in one line. You
correct it by moving things. Corrections make it better. It never takes an
action you can't see and undo.
6. **Together means together.** If you dumped several things in one go, they're
one thing. The system does not guess at grouping what you separated, and does
not separate what you grouped.

## Core concepts

### Dump

The raw input. Text, URL, image, audio, file, or several of these at once. A
dump becomes exactly one **item**. A dump with several files becomes one item
with several **attachments**.

### Item

The unit on the board. Has: what was dumped (attachments), what the system
understood from it (extraction), where it sits (pile), why it sits there
(reason), and history (moves, extractions).

### Attachment

One raw input belonging to an item. Kept verbatim, forever. An item can gain or
lose attachments later; either triggers re-extraction.

### Extraction

What the system understood from the raw input: a title, a summary, any dates
(each due, event or mentioned), people, links, actions, a rough size, and a
confidence. Produced by something that can read what was dumped, whatever form
it took. It knows nothing about piles or about the rest of the board.
Extractions are versioned and kept, so the understanding can be redone and
compared when the extractor improves.

### Pile

Where an item sits. Four, fixed:

- **2min** — do it right now. Not a pile, a queue: it should be empty by end of
  day. The test is "one sitting, no context switch", not "small".
- **deadline** — has a due date. Ordered by urgency.
- **eventually** — no date, no rush.
- **unsorted** — the sorter was unsure, disagreed with a due date, or wanted a
  deadline with no date to back it. You decide.

### Sorter

Takes an extraction plus a compact view of the board plus your recent
corrections, and returns a pile, a confidence, an estimate, tags, and a one-line
reason. May also propose that an item relates to another. Works from the
extraction, never from the raw input. Runs often.

Hard rules run before the sorter: an explicit pile hint from you always wins; a
confidently parsed due date means `deadline`, unless a confident sorter calls
it `eventually`, which sends the item to `unsorted`.

### Move

Any change of pile, by you or by the system, with who did it and when. Your
moves are the training signal: they become examples the sorter sees next time.

### Reason

One line per item saying why it landed where it did. Always visible. This is how
you learn to trust the sorter, and how you catch it being wrong.

### Morning pass

Once a day (and on demand), something looks at the whole board: re-ranks
deadlines, promotes `eventually` items whose date has crept close, evicts
anything left in `2min` from yesterday (it was mis-sorted; that's a correction),
and writes a three-line "today" at the top of the board. This is the part that
orients you. Per-item sorting alone does not.

### Board

The view you glance at. Shows every pile and anything that failed. You can dump
from it, same pipeline as any other input.

## Relationships between items

The sorter may say "this looks related to that". It proposes; you confirm.
Confirming merges attachments into one item and re-extracts. Never auto-merge: a
false merge is worse than a false split.

## Decisions already made

- Two stages, not one: **extractor** (understands the dump, pile-blind,
  versioned) then **sorter** (places it, board-aware).
- Raw inputs are kept verbatim. Everything is backed up, or the "nothing is
  lost" promise is false.
- The raw input is saved before anything else happens to it. If it can't be
  saved, capture fails loudly. Never a silent drop.
- The system never deletes. Status moves through open → done → archived. You
  may delete an item and everything it holds, on purpose, after confirming.
- Extraction failures are visible on the board and retryable; the raw input is
  already safe.
- The sorter owns the effort estimate; the extractor's size guess is only a
  hint.
- Open source.

## Cheap things worth having early

Snooze (hide until a date, morning pass surfaces it). Optional one-word reason
when you move something, fed to the sorter. Done log. What the sorting costs you
per day. Recognising the same thing dumped twice. A health line: is every stage
running, is everything safe. Export of everything.

## Deliberately open

Whether the sorter may move items on its own after the initial sort or only
propose. How long done items stay visible. Both get answered by using it for a
week. Default conservatively: propose only, archive on done.

## Out of scope for now

Tags, search, multi-user, external pulls (calendar, email). Real, none needed
until they hurt.

## How it gets built

Test-first. Nothing lands without a failing test guiding the way, including tiny
fixes.

## Revisions after first production use

Eighteen real items, unanswered mail and stale GitHub work, went in as one
batch. What that exposed, and what is now decided.

### A date is not a deadline

An email sent on 4 Aug produced an item "was due Tue 4 Aug". That is a lie the
board tells you every time you glance at it, which is the one thing the board
must never do.

Decided: the extractor labels every date it finds `due`, `event`, or
`mentioned`. Only `due` reaches the deadline rule, past or future. Other dates
are context the sorter may read and the board does not show.

### Age is a nudge, never a reason

Sorting the batch produced ten reasons that were the same sentence with a
different number in it. A reason that could have been written without reading
the item is not a reason; principle 5 dies quietly there.

Decided: a reason names something specific, a person, an action, a date or a
link, and the sorter must point at it in what was extracted. It sees the reasons
on other open items and may not repeat one. A rejected reason gets one retry;
a second rejection sends the item to `unsorted`.

### The sorter no longer chooses `unsorted`

Two items reached `unsorted` carrying confident rationales. If the sorter can
argue for a placement it was not unsure, and `unsorted` becomes a fifth pile by
the back door.

Decided: the sorter no longer chooses `unsorted`. It returns a pile and a
confidence. Low confidence makes an item unsorted, and so does a confident
sorter calling a due item `eventually`.
