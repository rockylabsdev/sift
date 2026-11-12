# Events audit view

Pressing `e` replaces the stretches and draft panels with an audit of the selected stretch: per-ticket event marks over the minutes counted, where the events came from, and every event with the silence before it.

## Sub-features

- `events-open` opens the view for the selected stretch.
- `events-close` returns to the stretches and draft panels.
- `events-follow` follows the selection while open.
- `events-empty` says when nothing fired in a stretch.

## How to get to it (user POV)

- Select a stretch and press `e`; press `e` again to close.

## Driving it with daystrip.sh

Preconditions:

- Fresh baseline.

- **Open.** Run `daystrip.sh keys j j j j e`. The title reads `events · 12:31–13:59 · 25`; a line reads `PUN-142  ← branch feature/PUN-142-side-lock · claude session · 23 prompts · 2 commits`; the event list starts `12:31 │` and includes `13:39 │ +9m`.
- **Follow.** Run `daystrip.sh keys k`. The title reads `events · 11:43–12:31 · 0` and the body reads `nothing fired in this stretch`.
- **Close.** Run `daystrip.sh keys e`. The stretches box returns with `4/9` selected and baseline totals.
- **Proof.** Snap the open and closed states with `daystrip.sh snap events-<step>`.

## Gotchas

- Event kinds and branch names are faked by the prototype; prove only that they are shown, not where they came from.
- `+8m`/`+9m` render amber (near the 10-minute idle gap); check colour in the `.ansi` snap.
