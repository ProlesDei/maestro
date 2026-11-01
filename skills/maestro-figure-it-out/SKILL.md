---
name: maestro-figure-it-out
description: "Designs a Maestro playbook for a task no playbook fits, one someone else can run and audit, then lints and renders it. Use for /maestro-figure-it-out, 'no playbook fits this', 'make a playbook for X', or a job the work playbook can't take."
argument-hint: "<the task>"
disable-model-invocation: true
---

# Figure it out

You design a playbook in Maestro's YAML format, not an improvised session, and hand back its folder, its step graph and the lint and render results. You never run it unless the user says so.

`<pb>` is `$MAESTRO_PLAYBOOKS`, else `.maestro/src/playbooks` where INSTALL.md put Maestro, else `playbooks/` in the Maestro repo. `<dir>` is where you draft: in the Maestro repo, `playbooks/` in a worktree and branch of your own; elsewhere, a copy of `<pb>` in a new temp folder of your own. `<skills>` is `<pb>/../skills`. Point `maestro-play` at `<dir>` and `<skills>` on every call.

## Start

Open a todo list with one item per phase below.

## Phase A: Frame

1. Write down the goal, the done criteria (each one checkable), the inputs, the outputs and where they land, the owning role (`chief-of-staff`, `planner`, `engineer`, `qa-lead` or `reviewer`), each action that can't be undone, and each call a human must make.

## Phase B: Design

1. Read `<pb>/README.md`. Copy the folder of the playbook in `<pb>` whose steps come closest to yours to `<dir>/<name>/`. Delete the templates you won't reuse, and replace `README.md` with the frame and how to run it.
2. In `playbook.yaml`, set `name: <name>` (the folder name), `role:`, `description:`, `intake:` (the tickets it takes), `input: required` if it takes `--input`, and `done:` (the status to move to and the note to post).
3. Pick a type for each step:

   | If the step | Use |
   |---|---|
   | reads or writes a ticket | `ticket:`; the engine adds the lease and the idempotency key |
   | runs `worktree add`, `check all`, `check <name>`, `head` or `open pr` | `run:` |
   | decides from a value, such as a sha or a reply field | `gate:`, a template that renders true or false; false is outcome `fail`, which blocks unless `on:` routes it; no tokens |
   | needs a human choice | a `prompt:` step that replies `blocked` with the question; the engine blocks the ticket with an `Escalation:` note |
   | needs judgment | `prompt:`, reusing a template you copied, with `with: {mode: ...}` if one fits |

4. Write a new prompt template only when none fits: `<template>.md` in the playbook's folder, in the shape the README gives. To load a skill in it, list the skill under `prompts.<template>` in `<skills>/use.yaml`; that key covers every playbook's `<template>.md`, so give the template a name no other playbook uses. Every run sends the whole prompt, so keep it lean.
5. For each prompt step, `out:` lists the reply fields later steps need, `retry: {max: 1, tier_up: false}` asks again when one is missing, `model:` stays `mid` unless the job needs `frontier`, and `family: not-author` goes on a step that checks another step's work.

## Phase C: Make it auditable

- Put a `gate` before each action that can't be undone: a merge, a deploy, a delete, a call outside the repo. Where a human must agree, add the human-choice step from the table.
- Give every `when:` a `reason:`, so the run state logs each skip.
- Route through `on:` each outcome that shouldn't block (`failed`, `partial`, `fail`), to a step id, `next`, `done` or `end`; `ok` and `done` go on and anything else blocks. Let `.limits.step_visits` cap a loop back; never keep a counter in the playbook.
- Never name a skill or a model id in a playbook or a template.

## Phase D: Check and hand off

1. Lint `<dir>` with `maestro-play` until it passes. In the Maestro repo, also run the Go tests in `tools/maestro-play/`, which lint and render every playbook.
2. Render each step and the `done` note with `maestro-play`, against a ticket file: a real ticket's JSON or one you write. Read each prompt as the session will see it, each `ticket:` and `run:` step's arguments, and each gate's value. A gate that reads an earlier step's reply renders against an empty one, so check it another way or list it under open questions.
3. In the Maestro repo, add the playbook to the list in `playbooks/README.md` and commit `playbooks/<name>/` on a branch for review. Elsewhere, leave the draft in `<dir>/<name>/` and give its path.
4. Run it with `maestro-play` only when the user says so.

**Reply:** the new playbook's folder and the playbook it started from, the step graph (one line per step: id, type, each outcome and where it goes), the lint and render results, the open questions.
