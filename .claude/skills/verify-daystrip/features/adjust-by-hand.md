# Adjust by hand

On a work stretch, `+` and `-` add or remove 5 minutes on the ticket that starts the stretch. The change shows in the ticket's total and in the draft, marked as a hand adjustment.

## Sub-features

- `adjust-add` adds 5m per press.
- `adjust-remove` removes 5m per press.
- `adjust-mark` shows `+Nm by hand` beside the ticket in the draft.
- `adjust-floor` never shows a total below 0m.
- `adjust-only-work` ignores `+` and `-` on meetings and gaps.

## How to get to it (user POV)

- Select a work stretch, then press `+` (or `=`) or `-`.

## Driving it with daystrip.sh

Preconditions:

- Fresh baseline started wide: `daystrip.sh start 160 40`. At 120 columns the draft cuts the `by hand` mark to `…`.
- `1/9` (`08:10–10:00`, PUN-142 first) is selected.

- **Add.** Run `daystrip.sh keys + +`. PUN-142 totals `3h 08m` in the `│ total` row, the draft and the day-strip label; the draft PUN-142 line ends `+10m by hand`; `to send ... 6h 33m`. The `08:10–10:00` row still shows `1h 30m`.
- **Remove.** Run `daystrip.sh keys - - -`. PUN-142 totals `2h 53m`, the draft reads `-5m by hand`, and `to send ... 6h 18m`.
- **Floor.** Run `daystrip.sh keys $(printf -- '- %.0s' $(seq 40))`. PUN-142 totals `0m`, never negative; `to send` drops from `6h 18m` to `3h 25m`, exactly PUN-142's previous `2h 53m`.
- **Meetings ignore it.** Run `daystrip.sh keys j +`. No number changes.
- **Proof.** Snap before and after each step with `daystrip.sh snap adjust-<step>`.

## Gotchas

- The stretches rows never include hand adjustments, but the `│ total` row does, so after `+`/`-` a ticket's column no longer adds up to its total.
- Past the floor, extra `-` presses keep counting: after forty presses, getting back above 0m takes as many `+` presses as went below it.
- On a stretch shared by two tickets, only the first ticket listed in the detail line is adjusted.
