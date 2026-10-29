---
name: recap
description: When the user asks or returns, recap what happened since they last spoke, then walk through each open decision one at a time.
role: chief-of-staff
trigger: request
claims: none
types: none
---

# Recap

## Goal

The user catches up in a minute, then clears every decision waiting on them, one at a time, each with a recommendation.

## When to use

The user asks for a recap, or speaks after being away longer than the recap threshold from preflight.

## Steps

### 1. Read what changed

- **Goal:** Summaries since the user last spoke, with no tokens spent.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `summary --since <user cursor>`
- **Done when:** Exit `0` with what shipped, what moved, what got stuck, and `next_seq`. `<user cursor>` is the `next_seq` stored the last time the user spoke.
- **Evidence:** exit code; cursor range.

### 2. List open decisions

- **Goal:** Every decision waiting on the user, oldest first.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket list --status blocked --json`
- **Done when:** Exit `0`. The decisions are the tickets whose latest comment is `Handoff: waiting on the user:`.
- **Evidence:** exit code; ticket ids.

### 3. Send the recap

- **Goal:** The user knows what happened.
- **Role:** bot
- **Skill:** `talk-to-user`
- **Model:** Bot
- **Done when:** At most 8 lines: shipped, moved, stuck, then "N decisions need you". Every line cites a goal or workstream. Then store `next_seq` as the user cursor.
- **Evidence:** the recap sent; the stored cursor.

### 4. Walk through the decisions

- **Goal:** Each open decision answered or deferred by the user.
- **Role:** bot
- **Skill:** `talk-to-user`
- **Model:** Bot
- **Skip when:** step 2 found none.
- **Done when:** For each, one at a time: the question, the options and your recommendation, then the user's answer. Each answer goes to `handle-escalation` for its ticket. Stop when the user stops; the rest stay open.
- **Evidence:** per decision: the ticket id and the answer, or "deferred".

## Stop and block when

- `summary` or `ticket list` fails: tell the user the board can't be read, with the exit code. Don't guess.

## Wrap-up

- Skill: `maestro-log-entry` (nothing claimed, no status change). Log the cursor range, decisions answered and deferred, and any `Skipped:` steps.
- Handoff note: none; each answer is recorded on its ticket by `handle-escalation`.
