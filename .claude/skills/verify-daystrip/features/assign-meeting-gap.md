# Assign meetings and gaps

A meeting or a no-evidence gap starts as unassigned time. Pressing `t` on it assigns it to a ticket, which moves its minutes from the `unassigned` column into that ticket's column, total and draft worklog.

## Sub-features

- `assign-gap` cycles a gap through PUN-142, OPS-77, PUN-150 and back to unassigned.
- `assign-meeting` cycles a meeting without a ticket in its title the same way.
- `assign-suggested` sends a meeting whose title names a ticket to that ticket first.
- `assign-totals` keeps the stretches footer, day-box lanes and summary, draft and header in step.

## How to get to it (user POV)

- Select a meeting (`▰`) or gap (`░`) row, then press `t` or `Enter`.

## Driving it with daystrip.sh

Preconditions:

- Fresh baseline.

- **Gap to PUN-142.** Run `daystrip.sh keys j j j`, then `daystrip.sh keys t`. The `│›` row `11:43–12:31 ░ gap` shows `48m` under PUN-142. The detail reads `Gap · 48m` then `no evidence · assigned to PUN-142`. `│ total` reads PUN-142 `3h 46m`, unassigned `1h 38m`. The draft reads PUN-142 `3h 46m` and `ready to send  7h 11m`; the header reads `7h 11m to send`. The PUN-142 lane label reads `3h 46m` and the day-box summary reads `░ gaps 23m`.
- **Gap onward.** Run `daystrip.sh keys t`. The `48m` moves to the OPS-77 column, OPS-77 totals `2h 29m`, and PUN-142 is back to `2h 58m`. The next `t` moves it to PUN-150 (`2h 32m`). The one after reads `no evidence · unassigned` with the baseline totals and `░ gaps 1h 11m`.
- **Suggested meeting.** From a fresh baseline run `daystrip.sh keys j j j j j`. The detail reads `PUN-150 swarm · 30m` then `meeting · unassigned · its title names PUN-150`. Run `daystrip.sh keys Enter`. The row shows `30m` under PUN-150, the detail reads `meeting · assigned to PUN-150`, PUN-150 totals `2h 14m`, unassigned `1h 56m`, `ready to send  6h 53m`, and the summary reads `▰ meetings 45m`.
- **Plain meeting.** From a fresh baseline run `daystrip.sh keys j t`. Sprint planning goes to PUN-142. The row shows `45m` under PUN-142, the detail reads `meeting · assigned to PUN-142 · 1 event fired during it`, PUN-142 totals `3h 43m`, unassigned `1h 41m`, `ready to send  7h 08m`, and the summary reads `▰ meetings 30m`.
- **Work stretch ignores t.** Run `daystrip.sh keys k t` on a work stretch. No number changes.
- **Proof.** Snap before and after each `t` with `daystrip.sh snap assign-<step>`.

## Gotchas

- A meeting whose title names a ticket cycles only between that ticket and unassigned (PUN-150 swarm never reaches PUN-142 or OPS-77). That is current behaviour, not a harness fault.
- The day-box summary counts only unassigned time, so it shrinks as meetings and gaps are assigned.
