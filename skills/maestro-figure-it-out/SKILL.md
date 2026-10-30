---
name: maestro-figure-it-out
description: "Designs a Maestro playbook for a task no playbook fits, one someone else can run and audit, then lints and renders it. Use for /maestro-figure-it-out, 'no playbook fits this', 'make a playbook for X', or a job the work playbook can't take."
argument-hint: "<the task>"
disable-model-invocation: true
---

# Figure it out

You design a playbook in Maestro's YAML format, not an improvised session, and hand back its folder, its step graph and the lint and render results. You never run it unless the user says so.

Run this in the Maestro repo, or with `$MAESTRO_PLAYBOOKS` naming Maestro's `playbooks/` folder. `<dir>` is where you draft: `playbooks/` in the Maestro repo, else a scratch copy of `$MAESTRO_PLAYBOOKS`.

## Start

Open a todo list with one item per phase below.

## Phase A: Prove nothing fits

1. Run `ls $MAESTRO_PLAYBOOKS/*/playbook.yaml` and read each `description:` and `intake:`. Name the closest one and say why it doesn't fit: the wrong ticket kind, a missing step, or no role that owns the task.
2. If it fits with another `Type:` line on the ticket, stop and say so. That beats a new playbook.

## Phase B: Frame

Before you design anything, write down the goal, the done criteria (each one checkable), the inputs, the outputs and where they land, the owning role (`chief-of-staff`, `planner`, `engineer`, `qa-lead` or `reviewer`), each action that can't be undone, and each call a human must make.

## Phase C: Design

1. Read `$MAESTRO_PLAYBOOKS/README.md`. Copy `<dir>/work/` to `<dir>/<name>/`. In its `playbook.yaml`, set `name: <name>` (the folder name) and `role:` to the owning role.
2. Pick a type for each step:

   | If the step | Use |
   |---|---|
   | reads or writes a ticket | `ticket:`; the engine adds the lease and the idempotency key |
   | runs `worktree add`, `check all`, `head` or `open pr` | `run:` |
   | decides from a value, such as a sha or a reply field | `gate:`, a template that renders true or false; it costs no tokens |
   | needs a human choice | a `prompt:` step that replies `blocked` with the question; the engine blocks the ticket with an `Escalation:` note |
   | needs judgment | `prompt:`, reusing a template copied from `work/`, with `with: {mode: ...}` if one fits |

3. Write a new prompt template only when none fits: `<template>.md` in the playbook's folder, in the shape the README gives. The engine looks there first, then at the `playbooks/` root, which holds `rules.md`, `_contract.md` and `_escalate.md`.
4. For each prompt step, `out:` lists the reply fields the next step needs, `retry:` asks once more on a missing field, `on:` routes every outcome, and `model:` stays `mid` unless the job needs `frontier`.

## Phase D: Make it auditable

- Put a `gate` before each action that can't be undone: a merge, a deploy, a delete, a call outside the repo. Where a human must agree, add the `blocked` prompt step from the table.
- Give every `when:` a `reason:`, so the run state logs each skip.
- Loop back to an earlier step through `on:`, and let `.limits.step_visits` cap it. Never keep a counter in the playbook.
- Never name a skill or a model id in a playbook or a template.

## Phase E: Check

1. Run `maestro-play lint <dir>` until it passes. Then, for each prompt step, run `maestro-play render <name> --step <id> --ticket <id> --playbooks <dir>` and read the prompt as the session will see it.

## Phase F: Deliver

1. In the Maestro repo, commit `playbooks/<name>/` on a branch for review. Elsewhere, leave the draft in `<dir>/<name>/` and give its path.
2. Only when the user says so, run it: `maestro-play run <name> --ticket <id> --playbooks <dir>`. If they want it done by hand, follow its steps and log each one as the engine would.

**Reply:** the closest playbook and why it doesn't fit, the new playbook's folder, the step graph (one line per step: id, type, each outcome and where it goes), the lint and render results, the open questions.
