---
name: maestro-handoff-note
description: Post the handoff note on a ticket and move it to its next status, releasing the lease; use it at wrap-up whenever the work goes on to the next role.
disable-model-invocation: true
metadata:
  internal: true
---

# Handoff note

You leave a note the next role can act on cold, then hand the ticket on. Use `maestro-escalate` instead when someone above you must decide.

## Inputs

- Ticket id, holder `<bot-id>/<session-id>`, the lease token from the claim, and the version from the last read.
- Your role: the bot-profile folder name, for example `operations-manager`.
- Target status, or `keep` when it stays where it is, and `--role` if the ticket moves to another role. `done` on a scout ticket you own that isn't `in_review` yet is two moves: `in_review`, then `done`.
- For engineers submitting code: the author's model family.

## Steps

1. **Check the lease.** `maestro-ticket get <id> --json`. If another holder has it, or its `claim.token` isn't yours, stop and reply `failed`. Write nothing.
2. **Note.** `maestro-ticket comment <id> --body "<note>" --holder <holder> --token <token> --idempotency-key <session-id>:handoff --json`. Start the body with `Handoff:`, or `Approved:` / `Declined:` when it answers an escalation, and include:
   - what you did and what's left
   - where: branch, commit, paths
   - evidence: checks run with exit codes and test counts, evidence files
   - `Tries: <n>` from the last read (maestro-ticket adds one when review or QA sends it back to `in_progress`), and each `Skipped: <step> — <reason>`
   - engineers submitting code: `author-family: <family>`
   - scout tickets: where the `Report:` comment is, and the answer in one line
   - Exit `4` with `idempotency_conflict`: an earlier try already posted the note. Don't post another; go on to the status.
3. **Status.** `maestro-ticket transition <id> --to <S> --from <current> --expect-version <N> --holder <holder> --token <token> --actor-role <your role> [--role <R>] --note "<one line>" --release --idempotency-key <session-id>:handoff-status --json`
   - Closing a scout ticket you own: pass `--actor <your role>`, the owner the ticket records. If it isn't `in_review` yet, first move it there the same way without `--release` (key `<session-id>:handoff-review`), then to `done` with the new version.
   - Exit `4`: someone changed it, or the lease was lost (`lease_lost`). Re-read with `get`, report both versions, don't retry.
   - Exit `2`: transition not allowed (`invalid_transition`), or not yours to make (`forbidden`: only the Operations Manager sets a ship ticket `done`, only the owner a scout ticket). Report it; don't pick another status yourself.
   - Status stays the same (the prompt says `keep`): run `maestro-ticket release <id> --holder <holder> --token <token> --idempotency-key <session-id>:handoff-release --json` instead.

## Reply

```yaml
status: done
summary: T-12 → in_review, released; handoff posted
evidence:
  - maestro-ticket comment T-12 → exit 0
  - maestro-ticket transition T-12 --to in_review --expect-version 9 → exit 0, version 10
next: code review
questions: []
```

## Rules

- Run each write once. Idempotency keys make a retried prompt safe; don't change them between tries.
