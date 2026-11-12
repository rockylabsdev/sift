# Sift
A terminal worklog drafter

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
