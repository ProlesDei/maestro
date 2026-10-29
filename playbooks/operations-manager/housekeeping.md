---
name: housekeeping
description: On a schedule, remove what finished tickets left behind, rotate logs and snapshot the ticket store, without touching anything a live lease or the user owns.
role: operations-manager
trigger: scheduled
claims: none
types: none
---

# Housekeeping

## Goal

Disk, branches and the store stay tidy and backed up, and nothing in use or owned by the user is removed.

## When to use

Housekeeping is due, per the schedule the bot runtime keeps.

## Steps

### 1. List what can go

- **Goal:** A plan of what to remove, with no tokens spent.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `housekeep`
- **Done when:** Exit `0` with the plan: worktrees, `maestro/` branches, scratchpads and temp files of `done` or `cancelled` tickets, logs past retention, and when the store was last snapshotted. Empty plan and a recent snapshot: end here.
- **Evidence:** item counts by kind; the last snapshot time.

### 2. Check the plan

- **Goal:** Nothing in the plan is still needed.
- **Role:** harness
- **Skill:** `maestro-housekeeping`
- **Model:** Mid-tier
- **Done when:** Each item is confirmed safe: its ticket is `done` or `cancelled`, no live lease points at it, an unmerged branch has its pull request merged or closed, and it's inside Maestro's own paths. Items that fail are dropped from the plan with the reason.
- **Evidence:** the kept and dropped items, with reasons.

### 3. Apply

- **Goal:** The checked plan is applied, and the store is snapshotted.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `housekeep --apply <plan>`
- **Done when:** Exit `0`; every item in the plan is gone; the snapshot is written.
- **Evidence:** exit code; counts removed; the snapshot path.

### 4. Health check

- **Goal:** The store is still healthy.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket doctor --json`
- **Done when:** Exit `0`.
- **Evidence:** exit code; doctor output summary.

## Stop and block when

- `ticket doctor` reports integrity errors: change nothing else. Create a `blocked` chore ticket with the doctor output and an `Escalation (chief-of-staff): ticket store failed its health check` note. A failed environment check (`filesystem`, `locks`, a bare repo or a submodule) isn't store damage: say so in the note, since the user has to fix the machine, not the store.
- An item is outside Maestro's paths (`maestro/` branches, the `maestro/` folder under the git common dir): drop it. Never remove the user's branches or files.

## Wrap-up

- Skill: `maestro-log-entry` (nothing claimed, no status change). Log the counts removed, the snapshot path and any `Skipped:` steps.
- Handoff note: none.
