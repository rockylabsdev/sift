# Themes

The screen comes in three colour themes: graphite, dusk and paper. `T` cycles them in that order and wraps. The footer names the current theme after `T`. The `SIFT_THEME` environment variable picks the theme the app starts in.

## Sub-features

- `theme-cycle` moves graphite to dusk to paper and back to graphite.
- `theme-footer` names the current theme in the footer.
- `theme-colour` changes the ticket hues, the selected-row background and the borders.
- `theme-env` starts in the theme `SIFT_THEME` names.

## How to get to it (user POV)

- Press `T`.
- Launch with `SIFT_THEME=dusk` (or `paper`, `graphite`).

## Driving it with daystrip.sh

Preconditions:

- Fresh baseline. The footer reads `T graphite`.

- **Cycle.** Run `daystrip.sh keys T` three times, snapping after each. The footer reads `T dusk`, then `T paper`, then `T graphite`. No number or text other than the footer name changes.
- **Colour.** In each `.ansi` snap, the selected-row background and the PUN-142 hue differ by theme. Count them with `grep -c` on the snap.

  | theme | selected-row background | PUN-142 hue |
  | --- | --- | --- |
  | graphite | `48;2;35;43;40m` | `38;2;181;201;155m` |
  | dusk | `48;2;39;41;57m` | `38;2;170;188;224m` |
  | paper | `48;2;227;229;216m` | `38;2;82;109;63m` |

  Each snap holds its own theme's codes and none of the other two.
- **Env.** Stop the run, then `eval "$(SIFT_THEME=paper daystrip.sh start | head -1)"`. The footer reads `T paper` and the `.ansi` snap holds `48;2;227;229;216m`. `daystrip.sh keys T` then reads `T graphite`.
- **Proof.** Snap each theme with `daystrip.sh snap theme-<name>`.

## Gotchas

- The page background is sent as the terminal's default colour, not drawn per cell, so the `.ansi` snap does not show it. Prove the theme with the selected-row background, the hues or the footer name.
- `SIFT_THEME` only takes effect on the `start` that creates the run's tmux server. An unknown name falls back to graphite.
