# Role profiles

One folder per role. Each `persona.md` says what that role does: who it is, how it thinks and which jobs and skills it owns. Code serves six of the roles and the chief of staff runs in T3, so no session loads a persona; read them to understand the team. The one file code uses is `chief-of-staff/t3.md`: `scripts/install.sh chief` copies it as the chief project's `AGENTS.md` and `CLAUDE.md`.

- `<name>/persona.md`: the role. Frontmatter holds `name` (the folder name) and `description`. The rules every role shares live in [team-rules.md](team-rules.md).
- The folders are seven profiles for the six ticket roles: `chief-of-staff` (talks to the owner, leads through the planner), `planner` (runs the team day to day and `/maestro-planner`), `senior-engineer` and `junior-engineer` (both build tickets as the `engineer` role, on different models), `qa-lead` (runs `/maestro-qa-lead`), `reviewer` (runs `/maestro-code-review`) and `designer` (runs `/maestro-designer`). Many sessions run one role at once; don't add a folder for more.
- What a session does when a job runs lives in that job's skill under `skills/`. Name the skill; never restate it.
- Write in second person. Name a team rule; never restate it.
