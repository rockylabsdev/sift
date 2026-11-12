# The evidence log

Sift records evidence that you were working, then derives your day from it. This page is the contract for that record. Collectors outside the `sift` binary will write to it, so it changes only by adding fields, and a breaking change raises `v`.

## Where it lives

Events go to `$XDG_DATA_HOME/sift/events/YYYY-MM.jsonl`, or `~/.local/share/sift/events/` when `XDG_DATA_HOME` is unset. There is one file per UTC month. Each line is one JSON object.

The log is append-only. Sift never rewrites a line, and corrections to a day live in that day's decisions, not in the events. A writer takes an exclusive `flock` on `evidence.lock` in the data directory while it appends one line.

## An event

```json
{"v":1,"at":"2026-11-10T09:00:12Z","source":"claude-code","kind":"prompt","ticket":"PUN-142","found":{"via":"branch","value":"feature/PUN-142-side-lock"},"dir":"/home/me/src/app","origin":"prompt"}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `v` | integer | Schema version. This page describes `1`. A reader skips lines with a higher `v` and reports them. |
| `at` | RFC 3339 time, UTC | When it happened, to the second. |
| `source` | string | The collector: `claude-code` for the plugin, `cli` for `sift record` by hand. |
| `kind` | string | What happened: `prompt` for a prompt you typed, `hand` for `sift record`. |
| `ticket` | string or `null` | The tracker key the work was for. `null` when nothing named one. That time shows up as unassigned and is never guessed onto a ticket. |
| `found` | object or `null` | How the ticket was told. `via` is `file` (a `.sift` or `.punchcard` file, `value` is its path) or `branch` (`value` is the branch name). `null` exactly when `ticket` is. |
| `dir` | string | The directory the work happened in. |
| `origin` | `hand` or `prompt` | Who acted. Only your own acts are evidence. An agent's activity (`agent`) is never written. |

## How a ticket is told

The nearest `.sift` file at the directory or above wins, looking no higher than the git root. A `.punchcard` file is read the same way so directories set up for Punchcard keep working. The file's first line that is not blank and not a `#` comment must be a key such as `PUN-142`. When no file names a key, the first key in the current branch name is used (`feature/pun-142-x` gives `PUN-142`).

## What the deriver does with it

`sift draft` reads a day's events and applies these rules (`internal/derive`):

- Two events at most the idle gap apart (default 10 minutes, `idle_gap_minutes` in the config) join into one stretch. The time between them is split at the midpoint between their tickets.
- A stretch ends 5 minutes after its last event.
- A meeting takes every minute it covers, unless `events_beat_meetings` is set. Meetings arrive with the calendar, in a later slice.
- A silence of at least the idle gap, between the day's first and last event, is a gap. Gaps are not counted unless you assign them.
- A day is the local calendar day. A stretch that crosses midnight is split there. Counted time never exceeds the hours in the day.
- The same events always derive the same day.
