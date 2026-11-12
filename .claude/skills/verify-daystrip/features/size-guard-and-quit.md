# Size guard and quit

Below 96x30 the app replaces the whole screen with a one-line size message, and restores the full screen as soon as the terminal is large enough. `q` leaves the app.

## Sub-features

- `size-guard` shows the needed and current size below 96x30.
- `size-restore` redraws the full screen, state intact, once large enough.
- `quit` exits with status 0.

## How to get to it (user POV)

- Resize the terminal.
- Press `q` or `Ctrl-C`.

## Driving it with daystrip.sh

Preconditions:

- Fresh baseline.

- **Guard.** Run `daystrip.sh keys j j` then `daystrip.sh resize 90 28`. The screen's first line reads `PROTOTYPE: needs 96×30 or larger (now 90×28)` and nothing else is drawn.
- **Restore.** Run `daystrip.sh resize 120 36`. The main screen returns with `3/9` still selected.
- **Quit.** Run `daystrip.sh keys q`. The screen reads `[daystrip exited 0]` and `daystrip.sh doctor` reports `FAIL app process not running`.
- **Proof.** Snap each state with `daystrip.sh snap size-<step>`.

## Gotchas

- `start 90 28` launches straight into the guard; `doctor` reports it as `warn size guard shown`.
- After `q` the tmux session stays open to show the exit line; still run `daystrip.sh stop`.
