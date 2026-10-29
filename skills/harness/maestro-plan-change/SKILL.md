---
name: maestro-plan-change
description: Plan a change before anything is edited, with the example to follow, the files, how each criterion is proved, the risks, a bug's root cause, or an infrastructure plan; use it at a playbook's plan step.
disable-model-invocation: true
metadata:
  internal: true
---

# Plan a change

You decide how the ticket will be done and write it down, so the next step only has to follow it. You read; you never edit code.

## Inputs

- Ticket id, holder, and the ticket's worktree if it has one.
- What to plan: a build (the default), a bug's root cause, or an infrastructure request.

## Steps

1. **Read** the ticket (`maestro-ticket get <id> --json`): goal, acceptance criteria, constraints, pointers, and any repro or trace in the scratchpad. Read the entries in `.maestro/project/memory/` about the areas it names.
2. **Find an example**: the closest thing the code already does the same way (a similar command, endpoint or test), by path. A plan for the Junior Engineer must have one; if there is none, reply `blocked`.
3. **Write the plan** in the scratchpad (`$(git rev-parse --git-common-dir)/maestro/scratch/<bot-id>/<ticket-id>.md`) under `## Plan`:
   - the files to change or add, and the approach in a few lines
   - each acceptance criterion with the test or check that will prove it
   - each risk (behaviour callers rely on, data, security, performance) with how it's covered
4. **Bug:** the root cause, not where it shows: `file:line`, why it happens, the smallest fix, and every other place the same cause appears.
5. **Infrastructure:** what's needed and why; where, per "Release" in `.maestro/project/project-setup.md`; whether it changes files in the repo; the exact commands for anything else, with secrets by variable name; the runner request that proves it works (`check <name>` or `smoke <env>`); the rollback; whether it touches production.

## Reply

```yaml
status: done
summary: plan for T-12 follows src/export/json.ts; 2 criteria, 1 risk
evidence:
  - read src/export/json.ts, src/export/csv.ts, test/export.test.ts
plan: scratch/jse-1/T-12.md#plan
next: build
questions: []
```

## Rules

- Read only. No edits, no commits.
- A criterion nobody can prove, or a plan that needs a decision the ticket doesn't make: reply `blocked` with the question.
