# Stretch selection

The stretches box lists the day as nine rows (work stretches, meetings, no-evidence gaps). One row is selected; the selection drives every other action, is highlighted on the day strip, and is spelled out on the box's last line.

## Sub-features

- `sel-next` moves the selection down one stretch.
- `sel-prev` moves the selection up one stretch.
- `sel-clamp` stops at the first and last stretch instead of wrapping.
- `sel-detail` shows the selected stretch in full on the detail line.

## How to get to it (user POV)

- Press `j`, `l`, `Down` or `Right` for the next stretch.
- Press `k`, `h`, `Up` or `Left` for the previous stretch.

## Driving it with daystrip.sh

Preconditions:

- Fresh baseline; the counter reads `1/9` and the `│›` row is `08:10–10:00`.

- **Next.** Run `daystrip.sh keys j`. The counter reads `2/9`, the `│›` row is `10:00–10:45 ▀ Sprint planning`, and the detail line reads `› Sprint planning · meeting, not logged · 1 event fired during it`.
- **Every next key.** Run `daystrip.sh keys l Down Right`. The counter reads `5/9` and the `│›` row is `12:31–13:59`.
- **Previous.** Run `daystrip.sh keys k`. The counter reads `4/9` and the `│›` row is `11:43–12:31 ░ no evidence`.
- **Clamp at the end.** Run `daystrip.sh keys j j j j j j j j`. The counter reads `9/9` and the `│›` row is `15:55–17:11`.
- **Clamp at the start.** Run `daystrip.sh keys k k k k k k k k k k`. The counter reads `1/9`.
- **Nothing else moves.** Across all steps, the `│ total` row and `to send` line stay at the baseline values.
- **Proof.** Snap after each step with `daystrip.sh snap sel-<step>`.

## Gotchas

- Selecting does not change any number; a changed total during this recipe is a bug.
- The day-strip highlight is a background colour only: check it in the `.ansi` snap, not the `.txt`.
