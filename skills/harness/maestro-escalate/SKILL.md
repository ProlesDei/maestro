---
name: maestro-escalate
description: Hand a ticket up the escalation chain with a decision-ready note, blocking or re-routing it and releasing the lease; use it when the session can't go on without someone above it.
disable-model-invocation: true
metadata:
  internal: true
---

# Escalate

You pass a problem one step up the chain (session → ticket owner → Chief of Staff → user), written so the reader can decide in one go.

## Inputs

- Ticket id, holder `<bot-id>/<session-id>`, the lease token from the claim, and the version from the last read.
- Your role: the bot-profile folder name.
- Who it goes to: the role to set with `--role`, and the prefix. The bot names both: `Escalation:` for the ticket's owner (or the Senior Engineer, on `todo`), `Escalation (chief-of-staff):` for the Chief of Staff.
- Target status: `blocked`, `todo` when a Junior Engineer hands the ticket to the Senior Engineer, or `keep` when it's already `blocked` and you forward it (maestro-ticket has no `blocked` → `blocked` move).

## Steps

1. **Check the lease.** `maestro-ticket get <id> --json`. If another holder has it, or its `claim.token` isn't yours, stop and reply `failed`. Write nothing.
2. **Commit work in progress** on the ticket's branch, if there is one, so the worktree is clean. Never discard it.
3. **Note.** `maestro-ticket comment <id> --body "<note>" --holder <holder> --token <token> --idempotency-key <session-id>:escalate --json`. Start the body with the prefix and include, briefly:
   - **What happened:** one or two sentences.
   - **What was tried:** each attempt and why it failed, with the failing output line.
   - **Options:** two or three, with trade-offs.
   - **Recommendation:** which one and why.
   - **Decision needed:** the exact question, ideally answerable in one word.
   - **Where:** branch, commit, paths.
   - **Tries:** the count from the last read, and any `Skipped: <step> — <reason>` lines.
   - `Return-role: <role>`: the ticket's role before this escalation, so whoever unblocks it can hand it back. Forwarding: copy it from the escalation you're forwarding.
4. **Status.** `maestro-ticket transition <id> --to <S> --from <current> --expect-version <N> --holder <holder> --token <token> --actor-role <your role> --role <R> --note "<prefix> <one line>" --release --idempotency-key <session-id>:escalate-status --json`
   - Exit `4`: someone changed it, or the lease was lost (`lease_lost`). Re-read, report both versions, don't retry.
   - Exit `2`: transition not allowed. Report it.
   - `keep` (forwarding a `blocked` ticket): no transition. Run `maestro-ticket release <id> --holder <holder> --token <token> --idempotency-key <session-id>:escalate-release --json`; the prefix routes it, and the ticket keeps its status and `blocked_from`.

## Reply

```yaml
status: done
summary: T-12 → blocked for planner; decision needed on the date format
evidence:
  - maestro-ticket comment T-12 → exit 0
  - maestro-ticket transition T-12 --to blocked --role planner --expect-version 9 → exit 0, version 10
next: planner decides
questions: [ISO dates or locale dates in the export?]
```

## Rules

- The note is the latest comment on the ticket, so routing can read its prefix. Post nothing after it.
