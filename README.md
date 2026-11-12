# Sift
A terminal worklog drafter

## Claude Code skills

`.claude/` vendors the Claude Code build of [pstack](https://github.com/cursor/plugins/tree/main/pstack)
(MIT, see `.claude/LICENSE.pstack`), copied from `cursor/plugins` at commit `2eaf465`, so its skills
load in every Claude Code session for this repo, including cloud sessions. As project skills they have
no `pstack:` prefix: use `/setup-pstack`, `/poteto-mode`, `/poteto-help`, and so on. To update, re-copy
`harness/pstack/claude-code/{skills,agents,docs,README.md}` from that repo.
