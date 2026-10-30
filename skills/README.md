# Skills

This folder is your slot. Maestro ships three skills, all commands you call by name (`/maestro-work` in Claude Code, `$maestro-work` in Codex). The `maestro-` prefix keeps them from clashing with a team's own skills:

- `maestro-work/`: take one ticket to an open PR through `maestro-play run engineer/work`.
- `maestro-create-ticket/`: add one ticket with `maestro-ticket create`. A team can install its own method in its place (`scripts/install.sh --tickets <dir>`).
- `maestro-review/`: review a PR, then merge it and close the ticket, or send it back.

To add your own, drop a `skills/<name>/SKILL.md` folder here. `scripts/install.sh` copies every folder into the target repo's `.claude/skills/` and `.agents/skills/`.

`use.yaml` says which of your skills `maestro-play` names at the top of each prompt step: `prompts.all` for every step, `prompts.<template>` for one. Skills under `commands` are for you to call, so a prompt never names one. Each needs `disable-model-invocation: true` in its frontmatter and `agents/openai.yaml` with `policy.allow_implicit_invocation: false`; `maestro-play lint` checks both.
