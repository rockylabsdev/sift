# Sift
A terminal worklog drafter. Your work, accounted for.

Sift records evidence as you work, derives your day from it, and drafts one worklog per ticket for you to check and send. Nothing is started or stopped.

## Install

```sh
go install ./cmd/sift
claude plugin marketplace add rockylabsdev/sift
claude plugin install sift@sift
```

Each prompt you type in Claude Code is now an event for the ticket its directory names: a `.sift` file (first line, e.g. `PUN-142`), else the branch name. An agent's own activity is never counted.

Try it before any of that with `sift demo`, which opens the review on a seeded fake day and keeps nothing.

## Use

```sh
sift                   # review the earliest day waiting, else today
sift status            # today's totals and the days waiting
sift confirm           # accept a day: one worklog per ticket, sent
sift log               # this week's worklogs
```

In the review, `↑↓` selects a stretch, `t` assigns a meeting, gap or ticketless time to a ticket, `+ −` adjust a ticket by 5 minutes, `e` inspects a stretch (its events, where each ticket came from, and silences close to the idle gap), `c` confirms, `[ ]` change day and `T` changes theme (Graphite, Dusk, Paper).

Work no collector saw: `sift block PUN-142 10:00-11:30`. In Claude Code, `/sift` shows the draft and confirms it with your yes. An agent cannot confirm a day on its own: `sift confirm` from an agent only proposes the draft, and the plugin puts `sift confirm --yes` to you.

## Connect

```sh
sift setup jira --site acme.atlassian.net --email you@acme.com   # reads the API token from stdin
sift setup calendar --email you@acme.com                         # reads the iCal subscription address from stdin
sift setup none                                                  # no tracker: confirmed worklogs stay local
sift import punchcard                                            # keep punchcard's worklog history in sift log
```

Secrets go to the macOS Keychain, else a 0600 file under `~/.config/sift/secrets`. Meetings leave out all-day events, declined and cancelled invitations, events marked free, and events with no one else invited; `sift draft` lists what it left out and why.

Settings live in `~/.config/sift/config.json`: `idle_gap_minutes` (default 10), `round_minutes` (0, 5 or 15), `events_beat_meetings`, `projects` (the branch rule takes only these projects' keys) and `theme`. The event format is a public contract, described in [docs/evidence.md](docs/evidence.md).

The design prototype the review grew from is still in `cmd/prototype-daystrip`.

## Claude Code skills

`.claude/` vendors the Claude Code build of [pstack](https://github.com/cursor/plugins/tree/main/pstack)
(MIT, see `.claude/LICENSE.pstack`), copied from `cursor/plugins` at commit `2eaf465`, so its skills
load in every Claude Code session for this repo, including cloud sessions. As project skills they have
no `pstack:` prefix: use `/setup-pstack`, `/poteto-mode`, `/poteto-help`, and so on. To update, re-copy
`harness/pstack/claude-code/{skills,agents,docs,README.md}` from that repo.
The copy leaves out `skills/statechart/scripts/test/` (pstack's own test suite and its TLC
output corpus, ~16k lines) and the `test` script in that skill's `package.json`; the skill does not
need them to run.
`/setup-pstack` model choices live in `.claude/rules/pstack-models.md` (committed, so cloud sessions
keep them) rather than `~/.claude/rules/`; re-run `/setup-pstack` and save the result there to change them.

Project-specific verification skills (not part of pstack):
- `/verify-daystrip` drives the daystrip prototype TUI in tmux and records screen evidence (`.verify/`, gitignored).
- `/verify-daystrip-credit` checks that the draft credits every ticket exactly what the day strip shows (`check.sh`, ~30s).
