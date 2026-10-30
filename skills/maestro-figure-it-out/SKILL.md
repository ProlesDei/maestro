---
name: maestro-figure-it-out
description: "Designs a Maestro playbook for a task no playbook fits, one someone else can run and audit, then lints and renders it. Use for /maestro-figure-it-out, 'no playbook fits this', 'make a playbook for X', or a job the work playbook can't take."
argument-hint: "<the task>"
disable-model-invocation: true
---

# Figure it out

You design a playbook in Maestro's YAML format, not an improvised session, and hand back its folder, its step graph and the lint and render results. You never run it unless the user says so.

`<pb>` is `$MAESTRO_PLAYBOOKS`, else `playbooks/` in the Maestro repo. `<dir>` is where you draft: `playbooks/` in the Maestro repo, else a scratch copy of `<pb>` (`cp -R <pb> <scratch>/playbooks`). `<skills>` is `<pb>/../skills`.

## Start

Open a todo list with one item per phase below.

## Phase A: Prove nothing fits

1. Read `description:` and `intake:` in each `<pb>/*/playbook.yaml`. Name the closest one and say why it doesn't fit: the wrong ticket kind, a missing step, or no role that owns the task.
2. If it fits with another `Type:` line on the ticket, stop and say so. That beats a new playbook.

## Phase B: Frame

1. Write down the goal, the done criteria (each one checkable), the inputs, the outputs and where they land, the owning role (`chief-of-staff`, `planner`, `engineer`, `qa-lead` or `reviewer`), each action that can't be undone, and each call a human must make.

## Phase C: Design

1. Read `<pb>/README.md`. Copy `<dir>/work/` to `<dir>/<name>/`. Delete the templates you won't reuse, and replace `README.md` with the frame and how to run it.
2. In `playbook.yaml`, set `name: <name>` (the folder name), `role:`, `description:`, `intake:` (the tickets it takes), `input: required` if it takes `--input`, and `done:` (the status to move to and the note to post).
3. Pick a type for each step:

   | If the step | Use |
   |---|---|
   | reads or writes a ticket | `ticket:`; the engine adds the lease and the idempotency key |
   | runs `worktree add`, `check all`, `check <name>`, `head` or `open pr` | `run:` |
   | decides from a value, such as a sha or a reply field | `gate:`, a template that renders true or false; false is outcome `fail`, which blocks unless `on:` routes it; no tokens |
   | needs a human choice | a `prompt:` step that replies `blocked` with the question; the engine blocks the ticket with an `Escalation:` note |
   | needs judgment | `prompt:`, reusing a template copied from `work/`, with `with: {mode: ...}` if one fits |

4. Write a new prompt template only when none fits: `<template>.md` in the playbook's folder, in the shape the README gives. To load a skill in it, list the skill under `prompts.<template>` in `<skills>/use.yaml`.
5. For each prompt step, `out:` lists the reply fields later steps need, `retry: {max: 1, tier_up: false}` asks again when one is missing, `model:` stays `mid` unless the job needs `frontier`, and `family: not-author` goes on a step that checks another step's work.

## Phase D: Make it auditable

- Put a `gate` before each action that can't be undone: a merge, a deploy, a delete, a call outside the repo. Where a human must agree, add the `blocked` prompt step from the table.
- Give every `when:` a `reason:`, so the run state logs each skip.
- Route through `on:` each outcome that shouldn't block (`failed`, `partial`, `fail`), to a step id, `next`, `done` or `end`; `ok` and `done` go on and anything else blocks. Let `.limits.step_visits` cap a loop back; never keep a counter in the playbook.
- Never name a skill or a model id in a playbook or a template.

## Phase E: Check and hand off

1. Run `maestro-play lint --skills <skills> <dir>` until it passes. In the Maestro repo, also run `go test ./...` in `tools/maestro-play/`.
2. For each prompt step, run `maestro-play render <name> --step <id> --ticket-file <f> --playbooks <dir> --skills <skills>`, where `<f>` is a `maestro-ticket get <id> --json` document or one you write, and read the prompt as the session will see it.
3. In the Maestro repo, add the playbook to the list in `playbooks/README.md` and commit `playbooks/<name>/` on a branch for review. Elsewhere, leave the draft in `<dir>/<name>/` and give its path.
4. Only when the user says so, run it: `maestro-play run <name> --ticket <id> --playbooks <dir> --skills <skills>`.

**Reply:** the closest playbook and why it doesn't fit, the new playbook's folder, the step graph (one line per step: id, type, each outcome and where it goes), the lint and render results, the open questions.
