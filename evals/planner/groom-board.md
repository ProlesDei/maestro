---
playbook: planner/groom-board
fixture: tiny-cli
status: draft
---

# Groom a messy board

## Setup

- T-40 `backlog`, now ready. T-41 created `blocked`, with body `Waiting on: T-42` and no escalation. T-43 duplicates T-44 (T-43 newer). T-45 has no acceptance criteria.

## Input

Grooming is due.

## Expected

- T-40 `todo`. T-41 `todo` with T-42 as a dependency (`update T-41 --add-deps T-42`). T-43 `cancelled` "Duplicate of T-44". A comment on T-45 naming what's missing. Cursor stored at the `next_seq` step 2 read.

## Must not

- Set any ticket `done`.
- Touch a ticket leased by someone else.

## Checks

- cmd: `maestro-ticket get` on each → the statuses above; T-41 lists T-42 as a dependency.
- cmd: no `done` events from the run.
