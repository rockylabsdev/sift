---
name: verify-daystrip-credit
description: Check that the daystrip prototype's draft (the worklogs it would send to Jira) credits each ticket exactly the minutes the day strip shows, in every reachable UI state and on generated days. Use after any change to derive, newModel/modelFor, Update, total, eff or stretchBox in cmd/prototype-daystrip/main.go, or to the derivation rules, meetings, gaps or hand adjustments.
---

# Verify daystrip credit

## What is verified

- **P1, no double or missing credit.** Each ticket's draft total equals the minutes the day strip credits to it, plus its hand adjustment, floored at 0m. *Harm if broken:* the worklogs sent to Jira bill time that was not worked, or drop time that was.
- **P2, the table adds up.** In the stretches table, each ticket's column adds up to the total printed under it. This is the promise in `stretchBox`'s comment. *Harm if broken:* the user checks the rows, sees they agree, and approves a total that differs from them.

"Credited by the day strip" means the minutes whose effective owner `model.eff` is that ticket: the solid counted bars on screen.

## How, and why this tool

Go has no verifier that runs on ordinary application code like this. So the check runs the **real code** in `cmd/prototype-daystrip/main.go` (`derive`, `modelFor`, `Update`, `total`, `eff`, `stretchBox`) under two kinds of input, in `cmd/prototype-daystrip/credit_verify_test.go` (build tag `verify`, so `go test ./...` skips it):

- **Exhaustive, seeded day** (`TestCreditSeededDay`, `TestColumnsSeededDay`). A breadth-first search over every UI state reachable from launch through every bound key: `left h up k right l down j e + = - t enter`. A state is the selection, the inspect level, the hand adjustments, and each meeting's and gap's assignment. The bound: hand adjustments stay within ±10m per ticket. That gives 432,000 states, all checked. The first failure is reported with the shortest key path that reaches it. The table's sums do not depend on the selection or inspect level, so `TestColumnsSeededDay` renders each distinct table once (one render costs about 0.8 ms, almost all of it lipgloss).
- **Generated days** (`TestCreditGeneratedDays`). 20,000 days from a fixed seed, so runs are reproducible. Each day has 1–60 events at any minute on any ticket and 0–3 non-overlapping meetings inside 08:00–20:00, some naming a ticket. Each day is driven by 40 random key presses, and P1 is checked after every press. Failures are shrunk to the few events, meetings and keys that matter. `FuzzCreditGeneratedDays` is the open-ended version (see below).
- **Overlapping meetings** (`TestCreditOverlappingMeetings`). The same as above, but meetings may overlap.

Treated as one step, as in the code: a single `Update` call per key.

## Run it

```sh
.claude/skills/verify-daystrip-credit/check.sh        # ~2 min; exit 0 = no new break
```

Each line reads `PASS`, `KNOWN FINDING` (still failing exactly as recorded below), `FIXED?` (a known finding stopped reproducing), or `FAIL` (new). For a deeper search after a risky change:

```sh
go test -tags verify -run XXX -fuzz FuzzCreditGeneratedDays -fuzztime 120s ./cmd/prototype-daystrip
```

A fuzz failure is saved under `cmd/prototype-daystrip/testdata/fuzz/` and replays on every later `check.sh` run. Commit it if it is a real bug, and delete it if you fix the model.

Tools: Go and its standard `testing` package only, at the module's Go version (`go.mod`).

## Known findings

None open. Two were found and fixed:

- **F1, overlapping meetings were billed twice.** `total` added each logged meeting's full `to - from`, while the day strip gives shared minutes to one meeting. Fixed by making `total` the minutes the strip credits (`countedAll`, one owner per minute) plus the hand adjustment. `TestCreditOverlappingMeetings` is now must-pass.
- **F2, hand adjustments broke the table's sums.** The `total` row included adjustments and the stretch rows did not. Fixed with a `by hand` row in the table, shown when an adjustment changed a total. `-` also stops at the floor now, so the draft's `by hand` mark always equals the change. Removing the row brings back the recorded counterexample (`after keys [+]: PUN-142: column adds up to 2h 58m, total row shows 3h 03m`).

Since F1's fix, P1 holds largely by construction: `total` and the strip both count `eff`. It still catches a `total` or `Update` that drifts from `eff`. P2 reads the rendered table, so it stays an independent check.

## Reading a result

- **`FAIL` on `TestCreditSeededDay` or `TestCreditGeneratedDays`.** The message names the ticket, the draft total, the strip's credit, the adjustment, and the keys (and day) that got there. Replay it by hand with the `verify-daystrip` skill. On the seeded day, press the listed keys and compare the stretches `total` row against the counted bars. If the screen agrees with the message, it is a real bug: report it and do not change product code unless asked.
- **A modelling error looks different.** The check itself breaks instead of finding a counterexample. Examples: `seeded day has N stretches, want the 9 the app shows` (the seeded day changed: update that number and the recorded counterexamples together), `reading row ...: not a duration` (the stretches table layout changed: update the column offsets in `checkColumns`), or a panic inside the test helpers. Fix the check, then rerun.
- **`FAIL ... fails differently than recorded`.** A known finding now fails with another counterexample. Either the seeded day or generator changed, so re-record the signature in `check.sh` and the text above, or a second bug now shows first.
- **`FIXED?`.** Confirm the fix by hand. Then move the test into the must-pass loop in `check.sh`, delete its entry here, and move the sync commit.

## When a change is intended

If you change what the draft should send (for example, meetings that deliberately count in full, or adjustments shown as their own table row), change P1 or P2 in `credit_verify_test.go` (`checkCredit`, `checkColumns`) and the property text above in the same commit. Then show the new check failing on a broken variant before trusting it (see below).

## Showing it can fail

Each must-pass check was seen failing on a scratch break, then the break was reverted:
- adding 1m to the second gap's credit in `total` → `TestCreditSeededDay` fails after `right ×7, t`;
- handing a meeting's first minute back to PUN-142 in `derive` → `TestCreditSeededDay` fails after `right t`;
- skipping the `by hand` row in `stretchBox` → `TestColumnsSeededDay` fails after `+`;
- dropping the day's last minute in `minutesOf` → `check.sh` exits 1, with `TestCreditGeneratedDays` shrunk to a single 19:55 PUN-150 event (the seeded day never reaches 20:00, so only the generated days catch it).

A scratch break that changes no behaviour passes, and should. Extending a gap's range by one minute in `eff` is one: that minute is never gap-owned, so the `o == gap` guard already excludes it. Before calling a miss a gap in the checks, confirm the break changes what the app shows.

Repeat one such break after any edit to the checks themselves.

## Not covered

- What the day strip *should* credit: the derivation rules themselves (idle gap, tail credit, midpoint split). P1 checks that the draft agrees with the strip, not that the strip is right.
- Hand adjustments beyond ±10m on the seeded day. The generated days reach any size, including the 0m floor.
- Meetings that start before 08:00 or end after 20:00, and days with no events (`derive` indexes `ev[0]` and panics on an empty day; the app never builds one).
- Rendering beyond the stretches table, the inspect view, and confirming or sending, which the prototype does not implement.

## Coverage and sync

Covers, in `cmd/prototype-daystrip/main.go`: `derive`, `day.minutesOf`, `newModel`, `modelFor`, `model.Update`, `model.total`, `model.ownedIn`, `model.eff`, `model.stretchBox`, `seedDay` and its helpers, and the `meetings`, `tickets`, `idleGap`, `tailCredit` and `dayMinutes` definitions.

**Sync commit: `c35d85d`.** At that commit the checks describe the code above. If `git diff c35d85d -- cmd/prototype-daystrip/main.go` touches any covered function, run `check.sh`, re-read the changed code against the properties, and only then move the sync commit to the new HEAD. Use `/maintain-formal-verification-skill` when the checks have fallen behind the code.
