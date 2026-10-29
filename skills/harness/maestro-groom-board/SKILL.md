---
name: maestro-groom-board
description: Read one workstream's tickets and list what's wrong on the board, each with its fix; use it at grooming, before the fixes are applied.
disable-model-invocation: true
metadata:
  internal: true
---

# Groom the board

You find what keeps the workstream's board from being accurate, and say how to fix each item. You change nothing in this step.

## Inputs

- The workstream, the cursor from the last grooming, and the stale-work threshold, from the prompt.

## Steps

1. **Read:** `maestro-ticket list --json` for the workstream's tickets, `maestro-ticket events --since <cursor> --json` (note the `next_seq`), and `get` for each ticket you judge.
2. **List**, with ticket id and version:
   - `backlog` tickets that are now ready: promote to `todo`.
   - Dependencies missing, wrong or circular: the fix.
   - `blocked` tickets waiting on another ticket: dependencies instead, back to the status they were blocked from (`blocked_from`; `todo` for a ticket created blocked).
   - Expired leases (`lease_expired` events; a ticket whose `lease_expiries` keeps growing is a candidate for escalation), and `in_progress` tickets with no lease and no event past the threshold: note them for their role.
   - Duplicates: cancel the newer with `Duplicate of T-<n>`.
   - Missing acceptance criteria, type, role or owner: fix, or comment what's missing.
   - Priority out of order: a comment with the fix.

## Reply

```yaml
status: done
summary: 4 fixes in workstream export
evidence:
  - maestro-ticket list → 12 tickets; events since 840 → next_seq 871
fixes:
  - {id: T-43, version: 3, fix: "cancel: Duplicate of T-44"}
next_seq: 871
next: apply fixes
questions: []
```

## Rules

- Read only.
- Never propose `done`.
