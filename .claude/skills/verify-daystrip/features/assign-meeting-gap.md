# Assign meetings and gaps

A meeting or a no-evidence gap starts as unlogged time. Pressing `t` on it logs it to a ticket, which moves its minutes from the `unlogged` column into that ticket's column, total and draft worklog.

## Sub-features

- `assign-gap` cycles a gap through PUN-142, OPS-77, PUN-150 and back to not logged.
- `assign-meeting` cycles a meeting without a ticket in its title the same way.
- `assign-suggested` sends a meeting whose title names a ticket to that ticket first.
- `assign-totals` keeps the stretches footer, day-strip labels and draft in step.

## How to get to it (user POV)

- Select a meeting (`▀`) or gap (`░`) row, then press `t` or `Enter`.

## Driving it with daystrip.sh

Preconditions:

- Fresh baseline.

- **Gap to PUN-142.** Run `daystrip.sh keys j j j`, then `daystrip.sh keys t`. The `│›` row `11:43–12:31 ░ no evidence` shows `48m` under PUN-142; the detail line reads `› no evidence for 48m, → PUN-142`; `│ total` reads PUN-142 `3h 46m`, unlogged `1h 38m`; the draft reads `to send ... 7h 11m` and PUN-142 `3h 46m`; the day strip label under PUN-142 reads `3h 46m`.
- **Gap onward.** Run `daystrip.sh keys t`. The `48m` moves to the OPS-77 column and OPS-77 totals `2h 29m`; PUN-142 is back to `2h 58m`. Two more `t` presses go to PUN-150, then `not logged` with the baseline totals.
- **Suggested meeting.** From a fresh baseline run `daystrip.sh keys j j j j j`. The detail line reads `› PUN-150 swarm · meeting, not logged · its title names PUN-150`. Run `daystrip.sh keys Enter`. The row shows `30m` under PUN-150; PUN-150 totals `2h 14m`; unlogged `1h 56m`; `to send ... 6h 53m`.
- **Plain meeting.** From a fresh baseline run `daystrip.sh keys j t`. Sprint planning logs to PUN-142: the row shows `45m` under PUN-142, PUN-142 totals `3h 43m`, unlogged `1h 41m`, `to send ... 7h 08m`.
- **Work stretch ignores t.** Run `daystrip.sh keys k t` on a work stretch. No number changes.
- **Proof.** Snap before and after each `t` with `daystrip.sh snap assign-<step>`.

## Gotchas

- A meeting whose title names a ticket cycles only between that ticket and `not logged` (PUN-150 swarm never reaches PUN-142 or OPS-77). That is current behaviour, not a harness fault.
- The draft `unlogged · meetings ... · gaps ...` line is cut with `…` at 120 columns; start at `160 40` to read it in full (`unlogged · meetings 1h 15m · gaps 1h 11m`).
