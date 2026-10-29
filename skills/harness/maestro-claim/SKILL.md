---
name: maestro-claim
description: Claim one ticket for this session with maestro-ticket and start its scratchpad; use it right after intake, once the bot has picked the work.
disable-model-invocation: true
metadata:
  internal: true
---

# Claim the work

You take a lease on one ticket so no other session works on it, then start the scratchpad that lets a later session resume. Claiming doesn't change the ticket's status; the playbook does that.

`--keepalive $PPID` makes the claim start a background heartbeat that runs while this harness session's process lives, and stops by itself on release or a terminal move.

## Inputs

- Holder: `<bot-id>/<session-id>`.
- Either a ticket id, or a queue: `--status` and `--role` to claim the next ticket from.
- Lease TTL (the bot passes it; default `30m`).
- Playbook name, for the scratchpad.

## Steps

1. Claim, with idempotency key `<session-id>:claim-<n>`, where `<n>` is 1 plus the claims already in the scratchpad's step log (a replayed key fails once its lease is gone, so a re-claim needs a new one):
   - By id: `maestro-ticket claim <id> --holder <holder> --ttl <ttl> --keepalive $PPID --idempotency-key <key> --json`
   - From a queue: `maestro-ticket claim --next --status <S> --role <R> --holder <holder> --ttl <ttl> --keepalive $PPID --idempotency-key <key> --json`
2. Handle the exit code:
   - `0`: claimed. Read the id, version, lease token (`claim.token`) and lease expiry from the JSON. Every later write to the ticket passes `--holder <holder> --token <token>`; the token changes on every claim, so never reuse one from an earlier claim.
   - `5`: no work in that queue. Reply `done` with `claimed: null`.
   - `4`: claimed by another holder, a conflict, or (by id) `waiting_on_deps` (a dependency isn't `done`) or `area_busy` (a live claim holds an overlapping code area). Reply `failed` with the error JSON; the bot picks other work. Don't retry with a different holder. `--next` already skips those tickets.
   - `2` naming `--keepalive`: `$PPID` isn't a live process. Reply `failed`; never claim without it, since nothing else heartbeats.
   - `3`: ticket not found. `2`: bad request. `1`: internal error. Reply `failed` with the error JSON.
3. On success, write the scratchpad at `$(git rev-parse --git-common-dir)/maestro/scratch/<bot-id>/<ticket-id>.md`. If one exists, keep its step log and update the header.

```markdown
# T-12 scratchpad
holder: jse-1/6f1c...
playbook: build-ticket
token: 7
version: 5
lease_expires: 2026-10-28T14:30:00Z
branch:
worktree:
temp_dir:
processes:
## Steps
- claimed at version 5, token 7
```

Fill in `branch`, `worktree`, `temp_dir` and `processes` (PID and command) as later steps create them. Add one line per finished step. Clean-up reads this file to know what this session owns.

## Reply

```yaml
status: done
summary: claimed T-12
evidence:
  - maestro-ticket claim T-12 --holder jse-1/6f1c... --ttl 30m --keepalive $PPID → exit 0
claimed: {id: T-12, status: todo, token: 7, version: 5, lease_expires: 2026-10-28T14:30:00Z}
scratchpad: scratch/jse-1/T-12.md (new)
next: start the playbook
questions: []
```

## Rules

- Claim one ticket per session. Never claim a second one.
- Use the exact holder you were given. Never reuse another session's holder to take over its lease.
- Write only the scratchpad. Don't touch the ticket otherwise.
