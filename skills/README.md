# Skills

This folder is yours. Maestro's playbooks, prompt templates and personas name no skill, so you can add, rename or remove skills here without editing Maestro. The skills here now are Paul's harness skills (decision 0780). They are separate from the bot personas in `bot-profiles/`.

## Layout

- `skills/<name>/SKILL.md`: one skill per folder, flat, in any format your harnesses or bot runtime read. Other files in the folder go with it.
- `skills/use.yaml`: optional. It says which skills get named, and where. If it's missing or a list is empty, nothing is named.

```yaml
prompts:                  # harness skills
  all: []                 # every prompt step
  build: [tdd]            # by template name in playbooks/prompts/, never by playbook
  review: [deep-review]
bots:                     # bot skills
  all: []
  senior-software-engineer: [architecture]
commands: [work]          # slash commands for the user, never named in a prompt
```

## How they're used

- **Prompts.** For each prompt step, `maestro-play` names `prompts.all` plus `prompts.<template>` right after `playbooks/prompts/_contract.md`, the way each harness expects (`docs/runner.md`).
- **Install.** `scripts/install.sh` copies every skill folder here into each harness's skill folder in the target repo, hidden from git by `.git/info/exclude` (decision 0882): `.claude/skills/<name>/SKILL.md` for Claude Code, and `.agents/skills/<name>/SKILL.md` for Codex and Grok (decision 0143). Nothing else here is installed.
- **Bots.** Bot setup adds `bots.all` plus `bots.<role>` to that bot's instructions.
- **One list.** `use.yaml` is global. A project can't override it (decision 0701).
- **Checked.** `maestro-play lint` fails on any listed skill that has no folder here.

## Slash commands

An installed skill is a command you call by name: `/<name>` in Claude Code, `$<name>` in Codex. Grok Build finds the same folders (decision 0140), and its headless prompts name a skill by its path. Skills under `commands` are manual-only, so they run only when you call them: `disable-model-invocation: true` in the frontmatter for Claude Code and Grok, and `agents/openai.yaml` with `policy.allow_implicit_invocation: false` for Codex, which ignores the frontmatter key (decision 0143). `evals/check.py` fails a skill that has the first without the second, and `maestro-play lint` fails a `commands` skill that lacks either, or that a `prompts` skill tells a step to call (decision 0788). Skills under `prompts` set neither, because a playbook step has to be able to load them.

Two commands are workflow entry points, not procedures. They hold no workflow logic of their own: that stays in the playbooks and the engine.

- `/maestro-work [ticket id]` drives one ticket from `todo` to merged. It runs the playbook chain through `maestro-play` (build, review, QA, then release), relays each `ask` step to you, and stops at a block, a refusal or a PR waiting for a human merge.
- `/maestro-create-ticket [what needs doing]` makes one `maestro-ticket create` call. It asks once for type, kind, role, owner, deps and areas, then hands you the id for `/maestro-work`.

To try them in a clone of this repo without installing, copy the folders by hand: `cp -r skills/maestro-work skills/maestro-create-ticket .claude/skills/`. Use `.agents/skills/` for Codex and Grok, and list the copies in `.git/info/exclude` so they stay out of commits.
