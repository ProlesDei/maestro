# Skills

Maestro ships 17 skills. Each has the `maestro-` prefix, so none clashes with a team's own skills. Call any of them by name: `/maestro-sharpen` in Claude Code, `$maestro-sharpen` in Codex.

| Group | Skills |
|---|---|
| Ticket to merge | `maestro-create-ticket` (one ticket), `maestro-code-review` (record a verdict against the reviewed head and criteria) |
| Roles | `maestro-planner`, `maestro-qa-lead` (direct or full QA before merge), `maestro-chief-of-staff`, `maestro-designer` (formal design when requested or justified) |
| Working tools | `maestro-swarm`, `maestro-sharpen`, `maestro-recall`, `maestro-prune`, `maestro-refactor-code`, `maestro-technical-writing`, `maestro-create-edit-skill` |
| How to build | `maestro-working-principles`, `maestro-software-engineer`, `maestro-prove-complete`, `maestro-tdd` (only when a person asks for tests) |

The skills live here in `skills/`. [INSTALL.md](../INSTALL.md) says how a team installs them and where its own skills go.

A skill only a person starts needs `disable-model-invocation: true` in its frontmatter and `agents/openai.yaml` with `policy.allow_implicit_invocation: false`.

See [maestro-create-edit-skill](maestro-create-edit-skill/SKILL.md) for the skill format.
