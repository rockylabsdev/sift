---
description: Show a day's sift draft and confirm it
argument-hint: "[YYYY-MM-DD]"
allowed-tools: Bash(sift draft:*), Bash(sift status:*)
---

Show the user the sift draft for $ARGUMENTS (today if empty, or the day `sift status` says is waiting).

1. Run `sift draft --md` (add `--date $ARGUMENTS` when a date was given) and show its output as it is. Do not round, merge or reword the numbers.
2. Point out anything unassigned: meetings, gaps, or time with no ticket. Say that `sift` opens the review screen to assign or adjust it.
3. Ask whether to confirm. Only if the user says yes, run `sift confirm --yes` with the same `--date`. Sift will ask them once more before anything is sent. Never run it without their yes.
4. Report what `sift confirm` printed. If it exits 7, the worklogs are kept and `sift sync` sends them later.
