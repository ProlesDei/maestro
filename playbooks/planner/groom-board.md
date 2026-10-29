---
name: groom-board
description: Keep the board tidy on a schedule: clear blockers, find stale claims and duplicates, fix order, and close finished tracking tickets.
role: planner
trigger: scheduled
claims: none
---

# Groom the board

## Goal

The board is accurate: nothing waits that could move, nothing is listed twice, and the most important work is on top.

## When to use

Grooming is due, per the schedule the bot runtime keeps.

## Steps

### 1. Wake gate

- **Goal:** Spend no tokens when nothing changed.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** `maestro-ticket events --since <cursor> --limit 1 --json` and `maestro-ticket doctor --json` both exit `0`. `<cursor>` is the `next_seq` you stored last time. No events and no expired leases: end here, with no harness session.
- **Evidence:** both exit codes; event count; expired lease count.

### 2. Find problems

- **Goal:** A list of what's wrong on the board, with a fix for each.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** The harness has read `maestro-ticket list --json` and the tickets it needs (`maestro-ticket get`), and listed:
  - Blocked tickets with no open `Escalation:` whose `Waiting on:` tickets are done: unblock to `todo`.
  - Blocked tickets with a question or a stuck note and no open `Escalation:`: answer in a comment and unblock to `todo` (with `--role` if it should change hands), or escalate.
  - Blocked tickets that need a decision above your authority: escalate.
  - Expired leases, and `in_progress` tickets with no lease and no activity for a day: note them for the role.
  - Duplicates: cancel the newer one.
  - Tickets missing acceptance criteria or a role: comment what's missing.
  - Priority out of order: comment the fix (maestro-ticket can't change priority).
  - Tracking tickets whose tickets are all done: close.
- **Evidence:** ticket ids and versions for each item.

### 3. Apply fixes

- **Goal:** The fixes are on the board.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** Each fix is applied with `--expect-version` and `--idempotency-key <session-id>:<ticket-id>:<fix>`:
  - unblock: `transition --to todo --from blocked`
  - escalate: comment `Escalation: ...`
  - duplicate: `transition --to cancelled --note "Duplicate of T-<n>"`
  - close a tracking ticket: claim it, then follow the no-code close in the playbooks README.
  
  Skip any ticket with a live lease held by someone else.
- **Evidence:** each command and its exit code.

### 4. Render

- **Goal:** The backlog file matches the board.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** `maestro-ticket render` exits `0`. Store the new `next_seq` as the cursor.
- **Evidence:** exit code; stored cursor.

## Stop and block when

- `maestro-ticket doctor` reports integrity errors. Change nothing else. Create a `blocked` ticket for `chief-of-staff` with the doctor output, and comment `Escalation: ticket store failed its health check`.

## Wrap-up

- Status: no change (nothing claimed). Log the counts of fixes by kind.
- Handoff note: none; each fix carries its own note.
