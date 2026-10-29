---
name: status-brief
description: Send the user a short status brief on a schedule, built from workstream summaries, but only when something changed since the last one.
role: chief-of-staff
trigger: scheduled
claims: none
types: none
---

# Status brief

## Goal

The user knows what shipped, what's moving, what's stuck and what needs them, in under a minute of reading. No brief when nothing changed.

## When to use

The status brief is due, per the schedule the bot runtime keeps. When the user asks, use `digest` or `recap` instead.

## Steps

### 1. Wake gate

- **Goal:** Spend no tokens when nothing changed.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket events --since <cursor> --limit 1 --json`
- **Done when:** Exit `0`. `<cursor>` is the `next_seq` stored after the last brief (`0` the first time). No events: end here, with no brief and no wrap-up.
- **Evidence:** exit code and event count.

### 2. Read the summaries

- **Goal:** What changed, per goal and workstream.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `summary --since <cursor>`
- **Done when:** Exit `0` with the summaries and `next_seq`.
- **Evidence:** exit code; `next_seq`.

### 3. Write and send the brief

- **Goal:** The user has a brief built only from the summaries.
- **Role:** bot
- **Skill:** `talk-to-user`
- **Model:** Bot
- **Done when:** At most 10 lines under Shipped, In flight, Blocked and on whom, Needs you (each with a recommendation). Every line cites a goal or workstream. Nothing is invented. Sent, then the new `next_seq` stored as the cursor. Store it only after sending, so a failed send repeats next time.
- **Evidence:** the brief; the stored cursor.

## Stop and block when

- `ticket events` or `summary` fails: tell the user the board can't be read, with the exit code. Don't guess a brief.

## Wrap-up

- Skill: `maestro-log-entry` (nothing claimed, no status change). Log the cursor range covered.
- Handoff note: none.
