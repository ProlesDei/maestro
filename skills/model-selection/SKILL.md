---
name: model-selection
description: How to pick the model tier for each playbook step, when to escalate it, and how to keep the Code Reviewer on the opposite model family; use it at every Next step.
---

# Choosing a model per step

Each step runs on the cheapest tier that can do it. Cost matters more across many tickets than on any single one.

## Tiers

| Tier | Use for |
|---|---|
| Frontier | Hard judgment: ambiguous goals, risky or complex code, decisions for the user |
| Mid-tier | Most steps: routine code, reviews, verification, intake, claim, wrap-up |
| No tokens | Deterministic steps: tests, lint, build, `maestro-ticket` commands, `maestro-ticket render`, `git status` |

No-tokens steps run as plain commands, not prompts. The bot runtime executes them the way it runs Preflight's health check: it runs the command in the repo and reads the exit code and output. It never reads or edits file contents itself; anything that needs reading or judging is a prompt. Judge them by exit code and output. Run only commands the role's permissions allow (see `harness-cli`).

## Default tier per role

| Role | Default |
|---|---|
| chief-of-staff | Frontier |
| senior-software-engineer | Frontier |
| planner | Mid-tier |
| junior-software-engineer | Mid-tier |
| code-reviewer | Mid-tier |
| qa-lead | Mid-tier |
| operations-manager | Mid-tier |

A playbook step's `Tier` overrides the role default for that step. Never go below it.

## Escalate after a failed retry, not before

1. First try: the step's tier.
2. Not right: one sharper prompt on the same tier.
3. Still not right, and retries remain: one tier up, in the same session (resume it with the new `--model`).
4. Frontier didn't get it, or retries or budget ran out: set Blocked.

Escalation applies to that step only. The next step starts on its own tier again. Retry limits and budgets come from `.maestro/environment.md`.

## Code Reviewer: opposite model family

The reviewer's model must come from a different family than the author's (for example, review Claude-written code with a Grok model, and the reverse).

- The author's family is in the latest `Handoff:` comment as `author-family:`.
- Engineers: put your own family there when you submit.
- Reviewer: pick only tickets you can review on another family. If `environment.md` maps no other family for your harness, leave the ticket for another reviewer. If no reviewer can take it, set Blocked with an `Escalation:` note to the Chief of Staff.

## Model IDs

Don't hardcode model IDs in prompts, playbooks or skills. `.maestro/environment.md` maps each tier to a model ID per harness, and names each model's family. Read it at preflight. If a tier has no model for your harness, use the next tier up and say so in the wrap-up retro.

Effort levels (`--effort`, `--reasoning-effort`) come from the same file when it sets them.
