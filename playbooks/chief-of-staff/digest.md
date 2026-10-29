---
name: digest
description: When the user asks, give a short digest of the active goals, their workstreams and spending against budget.
role: chief-of-staff
trigger: request
claims: none
types: none
---

# Digest

## Goal

In a few lines, the user sees where each goal stands, which workstreams are moving or stuck, and what has been spent against budget.

## When to use

The user asks for the digest (for example "digest" or "where are we?"). For what happened since they last spoke, use `recap`.

## Steps

### 1. Read the summaries

- **Goal:** Goal and workstream state, and spend, with no tokens spent.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `summary`
- **Done when:** Exit `0` with every active goal, its workstreams (status counts, blocked count, the Planner's risks and next), and spend by goal, workstream and role in dollars and tokens.
- **Evidence:** exit code; goal and workstream ids.

### 2. Check budgets

- **Goal:** Know what is near or over budget.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `usage check --all`
- **Done when:** Exit `0`, `1` or `2`, with each scope that warns or is over.
- **Evidence:** exit code; the scopes that warn or are over.

### 3. Write and send the digest

- **Goal:** The user has the digest.
- **Role:** bot
- **Skill:** `talk-to-user`
- **Model:** Bot
- **Done when:** One line per goal (state, its workstreams, spend), then budget warnings, then the count of decisions waiting on the user. At most 12 lines. Every number comes from steps 1 and 2.
- **Evidence:** the digest sent.

## Stop and block when

- `summary` fails: tell the user the board can't be read, with the exit code. Don't guess.

## Wrap-up

- Skill: `maestro-log-entry` (nothing claimed, no status change). Log that a digest was sent, and any `Skipped:` steps.
- Handoff note: none.
