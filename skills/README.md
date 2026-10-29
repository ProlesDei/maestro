# Skills

Reusable instructions for one task, one skill per folder. Two kinds, kept in separate folders:

| | Bot skills | Harness skills |
|---|---|---|
| Folder | `skills/bot/<name>/` | `skills/harness/<name>/` |
| Name | no prefix | `maestro-` prefix |
| Read by | the bot runtime, as part of every bot profile | a harness session in the target repo |
| Teaches | how to plan, prompt and judge | how to do one step: ticket CLI, files, git |
| Installed into the target repo | never | yes: generated per installed harness (decision 0143) |

Harness skills go to `.claude/skills/<name>/` for Claude Code and `.agents/skills/<name>/` for Codex and Grok Build, hidden from `git status` by `.git/info/exclude`. A session loads one only when the prompt names it; the runner (`docs/runner.md`) names it the way each harness expects. `skills/harness/rules.md` is installed as `.maestro/rules.md` and passed to every session in its launch flags.

## Format

Copy the `_template/` folder of the kind you're writing.

- **Frontmatter**
  - `name`: the folder name.
  - `description`: one sentence: what the skill does and when to use it.
  - `metadata.internal: true` on every skill, so third-party skill installers skip them.
  - Harness skills also set `disable-model-invocation: true` (Claude Code, Grok Build) and ship `agents/openai.yaml` with `policy.allow_implicit_invocation: false` (Codex), so they run only when named.
  - Add other fields only when a reader needs them.
- **Body**: what the skill is for, inputs, steps, the reply or output, rules.
- **Other files** in the folder support that skill. Installers copy the whole folder.

## Rules

- Folder names: lowercase, hyphens, matching `name`. Folders starting with `_` are not skills; readers and installers skip them.
- Harness skills rely on `.maestro/rules.md` and bot skills on `bot-profiles/team-rules.md`. Never restate a rule from either.
- Apart from those, the body stands alone. Don't send the reader to another skill for a step it needs.
- Harness skills reply in the structured format from `skills/bot/prompt-contract`.

## Current skills

Session-loop step names are from `docs/bot-anatomy.mermaid`. Playbook-step skills are named by the playbooks' **Skill:** lines.

| Skill | Kind | Used at |
|---|---|---|
| `maestro-check-intake` | Harness | Check intake |
| `maestro-claim` | Harness | Pick the playbook (claim the work) |
| `maestro-propose-memory` | Harness | Wrap up, when there's something durable to keep |
| `maestro-handoff-note` | Harness | Wrap up, handing the ticket on |
| `maestro-escalate` | Harness | Wrap up, when someone above must act |
| `maestro-log-entry` | Harness | Wrap up, last; the only wrap-up for scheduled duties |
| `maestro-clean-up` | Harness | Clean up, when the harness has no cleanup hook |
| `maestro-plan-change` | Harness | Playbook step: plan a build, find a bug's cause, plan an infrastructure request |
| `maestro-capture-baseline` | Harness | Playbook step: repro, captured behaviour or trace, before and after a change |
| `maestro-build-change` | Harness | Playbook step: build or fix on the ticket branch |
| `maestro-verify` | Harness | Playbook step: prove the change works, before submitting and at QA |
| `maestro-read-feedback` | Harness | Playbook step: read a send-back, an escalation or a report |
| `maestro-review-change` | Harness | Playbook step: code review |
| `maestro-investigate` | Harness | Playbook step: scout investigation, and fact-finding for plans and answers |
| `maestro-write-report` | Harness | Playbook step: a scout's report, a workstream spec |
| `maestro-write-tickets` | Harness | Playbook step: create or fix tickets |
| `maestro-groom-board` | Harness | Playbook step: find board problems |
| `maestro-check-release` | Harness | Playbook step: ship gates before release |
| `maestro-analyse-logs` | Harness | Playbook step: improvement loop measuring and pattern-finding |
| `maestro-propose-change` | Harness | Playbook step: improvement loop, adopt tuning or propose |
| `maestro-curate-memory` | Harness | Playbook step: memory curation |
| `maestro-housekeeping` | Harness | Playbook step: check a housekeeping plan |
| `maestro-draft-setup` | Harness | Playbook step: project setup, definition of done, verification guide |
| `runner` | Bot | Preflight; Start a harness session; every no-token step; Clean up (close the session) |
| `prompt-contract` | Bot | Next step; Check the structured reply; Result; Wrap up |
| `model-selection` | Bot | Next step (pick the model tier) |
| `talk-to-user` | Bot | Chief of Staff playbook steps that message the user |
