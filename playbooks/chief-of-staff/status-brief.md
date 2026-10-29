---
name: status-brief
description: Send the user a short status brief on a schedule, but only when something changed on the board since the last one.
role: chief-of-staff
trigger: scheduled
claims: none
---

# Status brief

## Goal

The user knows what shipped, what's moving, what's stuck and what needs them, in under a minute of reading. No brief when nothing changed.

## When to use

The status brief is due, per the schedule the bot runtime keeps.

## Steps

### 1. Wake gate

- **Goal:** Spend no tokens when nothing changed.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** `at-ticket events --since <cursor> --limit 1 --json` exits `0`. `<cursor>` is the `next_seq` you stored after the last brief (`0` the first time).
  - No events: end here. No brief, no harness session, no wrap-up prompt.
  - Events: go on.
- **Evidence:** exit code and event count.

Run this before you start a harness session for the brief.

### 2. Write the brief

- **Goal:** A brief built from what changed.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** The harness has read `at-ticket events --since <cursor> --json` and `.agent-team/Backlog.md` and replied with at most 10 lines under:
  - Shipped
  - In flight
  - Blocked, and on whom
  - Needs you: decisions, each with a recommendation
  
  Every line cites a ticket id. Nothing in the brief is invented.
- **Evidence:** the `next_seq` from the events output; the ticket ids cited.

### 3. Send it

- **Goal:** The user has the brief.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** You've sent the brief and stored the new `next_seq` as the cursor. Store it only after the brief is sent, so a failed send repeats next time.
- **Evidence:** the stored cursor value.

## Stop and block when

- `at-ticket events` fails: tell the user the board can't be read, with the exit code. Don't guess a brief.

## Wrap-up

- Status: no change (nothing claimed). Log the cursor range covered.
- Handoff note: none.
