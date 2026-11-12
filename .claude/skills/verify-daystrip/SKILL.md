---
name: verify-daystrip
description: Drive the daystrip prototype TUI (cmd/prototype-daystrip, Sift's daily-review screen) in an isolated tmux session the way a user does, and capture screen evidence. Use to prove any change to the day strip, stretch list, meeting/gap assignment, hand adjustments, events audit view or draft panel actually works on screen, not just compiles.
---

# Verify daystrip

The only user-facing surface in this repo is the daystrip prototype: a Bubble Tea TUI over one seeded fake day (Tue 10 Nov, three tickets PUN-142, OPS-77, PUN-150, two meetings). It keeps no files, makes no network calls and sends nothing to Jira, despite the `⏎ confirm & send` label. Every launch starts from the same seeded state, so runs never share or corrupt state.

All steps go through `.claude/skills/verify-daystrip/daystrip.sh` (run from anywhere inside the repo). It runs the app in its **own tmux server** (`tmux -L verify-daystrip-<run>`), so it cannot touch a tmux session the user has open, and two runs can drive side by side.

## Launch

```sh
S=.claude/skills/verify-daystrip/daystrip.sh
eval "$($S start | head -1)"     # builds ./cmd/prototype-daystrip, starts it at 120x36, exports DAYSTRIP_RUN
$S start 140 40                  # or pick a size; below 96x30 the app shows only a size guard
```

`start` returns once the screen shows `day · Tue 10 Nov` (or the size guard) and has stopped changing. That is ready: the app has no background work. It needs `go` (the module pins go 1.26; the toolchain downloads on first build) and `tmux`. Build failures print and exit non-zero; fix the build before driving anything.

## Doctor

```sh
$S doctor
```

Read-only. Checks that the tmux session exists, the app process is still running (it exits on `q`), the binary was built from the current checkout (`build <rev> matches checkout`; a `-dirty` suffix means uncommitted edits to `cmd/`, `go.mod` or `go.sum` at build time), the window size, and that the main screen is drawn. Run it first whenever a screen looks wrong. If the build is stale, `stop` and `start` again.

## Drive

```sh
$S keys j j t        # tmux key names, one per argument; waits until the screen settles
$S keys Enter        # same as t
$S screen            # print the current screen as plain text
$S resize 90 28      # resize the window (the app gets a WindowSizeMsg)
```

Keys the app binds: `j`/`l`/`Down`/`Right` next stretch, `k`/`h`/`Up`/`Left` previous, `t`/`Enter` assign the selected meeting or gap to the next ticket, `+`/`=` add 5m to the selected work stretch's ticket, `-` remove 5m, `e` toggle the events view, `q`/`C-c` quit.

Read the screen by text, not by position. Stable handles:
- the stretch counter in the stretches box title, `N/9`;
- the selected stretch row starts with `│›` and the detail line starts with `│ › `;
- the stretches footer row starts with `│ total`; the draft lines contain `to send · 3 worklogs → Jira` and `unlogged · meetings`;
- the events view title reads `events · HH:MM–HH:MM · <count>`;
- the size guard reads `PROTOTYPE: needs 96×30 or larger (now WxH)`.

The feature map in [`features/`](features/README.md) has a recipe per feature. Read its README first.

## Evidence

```sh
$S snap <name>       # writes .verify/daystrip/$DAYSTRIP_RUN/NN-<name>.txt (plain) and .ansi (with colour)
```

Every `start`, `keys`, `resize`, `snap` and `stop` is appended to `.verify/daystrip/$DAYSTRIP_RUN/actions.log`, so the log plus the snaps show the action and the resulting state. `.verify/` is gitignored. Proof standards:
- Drive the real binary with keys; never prove behaviour by calling Go functions or editing seed data.
- Snap before and after the action, and assert on the specific numbers that should change (and on the ones that should not).
- A number in one panel is backed up by the others: a ticket's total appears in the day strip label, the stretches `total` row and the draft. Check all three.
- Colour matters for the day strip (selection, meeting tint); inspect the `.ansi` snap with `cat` in a truecolour terminal when the change is visual.

## Cleanup

```sh
$S stop
```

Kills only this run's tmux server and removes its build directory under `${TMPDIR:-/tmp}/verify-daystrip-<run>`. Evidence in `.verify/daystrip/<run>/` stays. Run `stop` after every attempt, failed ones included. Never `pkill daystrip` or kill tmux by name: the user may be running their own copy.
