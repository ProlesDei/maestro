---
playbook: planner/groom-board
fixture: tiny-cli
status: draft
---

# Groom a messy board

## Setup

- T-40 `backlog`, now ready. T-41 created `blocked`, with body `Waiting on: T-42` and no escalation. T-43 duplicates T-44 (T-43 newer). T-45 has no acceptance criteria. T-46 `in_progress`, leased by another holder.
- The stored `groom` cursor is older than all of these.

## Input

Grooming is due: `maestro-play run planner/groom-board`.

## Expected

- Steps run: `events`, `doctor`, `wake`, `problems`, `never_done`, `apply`. `store_broken` doesn't run.
- `apply` leaves T-40 `todo`; T-41 back to `todo` with `depends_on` T-42 (`update --add-deps`); T-43 `cancelled` "Duplicate of T-44"; a comment on T-45 starting `Groom: ` and naming what's missing.
- The `groom` cursor is stored at the `next_seq` that `events` returned, whatever `problems` replied.

## Must not

- Set any ticket `done`.
- Touch T-46, leased by someone else.

## Checks

- cmd: `maestro-ticket get` on each → the statuses above; T-41 lists T-42 in `depends_on`.
- cmd: `maestro-ticket events --since <seq before the run> --json` → no `done` transitions, nothing on T-46.
