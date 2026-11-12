# Daystrip verification map

This directory is the maintained source for verifying the user-facing behaviour of the daystrip prototype (`cmd/prototype-daystrip`). Read this index before driving the app, then use the matching feature file as the recipe.

## Baseline preconditions

- Start with `eval "$(.claude/skills/verify-daystrip/daystrip.sh start | head -1)"` at the default 120x36.
- `daystrip.sh doctor` reports `app process running`, `build ... matches checkout` and `main screen drawn`.
- The seeded baseline: stretch `1/9` selected; totals PUN-142 `2h 58m`, OPS-77 `1h 41m`, PUN-150 `1h 44m`; unlogged `2h 26m`; draft `to send ... 6h 23m`.
- Never drive an instance that this run did not start.

## Driving conventions

- Start each recipe from a fresh `start` unless it says otherwise: there is no undo, and state is per process.
- Send keys with `daystrip.sh keys`; it waits for the redraw. Do not add sleeps.
- Find rows by their text (`│›`, `│ total`, `to send`), never by line number: panel heights change with terminal size.

## Proof and skip reporting

- Snap before and after each action with `daystrip.sh snap <feature-id>-<step>`.
- Quote the before and after numbers from the snaps in the report.
- A recipe whose expected number does not appear is a failure. Report it with the snap paths; do not adjust the expectation to the screen.

## Feature entry contract

Each feature file starts with an H1 title and one paragraph describing the user-visible behaviour, then exactly four H2 sections: `Sub-features`, `How to get to it (user POV)`, `Driving it with daystrip.sh`, and `Gotchas`.

## Features

- [Stretch selection](./stretch-selection.md) covers moving through the nine stretches and what the selection highlights.
- [Assign meetings and gaps](./assign-meeting-gap.md) covers logging a meeting or a no-evidence gap to a ticket and the totals that follow.
- [Adjust by hand](./adjust-by-hand.md) covers adding and removing 5-minute steps on a ticket.
- [Events audit view](./events-view.md) covers the per-stretch event listing.
- [Size guard and quit](./size-guard-and-quit.md) covers the minimum terminal size and leaving the app.
