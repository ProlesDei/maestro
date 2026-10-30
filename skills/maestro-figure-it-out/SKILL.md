---
name: maestro-figure-it-out
description: "Use when the user types /maestro-figure-it-out <task> because no Maestro playbook fits the task. Designs a playbook for it that someone else can run and audit, lints and renders it, and runs it only if the user says so."
argument-hint: "<the task>"
disable-model-invocation: true
---

# Figure it out

The output is a playbook in Maestro's YAML format, not an improvised session. `<maestro>` is the folder that holds `playbooks/` and `tools/`; `$MAESTRO_PLAYBOOKS` names its `playbooks/`.

## 1. Prove nothing fits

List the playbooks (`ls <maestro>/playbooks/*/`) and read each `description:` and `intake:`. Name the closest one and say why it doesn't fit: the wrong ticket kind, a missing step, or no role that owns the task. If it fits with another `Type:` line on the ticket, stop and say so. That is better than a new playbook.

## 2. Frame the task

Before you design anything, write down the goal, the done criteria (each one checkable), the inputs, the outputs and where they land, the owning role (`chief-of-staff`, `engineer` or `reviewer`), each action that can't be undone, and each call a human must make.

## 3. Pick a type for each step

Read `<maestro>/playbooks/README.md` and start from a copy of `<maestro>/playbooks/engineer/work.yaml`.

| If the step | Use |
|---|---|
| reads or writes a ticket | `ticket:`; the engine adds the lease and the idempotency key |
| runs `worktree add`, `check all`, `head` or `open pr` | `run:` |
| decides from a value, such as a sha or a reply field | `gate:`, a template that renders true or false; it costs no tokens |
| needs a human choice | `ask:` with a `question` and its `choices` |
| needs judgment | `prompt:`, reusing a template in `<maestro>/playbooks/prompts/`, with `with: {mode: ...}` if one fits |

Write a new prompt template only when none fits, in the shape `playbooks/README.md` gives. For each prompt step, `out:` lists the reply fields the next step needs, `retry:` asks once more on a missing field, `on:` routes every outcome, and `model:` stays `mid` unless the job needs `frontier`.

## 4. Make it auditable

- Put a `gate` before each action that can't be undone: a merge, a deploy, a delete, a call outside the repo. Add an `ask` there when a human must agree.
- Give every `when:` a `reason:`, so the run state logs each skip.
- Loop back to an earlier step through `on:`, and let `.limits.step_visits` cap it. Never keep a counter in the playbook.
- Never name a skill or a model id in a playbook or a template.

## 5. Check it

Run `maestro-play lint <dir>`; it must pass. For each prompt step, run `maestro-play render <role>/<job> --step <id> --ticket <id>` and read the prompt as the session will see it.

## 6. Deliver

In the Maestro repo, commit `playbooks/<role>/<job>.yaml` and any new template on a branch for review. Elsewhere, write the draft to the scratch folder. Run it with `maestro-play run <role>/<job> --ticket <id> --playbooks <dir>` only when the user says so. If they want it done by hand, follow its steps and log each one as the engine would.

Reply: the closest playbook and why it doesn't fit, the new playbook's path, the step graph (one line per step: id, type, each outcome and where it goes), the lint and render results, and the open questions.
