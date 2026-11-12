# Adjust by hand

On a work stretch, `+` and `-` add or remove 5 minutes on the ticket that starts the stretch. The change shows in the ticket's total and in the draft, marked as a hand adjustment.

## Sub-features

- `adjust-add` adds 5m per press.
- `adjust-remove` removes 5m per press.
- `adjust-mark` shows `+Nm by hand` after the ticket key in the draft.
- `adjust-floor` never shows a total below 0m.
- `adjust-only-work` ignores `+` and `-` on meetings and gaps.

## How to get to it (user POV)

- Select a work stretch, then press `+` (or `=`) or `-`.

## Driving it with daystrip.sh

Preconditions:

- Fresh baseline at the default 120x36. The `by hand` mark fits at that width.
- `1 of 9` (`08:10–10:00`, PUN-142 first) is selected.

- **Add.** Run `daystrip.sh keys + +`. PUN-142 totals `3h 08m` in the `│ total` row, the draft and the day-box lane label. The draft line reads `PUN-142  +10m by hand            3h 08m`. The draft reads `ready to send  6h 33m` and the header `6h 33m to send`. The `08:10–10:00` row still shows `1h 30m`, and a `│ by hand` row above the rule shows `+10m` under PUN-142, so the column still adds up to `3h 08m`.
- **Remove.** Run `daystrip.sh keys - - -`. PUN-142 totals `2h 53m`, the draft reads `-5m by hand`, the `│ by hand` row shows `-5m`, and `ready to send  6h 18m`.
- **Floor.** Run `daystrip.sh keys $(printf -- '- %.0s' $(seq 40))`. PUN-142 totals `0m`, never negative. `ready to send` drops from `6h 18m` to `3h 25m`, exactly PUN-142's previous `2h 53m`. The draft mark and the `│ by hand` row both read `-2h 58m`, the change actually made.
- **Back from the floor.** Run `daystrip.sh keys +`. PUN-142 totals `5m` at once and the mark reads `-2h 53m by hand`: presses past the floor are not stored.
- **Meetings ignore it.** Run `daystrip.sh keys j +`. No number changes.
- **Proof.** Snap before and after each step with `daystrip.sh snap adjust-<step>`.

## Gotchas

- The `│ by hand` row appears only while an adjustment changes a total, so it takes one stretch row from the table: at 120x36 the rule then reads `0 above · 1 below`.
- On a stretch shared by two tickets, only the first ticket listed in the detail is adjusted.
