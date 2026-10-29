---
name: example-job
description: One sentence on what this playbook does.
role: example-role
trigger: new-work
claims: todo
types: feature, bug, chore, docs
---

# Example job

## Goal

The outcome, in one or two sentences.

## When to use

Which intake candidates this playbook is for, and which it isn't.

## Steps

### 1. Step name

- **Goal:** what this step must achieve.
- **Role:** harness, runner or bot.
- **Skill:** `maestro-...` for harness, `runner` for runner, a bot skill or `none` for bot.
- **Model:** Frontier, Mid-tier, No tokens or Bot.
- **Run:** runner steps only: `<command> <args>`.
- **Skip when:** only if the step can be skipped.
- **Done when:** the check that proves it.
- **Evidence:** what the reply must include.

### 2. Step name

- **Goal:**
- **Role:**
- **Skill:**
- **Model:**
- **Done when:**
- **Evidence:**

## Stop and block when

- The condition, and what the escalation note must say.

## Wrap-up

- Status: the target status, and `--role` if the ticket changes role.
- Handoff note: what the next role needs to start cold, and any `Skipped:` steps.
