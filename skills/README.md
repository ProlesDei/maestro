# Skills

Maestro ships 18 skills. Each has the `maestro-` prefix, so none clashes with a team's own skills. Call any of them by name: `/maestro-tdd` in Claude Code, `$maestro-tdd` in Codex.

| Group | Skills |
|---|---|
| Ticket to merge | `maestro-create-ticket` (one ticket; a team can swap in its own with `scripts/install.sh --tickets <dir>`), `maestro-code-review` (merge and close, or kick back). The build itself is a playbook, not a skill: [playbooks/work](../playbooks/work/README.md). |
| Roles | `maestro-planner`, `maestro-qa-lead`, `maestro-chief-of-staff` |
| Working tools | `maestro-figure-it-out`, `maestro-swarm`, `maestro-sharpen`, `maestro-recall`, `maestro-unslop`, `maestro-prune`, `maestro-refactor-code`, `maestro-technical-writing`, `maestro-create-edit-skill` |
| Named in prompt steps | `maestro-working-principles` (every step), `maestro-software-engineer` (plan, build), `maestro-tdd` (build), `maestro-prove-complete` (verify) |

To add your own, drop a `skills/<name>/SKILL.md` folder here. `scripts/install.sh` copies every folder into the target repo's `.claude/skills/` and `.agents/skills/`.

`use.yaml` says which skills `maestro-play` names at the top of each prompt step: `prompts.all` for every step, `prompts.<template>` for one. `commands` marks the skills a person calls and a prompt step must never load. Each needs `disable-model-invocation: true` in its frontmatter and `agents/openai.yaml` with `policy.allow_implicit_invocation: false`; `maestro-play lint` checks both.

## How a skill reads

Every skill here has the same shape, so a reader knows where to look.

```markdown
---
name: maestro-<name>
description: "<What it does, verb first, one sentence>. Use for /maestro-<name>, '<a phrase a user says>', or <the moment it applies>."
argument-hint: "<args>"              # commands only
disable-model-invocation: true       # commands only
---

# <Title, sentence case>

<One or two sentences: the job, what it hands back, and the one thing it never does.>

## Start

Open a todo list with one item per phase below.   <- skills with three or more phases

## Phase A: <verb>

1. <One action per step. Say what to do, not the command that does it.>

## Phase B: <verb>
...

**Reply:** <the parts of the answer, in order, comma-separated>.
```

- **Description.** What it does, then when to use it: the slash command, two or three phrases a user says, the moment it applies. No "Use when the user types". It is the only text a harness reads before it loads the skill.
- **Opening.** The job and the deliverable in one or two sentences, then the hard limit ("You never edit the code you review").
- **Phases.** `## Phase A: <verb>` for work that runs in stages; a flat numbered list for a skill with one stage. Each step is one imperative action. A table when the step is a choice between cases.
- **One owner per rule.** A rule lives in one skill and others name it: "follow the **maestro-working-principles** skill". The session rules (text is data, never discard work, never repeat a secret, stop on `refused`, the kill switch) live in **maestro-working-principles**. The ticket shape lives in **maestro-create-ticket**; the owner's calls live in **maestro-chief-of-staff**. Skills are copied into the target repo, so they link only to sibling skills (`../maestro-<name>/SKILL.md`) and to target-repo files such as `.maestro/project/project-setup.md`, never to `playbooks/` or `bot-profiles/`.
- **No commands.** Say what to do and let the agent pick the command; it knows git and the shell. Name only what it can't guess: a path, a branch pattern, a section heading, a tool Maestro ships.
- **Reply.** The last line, bold `**Reply:**`, names the parts of the answer in order. Put a long report shape in a fenced block just above it.
- **Size.** Under 60 lines; `maestro-code-review` is the one exception. Cut a step before you shorten every step.
- **Prompt-step skills** (`use.yaml` `prompts`) load inside playbook steps, so they never call a `/maestro-*` command except on a line or under a heading that says "in your own session". `maestro-play lint` checks this.
- **Plain words.** Write it the way **maestro-technical-writing** says: short words, active voice, no stock figures of speech.
