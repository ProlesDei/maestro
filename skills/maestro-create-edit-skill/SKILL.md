---
name: maestro-create-edit-skill
description: "Create, edit, rename or retire a skill so Claude Code, Codex and Grok Build all find it and a person can keep it up. Use for /maestro-create-edit-skill, 'add a skill', 'write a SKILL.md', 'this skill never triggers', 'this skill triggers too often', or when you change a skill's description. Not for using a skill, or for code and docs that are not skills."
argument-hint: "[what the skill should do, or its name]"
disable-model-invocation: true
---

# Create or edit a skill

A skill is a folder with a `SKILL.md`. A harness reads only `name` and `description` until a task matches, then loads the body. You hand back a skill that triggers when it should, and you never edit a `maestro-*` folder: those are the installer's copies, hidden from git, and a reinstall drops your change.

## Start

Open a todo list with one item per phase below.

## Phase A: Decide

1. List `.claude/skills/`, `.agents/skills/` and any folder `## Skills` in `.maestro/project/project-setup.md` names, and read each `description`.
2. Pick one: an existing skill covers it, so **edit** that one; the agent already does this well, so **stop** and say so; else **create** one.
3. Give the reason in one sentence before you write: "No skill covers commit style, so I'm creating `commit-style`."
4. Name it in lowercase letters, digits and single hyphens, 64 characters at most, never with the `maestro-` prefix. To change a Maestro skill, change it in the Maestro repo, where `skills/README.md` sets the format.

## Phase B: Write the frontmatter

1. Set `name` to the folder name.
2. Write `description` on one line, in double quotes, 1024 characters at most: what it does, verb first; when to use it (the slash command, two or three phrases a user says, the moment it applies); then `Not for ...`.
3. For a command only a person starts, add `argument-hint: "<args>"` and `disable-model-invocation: true`, and write `agents/openai.yaml` with `policy:` / `allow_implicit_invocation: false` so Codex never starts it. If playbook steps run here, also list it under `commands` in the `use.yaml` from Phase D.

## Phase C: Write the body

1. Open with one or two sentences: the job, what it hands back, and the one thing it never does.
2. Use `## Phase A: <verb>` headings for work in stages, or one numbered list. One imperative action per step. Write each command exactly as it runs.
3. Add only what the agent doesn't know. Link a sibling skill (`../<name>/SKILL.md`) instead of restating its rules.
4. Name the action, not the tool: "edit the file", not "use the Edit tool". Give a fallback for a feature one harness lacks, such as subagents or hooks.
5. Keep `SKILL.md` under 60 lines. Move long detail to `references/<file>.md` and say when to read it. Check every link resolves.
6. Put scripts in `scripts/`, in plain Bash with no agent APIs, and give them tests.
7. End with a bold `**Reply:**` line naming the parts of the answer, in order.
8. Write in plain words per the **maestro-technical-writing** skill ([SKILL.md](../maestro-technical-writing/SKILL.md)).

## Phase D: Place it

1. Read `## Skills` in `.maestro/project/project-setup.md`. If it says where skills live, place it there, follow it for the steps below, and skip step 2.
2. Else keep the same bytes in `.agents/skills/<name>/` (Codex, Grok Build) and `.claude/skills/<name>/` (Claude Code). Edit the `.agents` copy, then run `mkdir -p .claude/skills/<name> && cp -R .agents/skills/<name>/. .claude/skills/<name>/` and `diff -r .agents/skills/<name> .claude/skills/<name>`, which must print nothing.
3. To rename or retire, remove the old folders and links, run `git grep <old-name>`, and fix every hit.
4. For a skill playbook steps should load: put it in the skills folder `maestro-play` reads (`$MAESTRO_SKILLS`, else `<playbooks>/../skills`), list it under `prompts` in that folder's `use.yaml`, and run `maestro-play lint`. Such a skill never tells a step to call a `/command`.

## Phase E: Test it

1. Run the tests for any script you added or changed. Skill text gets no tests.
2. Trial it in a fresh session, such as `claude -p "<prompt>"` or `codex exec "<prompt>"`: one prompt that should load the skill, one nearby prompt that shouldn't. It passes when it loads on the first, stays out of the second, and the agent follows the steps without help. For a command, run `/<name>` once instead.
3. If it never loads, sharpen the description's trigger phrases. If it loads too often, add to `Not for ...`. Trial it again.
4. Put both prompts and their results in the PR body or commit message.

**Reply:** the decision and its reason, the folders and links written or removed, the `diff -r` result if you made copies, and the two trial prompts with what happened.
