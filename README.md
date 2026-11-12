# Sift
A terminal worklog drafter. Your work, accounted for.

Sift records evidence as you work, derives your day from it, and drafts one worklog per ticket for you to check. Nothing is started or stopped.

## Try it

```sh
go install ./cmd/sift
claude plugin marketplace add rockylabsdev/sift   # or: claude --plugin-dir ./plugin
claude plugin install sift@sift
```

Each prompt you type in Claude Code is now an event for the ticket its directory names (a `.sift` file, else the branch). Then:

```sh
sift events            # today's evidence
sift draft             # the worklogs today would produce
sift draft --date 2026-11-10 --json
```

Sift drafts and never sends yet. Review, confirm, the calendar and Jira come in later slices. The event format is a public contract, described in [docs/evidence.md](docs/evidence.md).

The review screen is still a prototype over a fake day: `go run ./cmd/prototype-daystrip` (96×30 or larger, truecolour; `T` changes theme).

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
