---
name: model-selection
description: How to pick the model tier for each playbook step, when to escalate it, and how to keep the Code Reviewer on another model family; use it at every Next step.
metadata:
  internal: true
---

# Choosing a model per step

Each step runs on the cheapest tier that can do it. Cost matters more across many tickets than on any single one.

## Tiers

| Tier | Use for |
|---|---|
| Frontier | Hard judgment: ambiguous goals, risky or complex code, decisions for the user |
| Mid-tier | Most steps: routine code, reviews, verification, intake, claim, wrap-up |
| No tokens | Deterministic steps: checks (lint, test, build), worktree setup, ticket reads, budget checks |

A no-token step is a runner request (`runner`), never a prompt and never a command you run. Judge it by the runner's status, exit code and output. Anything that needs reading or judging is a prompt to a harness session.

Your default tier is the one in your persona. A playbook step's **Model:** line overrides it for that step. Never go below it.

## Escalate after a failed retry, not before

1. First try: the step's tier.
2. Not right: one sharper prompt on the same tier.
3. Still not right, and retries remain: one tier up, in the same session (`session resume` with the new tier).
4. Frontier didn't get it, or retries or budget ran out: block.

This applies to one step. The next step starts on its own tier again. Retry limits and budgets come from preflight. This is not the ticket's Tries count, which only review and QA send-backs change.

## Code Reviewer: another model family

The reviewer's model must come from a different family than the author's (for example, review Claude-written code with a Grok model, and the reverse).

- The author's family is in the latest `Handoff:` comment as `author-family:`.
- Engineers: put your own family there when you submit.
- Reviewer: pick only tickets you can review on another family. If preflight shows no other family, leave the ticket for another reviewer. If no reviewer can take it, block with an escalation.

## Model IDs

Never write model IDs into prompts, playbooks or skills. Ask for a tier; the runner maps it to a model per harness. If a tier has no model for your harness, use the next tier up and say so in the retro. Effort levels come from preflight too.
