---
name: maestro-figure-it-out
description: Use when no bundled playbook fits the task. Designs a rigorous, auditable playbook for it in Maestro's YAML format, with deterministic steps where a check can decide, prompt steps only for judgment, gates before anything irreversible, and evidence at every step. Lints it, and runs it only if you say so.
argument-hint: "<the task>"
disable-model-invocation: true
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Figure it out

The output is a playbook that someone else could run and audit, not an improvised session. `<maestro>` is the Maestro install: the folder holding `playbooks/` and `tools/`.

## 1. Prove nothing fits

List the playbooks (`ls <maestro>/playbooks/*/`) and read the `intake:` and `description:` lines. Name the closest one and say exactly why it doesn't fit: the wrong kind, a missing step, or no role that owns the task. If it fits with a different ticket kind or a `Playbook:` body line, stop and say so. That beats a new playbook.

## 2. Frame the task

Write these down before you design anything:
- the goal
- the done criteria, each checkable
- the inputs
- the outputs and where they land
- who owns it (a role in `bot-profiles/`)
- what's irreversible
- what needs a human decision
- the budget

## 3. Choose a type for each step

Read the format in `<maestro>/playbooks/README.md` and start from `<maestro>/playbooks/_template/playbook.yaml`.

| If the step | Use |
|---|---|
| reads or writes a ticket | `ticket:` (the engine adds the holder, token and idempotency key) |
| runs a command the runner allows | `run:` |
| decides from a value (a sha, a trust level, a reply field) | `gate:`, which costs no tokens |
| waits for something outside (CI, a merge) | `wait:` with `every` and `timeout` |
| needs a human choice | `ask:` with the choices and the evidence paths |
| needs judgment | `prompt:`, reusing a template in `<maestro>/playbooks/prompts/` with a `mode:` if one fits. Write a new template only if none does |

For each prompt step: `out:` lists the reply fields the next step needs, `retry:` gives one retry on a schema miss, `on:` routes every outcome, and `model:` gives the cheapest tier that can do the job.

## 4. Make it auditable

- Put a `gate` in front of every irreversible action: merge, deploy, delete, an external call. Put an `ask` there when a human must agree.
- Every step leaves a record: run state, an evidence file or a note. Every `when:` has a `reason:`, so the skip gets logged.
- Loops go back to an earlier step through `on:`, capped by `.limits.step_visits`, never by a counter in the playbook.
- Never name a skill or a model ID in the playbook.

## 5. Check it

- `maestro-play lint <dir>`: it must pass.
- `maestro-play render <role>/<job> --step <id> --ticket <id>` for each prompt step. Read every rendered prompt as the session will see it.
- Write a golden task as `evals/<role>/<job>.md` per `<maestro>/evals/README.md`: the input, the expected step path, and what proves it passed.

## 6. Deliver

- **In the Maestro repo:** `playbooks/<role>/<job>.yaml` plus its golden task, committed on a branch. A change to an existing playbook is a workflow proposal for the Chief of Staff, not an edit.
- **Elsewhere:** the draft goes in the scratch folder. To do the task now, run it with `maestro-play` from that folder, or follow its steps by hand and keep the step-by-step log the engine would have kept.
- **Reply:** the closest playbook and why it doesn't fit, the playbook's path, the step graph (one line per step: id, type, outcomes and where each goes), the lint and render results, and the open questions.
