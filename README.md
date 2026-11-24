# Sift
A terminal worklog drafter. Your work, accounted for.

Sift records evidence as you work, derives your day from it, and drafts one worklog per ticket for you to check and send. Nothing is started or stopped.

## Install

```sh
brew install rockylabsdev/tap/sift
claude plugin marketplace add rockylabsdev/sift
claude plugin install sift@sift
```

The plugin is pinned to the release of the binary, so update both together: `brew upgrade sift`, then `claude plugin update sift@sift`. Without Homebrew, `go install github.com/rockylabsdev/sift/cmd/sift@latest` works too.

Each prompt you type in Claude Code is now an event for the ticket its directory names: a `.sift` file (first line, e.g. `PUN-142`), else the branch name.

Try it before any of that with `sift demo`, which opens the review on a seeded fake day and keeps nothing.

## Test a checkout

```sh
scripts/sandbox.sh                # a shell with this checkout's sift and plugin, and throwaway data
scripts/sandbox.sh sift demo      # or one command
SIFT_SANDBOX=~/sift-try scripts/sandbox.sh   # keep the sandbox between runs
go test ./...
```

Inside the sandbox, `claude` loads this checkout's plugin, and `sift setup jira` or `sift setup calendar` connect real accounts without touching your everyday config. The sandbox keeps secrets in its own config directory, never in the Keychain, so your real token and calendar address are never overwritten.

## Release

`scripts/release.sh 0.2.0` (or `0.2.0-rc.1` for a pre-release that skips the tap) pins the plugin to the tag, commits and tags on `main`. Pushing the tag runs CI, then GoReleaser publishes the tarballs and the Homebrew formula. It needs a `rockylabsdev/homebrew-tap` repository and a `HOMEBREW_TAP_GITHUB_TOKEN` secret: a fine-grained token with Contents read and write on that repository only.

## Use

```sh
sift                   # review the earliest day waiting, else today
sift status            # today's totals and the days waiting
sift confirm           # send what the day has so far: one worklog per ticket
sift log               # this week's worklogs
```

In the review, `↑↓` selects a stretch, `t` assigns a meeting, gap or ticketless time to a ticket, `x` skips a meeting you were not in, so its time goes back to your prompts (press it again to count the meeting), `r` moves one conversation's time in a stretch to another ticket (and, on time it moved, puts it back), `+ −` adjust a ticket by 5 minutes, `e` inspects a stretch (its events, where each ticket came from, and silences close to the idle gap), `c` sends what is ready, `[ ]` change day and `T` changes theme (Graphite, Dusk, Paper).

Started a conversation in an old worktree whose branch names the wrong ticket? Press `r` on its stretch in the review, or type `/sift retag PUN-160` in it, and that conversation's time on the old ticket counts for PUN-160, both what is logged and what comes after. `/sift retag PUN-160 from 9:45` moves only the time from then on. Other conversations, and helpers working on their own tickets, keep theirs. `sift retag --list` shows the retags made.

Confirming sends what the day has so far and keeps the day open: time up to then is sent and locked (drawn grey in the review, one `sent` row in the list), and work after it builds a new draft that the next confirm sends as new worklogs. Worklogs already sent are never edited; to change sent time, change it in Jira. A day sent in part waits for review until what came after is sent too.

Work no collector saw: `sift block PUN-142 10:00-11:30`. In Claude Code, `/sift` shows the draft and confirms it with your yes. An agent cannot confirm a day on its own: `sift confirm` from an agent only proposes the draft, and the plugin puts `sift confirm --yes` to you.

## Connect

```sh
sift setup jira --site acme.atlassian.net --email you@acme.com   # reads the API token from stdin
sift setup calendar --email you@acme.com                         # reads the iCal subscription address from stdin
sift setup none                                                  # no tracker: confirmed worklogs stay local
sift import punchcard                                            # keep punchcard's worklog history in sift log
```

Secrets go to the macOS Keychain, else a 0600 file under `~/.config/sift/secrets`. Meetings leave out all-day events, declined and cancelled invitations, events marked free, and events with no one else invited; `sift draft` lists what it left out and why. A published Outlook calendar names no attendees on any event, so there every event counts as a meeting, subject to the other rules, and `sift draft` says so. `calendar_no_attendees` (`meeting` or `skip`) decides this for events that name no one, whatever the feed.

Settings live in `~/.config/sift/config.json`: `idle_gap_minutes` (default 10), `round_minutes` (0, 5 or 15), `events_beat_meetings`, `projects` (the branch rule takes only these projects' keys), `agent_time` (`fill`, `workday` or `off`), `calendar_no_attendees` (`meeting` or `skip`) and `theme`.

Agents count too. While subagents or a long agent run work through a spec and you are not prompting, their activity fills that otherwise silent time, credited to the ticket of the session that started them, even in worktrees whose branches name no ticket. Your own prompts always come first, and agents running at once share the time rather than adding to it. The review draws agent time as `┅`. The event format is a public contract, described in [docs/evidence.md](docs/evidence.md).

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
