---
name: maestro-write-tickets
description: Create or fix tickets with every field the team needs and testable acceptance criteria; use it when planning, answering an escalation, acting on a report or applying grooming fixes.
disable-model-invocation: true
metadata:
  internal: true
---

# Write tickets

You put work on the board so an engineer can finish it without asking a question.

## Inputs

- Holder and session id, and the lease token for a ticket you hold.
- New tickets to create, or fixes by ticket id and version.

## Steps

1. **New tickets:** `maestro-ticket create --title <t> --type <ship|scout> --owner <role> --role <role> --priority <0-3> [--deps <ids>] [--areas <paths>] [--status backlog] --body-file - --idempotency-key <session-id>:<n> --json`. `--type scout` for work that ships no code: an investigation (it ends at a report its owner reads), or an infrastructure request for `operations-manager` (owner `operations-manager`, who sets it done). `ship` for the rest. `--owner` is the role that owns the workstream, normally its Planner. `--deps` lists the tickets that must be `done` first; `claim --next` won't hand the ticket out until they are, so it can go straight to `todo`. `--areas` lists the code paths a ship ticket touches; two tickets with overlapping areas are never worked at the same time. The body has:
   - the goal, and acceptance criteria that are each testable (a command or an observable behaviour)
   - pointers (files, the spec comment) and constraints
   - `Type: <feature|bug|chore|docs|investigation>`, and `Playbook: refactor` or `Playbook: performance` where it applies
2. **Fixes:** `maestro-ticket transition` with `--expect-version <N>`, `comment`, or `maestro-ticket update <id> --add-deps <ids> --remove-deps <ids> --areas <paths>` for dependencies and areas, each with idempotency key `<session-id>:<ticket-id>:<fix>`. A changed body or criteria goes in a comment with the corrected text in full. On a ticket you hold, add `--holder <holder> --token <token>`; on one nobody holds, neither. Leave tickets with someone else's live lease alone.
3. **Exit codes:** `4`: re-read and report both versions; don't retry. `2` on `--deps` or `--add-deps` means a cycle and `3` an unknown ticket: fix the list, don't drop it.

## Reply

```yaml
status: done
summary: 4 tickets created
evidence:
  - maestro-ticket create "List --json flag" → exit 0, T-31
tickets: [{id: T-31, type: ship, kind: feature, role: junior-software-engineer, priority: 2, deps: []}]
next: check the board
questions: []
```

## Rules

- Waiting on another ticket is a dependency, never `blocked`.
- Never set `done`.
