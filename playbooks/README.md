# Playbooks

A playbook is a fixed list of typed steps that `maestro-play` runs in order. Most steps cost no tokens: a ticket call, `git`, the project's checks, `gh`. Only a `prompt:` step starts a model session, and its prompt is a template that lives in the playbook's folder. The engine, not the model, decides what runs next, so every run of a playbook takes the same path. Playbooks are never installed into the target repo. Engine detail (commands, built-in requests, template data and functions, exit codes) is in [tools/maestro-play/README.md](../tools/maestro-play/README.md).

Maestro ships one: [work](work/README.md), a ticket to an open PR.

```
playbooks/
  rules.md          system prompt for every session the engine starts
  _contract.md      opens every prompt step
  _escalate.md      runs when a step blocks
  work/
    playbook.yaml   the steps
    plan.md         prompt for the plan step
    build.md        prompt for the build step
    verify.md       prompt for the verify step
    README.md       what it does, how to run it
```

Run one by its folder name: `maestro-play run work`. The engine reads a step's template from the playbook's folder first, then from `playbooks/`, so a template two playbooks share can sit at the top beside `_contract.md`.

## Step keys

| Key | Does |
|---|---|
| `ticket: <maestro-ticket args>` | a ticket call; the engine adds `--json`, the lease and the idempotency key |
| `run: <request>` | a built-in request: `worktree add`, `check all`, `head`, `open pr` |
| `gate: <template>` | renders `true` or `false` from values the run already has; no tokens |
| `prompt: <template>` | renders `_contract.md`, the skills `skills/use.yaml` names, then `<template>.md`, and sends it to the harness session |
| `model:` | `mid` (default) or `frontier` |
| `with: {k: <template>}` | rendered first, read in the prompt as `.step.with.k` |
| `out: [fields]` | the reply fields a `done` or `failed` reply must carry |
| `retry: {max, tier_up}` | asks again when a reply misses a field |
| `when:` and `reason:` | a template that renders `true` or `false`; false skips the step and logs the reason |
| `on: {outcome: step}` | where an outcome goes; by default `ok` and `done` go on, anything else blocks |
| `done: {to, note}` | the wrap-up: post the note, move the ticket, release the lease |

`intake:` lists the tickets a playbook takes. With `--ticket`, the engine checks only the status. Without it, the engine tries each entry in order and claims the first unclaimed ticket with that status whose latest comment starts with `latest:`.

## Add a playbook

1. Copy `work/` to `playbooks/<name>/`. In `playbook.yaml`, `name` is the folder name and `role` is one of `chief-of-staff`, `planner`, `engineer`, `qa-lead`, `reviewer`.
2. Make any step with no judgment a `run`, `ticket` or `gate` step, never a prompt.
3. Give each prompt template a one-line goal, about six numbered steps and a closing `Reply:` line, in 25 lines or fewer. Put fixed text first and the `## This step` variables last, so the harness can cache the prefix.
4. Prompts point to files, tickets and branches; they never paste them. They never name a skill or a model id.
5. Run `go test ./...` in `tools/maestro-play/`. It lints and renders every playbook here.
