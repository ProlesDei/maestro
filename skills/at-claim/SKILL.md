---
name: at-claim
description: Claim one ticket for this session with at-ticket and start its scratchpad; use it right after intake, once the bot has picked the work.
---

# Claim the work

You take a lease on one ticket so no other session works on it, then start the scratchpad that lets a later session resume. Claiming doesn't change the ticket's status; the playbook does that.

## Inputs

- Holder: `<bot-id>/<session-id>`.
- Either a ticket id, or a queue: `--status` and `--role` to claim the next ticket from.
- Lease TTL from `.agent-team/environment.md` (default `30m`).
- Playbook name, for the scratchpad.

## Steps

1. Claim, with idempotency key `<session-id>:claim`:
   - By id: `at-ticket claim <id> --holder <holder> --ttl <ttl> --idempotency-key <key> --json`
   - From a queue: `at-ticket claim --next --status <S> --role <R> --holder <holder> --ttl <ttl> --idempotency-key <key> --json`
2. Handle the exit code:
   - `0`: claimed. Read the id, version and lease expiry from the JSON.
   - `5`: no work in that queue. Reply `done` with `claimed: null`.
   - `4`: claimed by another holder, or a conflict. Reply `failed` with the error JSON. Don't retry with a different holder.
   - `3`: ticket not found. `2`: bad request. `1`: internal error. Reply `failed` with the error JSON.
3. On success, write the scratchpad at `$(git rev-parse --git-common-dir)/agent-team/scratch/<bot-id>/<ticket-id>.md`. If one exists, keep its step log and update the header.

```markdown
# T-12 scratchpad
holder: jse-1/6f1c...
playbook: build-ticket
version: 5
lease_expires: 2026-10-28T14:30:00Z
branch:
worktree:
temp_dir:
processes:
## Steps
- claimed at version 5
```

Fill in `branch`, `worktree`, `temp_dir` and `processes` (PID and command) as later steps create them. Add one line per finished step. Clean-up reads this file to know what this session owns.

## Reply

```yaml
status: done
summary: claimed T-12
evidence:
  - at-ticket claim T-12 --holder jse-1/6f1c... --ttl 30m → exit 0
claimed: {id: T-12, status: todo, version: 5, lease_expires: 2026-10-28T14:30:00Z}
scratchpad: scratch/jse-1/T-12.md (new)
next: start the playbook
questions: []
```

## Rules

- Claim one ticket per session. Never claim a second one.
- Use the exact holder you were given. Never reuse another session's holder to take over its lease.
- Write only the scratchpad. Don't touch the ticket otherwise.
- Never put secrets in the scratchpad.
