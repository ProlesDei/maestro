---
name: groom-board
description: On a schedule, keep one workstream's board tidy by promoting ready backlog tickets, finding stale work and duplicates, checking dependencies, and fixing order.
role: planner
trigger: scheduled
claims: none
types: none
---

# Groom the board

## Goal

The board is accurate: nothing waits that could move, nothing is listed twice, dependencies are right, and the most important work is on top.

## When to use

Grooming is due, per the schedule the bot runtime keeps. Each Planner session grooms only its own workstream's tickets. Escalations go to `answer-escalation`, not here.

## Steps

### 1. Wake gate

- **Goal:** Spend no tokens when nothing changed.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket events --since <cursor> --limit 1 --json`, then `ticket doctor --json`
- **Done when:** Both exit `0`. `<cursor>` is the `next_seq` you stored last time. No events (a lease freed by `reap` is a `lease_expired` event) and doctor's `leases` check counts no expired leases: end here, with no harness session.
- **Evidence:** both exit codes; event count; expired lease count.

### 2. Find problems

- **Goal:** A list of what's wrong on the board, with a fix for each.
- **Role:** harness
- **Skill:** `maestro-groom-board`
- **Model:** Mid-tier
- **Done when:** The harness has read the workstream's tickets and listed, with ticket ids and versions:
  - `backlog` tickets that are now ready: promote to `todo`.
  - Dependencies that are missing, wrong or circular: fix.
  - `blocked` tickets used to wait on another ticket: move to dependencies and back to the status they were blocked from.
  - Expired leases, and `in_progress` tickets with no lease and no activity past the stale threshold: note them for the role.
  - Duplicates: cancel the newer one.
  - Tickets missing acceptance criteria, a type, a role or an owner: fix or comment what's missing.
  - Priority out of order: comment the fix.
- **Evidence:** ticket ids and versions for each item; the `next_seq` of the events it read.

### 3. Apply fixes

- **Goal:** The fixes are on the board.
- **Role:** harness
- **Skill:** `maestro-write-tickets`
- **Model:** Mid-tier
- **Skip when:** step 2 found nothing.
- **Done when:** Each fix is applied with idempotency key `<session-id>:<ticket-id>:<fix>`, and moves also with `--expect-version`; dependencies and areas change with `update`. Tickets with a live lease held by someone else are left alone. Grooming never sets `done`.
- **Evidence:** each command and its exit code.

Store the `next_seq` from step 2 as the cursor, whatever ran after it.

## Stop and block when

- `ticket doctor` reports integrity errors: change nothing else. Create a `blocked` chore ticket with the doctor output and an `Escalation (chief-of-staff): ticket store failed its health check` note. A failed environment check (`filesystem`, `locks`, a bare repo or a submodule) isn't store damage: say so in the note, since the user has to fix the machine, not the store.

## Wrap-up

- Skill: `maestro-log-entry` (nothing claimed, no status change). Log the counts of fixes by kind and any `Skipped:` steps.
- Handoff note: none; each fix carries its own note.
