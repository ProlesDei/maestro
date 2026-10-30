# Skills

Maestro ships 18 skills. Each has the `maestro-` prefix, so none clashes with a team's own skills. You can call any of them by name: `/maestro-tdd` in Claude Code, `$maestro-tdd` in Codex.

| Group | Skills |
|---|---|
| Ticket to merge | `maestro-work` (ticket to open PR), `maestro-create-ticket` (one ticket; a team can swap in its own with `scripts/install.sh --tickets <dir>`), `maestro-review` (merge and close, or kick back) |
| Roles | `maestro-planner`, `maestro-qa-lead`, `maestro-software-engineer`, `maestro-chief-of-staff` |
| Working tools | `maestro-figure-it-out`, `maestro-swarm`, `maestro-sharpen`, `maestro-recall`, `maestro-unslop`, `maestro-prune`, `maestro-refactor-code`, `maestro-technical-writing` |
| Named in prompt steps | `maestro-working-principles` (every step), `maestro-tdd` (build), `maestro-prove-complete` (verify) |

To add your own, drop a `skills/<name>/SKILL.md` folder here. `scripts/install.sh` copies every folder into the target repo's `.claude/skills/` and `.agents/skills/`.

`use.yaml` says which skills `maestro-play` names at the top of each prompt step: `prompts.all` for every step, `prompts.<template>` for one. `commands` only marks the skills a prompt step must never load: a person calls them, never the model on its own. Each needs `disable-model-invocation: true` in its frontmatter and `agents/openai.yaml` with `policy.allow_implicit_invocation: false`; `maestro-play lint` checks both.
