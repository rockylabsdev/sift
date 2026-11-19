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
| `origin` | `hand`, `prompt` or `agent` | Who acted. `hand` and `prompt` are your own acts. `agent` is an agent's activity: it only fills time your own evidence leaves unclaimed (see below). |
| `session` | string, optional | The agent session the event came from. A subagent's events carry the session of the conversation that started it. |
| `agentId` | string, optional | The subagent, for agent activity from one. Absent for the main agent. |
| `spanMs` | integer, optional | How long the act ran, ending at `at`: a tool call's duration. |

A reader written before `agent` existed skips agent events, as it skips any origin it does not know.

## How a ticket is told

The nearest `.sift` file at the directory or above wins, looking no higher than the git root. A `.punchcard` file is read the same way so directories set up for Punchcard keep working. The file's first line that is not blank and not a `#` comment must be a key such as `PUN-142`. When no file names a key, the first key in the current branch name is used (`feature/pun-142-x` gives `PUN-142`). Set `"projects": ["PUN", "OPS"]` in `$XDG_CONFIG_HOME/sift/config.json` and the branch rule takes only those projects' keys, so a branch such as `fix-2/pun-142-x` gives `PUN-142` rather than `FIX-2`.

## Agent activity

The plugin records an agent's work as well as your prompts: each finished tool call (with how long it ran), and each subagent's start and stop. At most one is written a minute per session and ticket, except a run of a minute or more, which is always kept.

An agent event's ticket is, in order:

1. its directory's own: a `.sift` or `.punchcard` file, or a branch that names a key (with `projects` set, only those projects' keys). Claude Code's own subagent branches, `worktree-agent-<id>`, never count.
2. the ticket of your latest prompt in the same session, with `found.via` set to `session`. This is how a subagent working in a worktree whose branch names no ticket (implement-spec's per-ticket branches, pstack's subagents) is credited to the ticket you were working.
3. none: the time is unassigned, and the review lets you give it to a ticket.

## What the deriver does with it

`sift draft` reads a day's events and applies these rules (`internal/derive`):

- Two events at most the idle gap apart (default 10 minutes, `idle_gap_minutes` in the config) join into one stretch. The time between them is split at the midpoint between their tickets.
- A stretch ends 5 minutes after its last event.
- A meeting takes every minute it covers, unless `events_beat_meetings` is set. Meetings arrive with the calendar, in a later slice.
- A silence of at least the idle gap, between the day's first and last event, is a gap. Gaps are not counted unless you assign them.
- Agent activity fills only minutes that your prompts, meetings and blocks leave unclaimed, by the same joining rules: a tool call covers its whole run, and a tail follows the last act. Where agents on several tickets run at once, those minutes are shared between the tickets in turn, so a day still never counts more than its hours. `agent_time` in the config chooses `fill` (the default), `workday` (only between your first and last prompt) or `off`.
- A day is the local calendar day. A stretch that crosses midnight is split there. Counted time never exceeds the hours in the day.
- Nothing after now counts. On today, a meeting that has not started is not counted, one in progress counts up to now, and a stretch's tail stops at now.
- The same events always derive the same day.
