# Stretch selection

The stretches box lists the day as nine rows (work stretches, meetings, no-evidence gaps). One row is selected. The selection drives every other action, is bracketed on the day box, and is spelled out in two detail lines at the foot of the stretches box.

## Sub-features

- `sel-next` moves the selection down one stretch.
- `sel-prev` moves the selection up one stretch.
- `sel-clamp` stops at the first and last stretch instead of wrapping.
- `sel-detail` shows the selected stretch in full in the two detail lines.
- `sel-bracket` marks the selected stretch on the day box with `└──┘ HH:MM–HH:MM`.

## How to get to it (user POV)

- Press `j`, `l`, `Down` or `Right` for the next stretch.
- Press `k`, `h`, `Up` or `Left` for the previous stretch.

## Driving it with daystrip.sh

Preconditions:

- Fresh baseline. The counter reads `1 of 9`, the `│›` row is `08:10–10:00   43 events`, the detail reads `08:10–10:00 · 1h 50m` then `PUN-142 1h 30m · OPS-77 20m · from 43 events`, and the day box bracket reads `08:10–10:00`.

- **Next.** Run `daystrip.sh keys j`. The counter reads `2 of 9`, the `│›` row is `10:00–10:45 ▰ Sprint planning`, the detail reads `Sprint planning · 45m` then `meeting · unassigned · 1 event fired during it`, and the bracket reads `10:00–10:45`.
- **Every next key.** Run `daystrip.sh keys l Down Right`. The counter reads `5 of 9`, the `│›` row is `12:31–13:59   25 events`, and the detail reads `PUN-142 1h 28m · from 25 events`.
- **Previous.** Run `daystrip.sh keys k`. The counter reads `4 of 9`, the `│›` row is `11:43–12:31 ░ gap`, and the detail reads `Gap · 48m` then `no evidence · unassigned`.
- **Clamp at the end.** Run `daystrip.sh keys j j j j j j j j`. The counter reads `9 of 9` and the `│›` row is `15:55–17:11   22 events`.
- **Clamp at the start.** Run `daystrip.sh keys k k k k k k k k k k`. The counter reads `1 of 9`.
- **Nothing else moves.** Across all steps, the `│ total` row reads `2h 58m  1h 41m  1h 44m     2h 26m` and the draft reads `ready to send  6h 23m`.
- **Proof.** Snap after each step with `daystrip.sh snap sel-<step>`.

## Gotchas

- Selecting does not change any number. A changed total during this recipe is a bug.
- The selected row also has a background colour, and only the focused box (stretches) has the bright border. Check both in the `.ansi` snap, not the `.txt`.
