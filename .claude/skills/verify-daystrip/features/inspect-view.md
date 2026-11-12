# Inspect view

Pressing `e` replaces the stretches and draft boxes with an inspect box for the selected stretch. The first level summarises it: the stretch line, the minutes counted per ticket with their sources and branch, warnings such as the longest silence, and a mini lane of event marks. Pressing `e` again adds every event with the silence before it. A third `e`, or `esc` at either level, closes it.

## Sub-features

- `inspect-open` opens the summary level, titled `2 inspect`.
- `inspect-every` opens the event listing level, titled `2 inspect · every event`.
- `inspect-close` returns to the stretches and draft boxes on the third `e`.
- `inspect-esc` closes either level with `esc`.
- `inspect-follow` follows the selection while open.
- `inspect-empty` says when nothing fired in a stretch.
- `inspect-meeting` names the events that fired during a meeting.

## How to get to it (user POV)

- Select a stretch and press `e`. Press `e` again for every event, and once more to close. Press `esc` to close at any level.

## Driving it with daystrip.sh

Preconditions:

- Fresh baseline.

- **Open.** Run `daystrip.sh keys j j j j e`. The box title reads `2 inspect` with right title `25 events`. Lines read `12:31–13:59 · 1h 28m · work · 25 events`, `PUN-142   1h 28m counted · 23 prompts, 2 commits (shown, not counted) · branch feature/PUN-142-side-lock` and `! longest silence +9m before 13:39, 1m inside the 10m idle gap; a slower prompt would have split this stretch`. A `PUN-142` mini lane follows, then `e again lists every event with the silence before it · esc closes`. The day box stays above with the bracket on `12:31–13:59`.
- **Every event.** Run `daystrip.sh keys e`. The title reads `2 inspect · every event` with the legend `│ prompt  ◆ commit  +9m near the idle gap`. The summary stays, and an event listing follows that starts `12:31 │` and includes `13:39 │ +9m`.
- **Follow.** Run `daystrip.sh keys k`. The title still reads `2 inspect · every event`. The stretch line reads `11:43–12:31 · 48m · gap · no evidence · unassigned` and the body reads `nothing fired in this stretch`.
- **Close.** Run `daystrip.sh keys e`. The stretches box returns with `4 of 9` selected and baseline totals.
- **Esc from the summary.** Run `daystrip.sh keys e` (title `2 inspect`, right title `0 events`), then `daystrip.sh keys Escape`. The stretches box returns with `4 of 9`.
- **Esc from every event.** Run `daystrip.sh keys e e` (title `2 inspect · every event`), then `daystrip.sh keys Escape`. The stretches box returns with `4 of 9`.
- **Meeting.** Run `daystrip.sh keys k k e`. The right title reads `1 event`. Lines read `10:00–10:45 · 45m · Sprint planning · meeting · unassigned`, `OPS-77        0m counted · 1 prompt · branch ops/OPS-77-staging-certs` and `! 1 event fired during it (OPS-77 at 10:20); the meeting wins until you assign it`.
- **Proof.** Snap each level and each closed state with `daystrip.sh snap inspect-<step>`.

## Gotchas

- Event kinds and branch names are faked by the prototype. Prove only that they are shown, not where they came from.
- `+8m`/`+9m` render amber (near the 10-minute idle gap). Check colour in the `.ansi` snap.
- The level survives selection changes: `k` or `j` inside `every event` stays at that level.
