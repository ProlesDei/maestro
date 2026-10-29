---
name: at-wrap-up
description: Finish a ticket in one batch: memory proposals, handoff note, status change with release, one-line retro, log line, then render the backlog; use it once at the end of every playbook, including when blocked.
---

# Wrap up

You close out this session's work on a ticket in one go, so the next role can pick it up cold. The bot sends this as one batched prompt.

## Inputs

- Ticket id, holder `<bot-id>/<session-id>`, and the version from the last read.
- Target status (for example `in_review`, or `blocked`), and `--role` if the ticket moves to another role. "No change" for scheduled duties with no ticket.
- Playbook name, and the tokens and cost so far. The bot supplies both; you can't see them.

Paths under `scratch/` and `logs/` are relative to `$(git rev-parse --git-common-dir)/agent-team/`. No ticket (a scheduled duty): skip steps 1 to 4.

## Steps

1. **Check the lease.** Run `at-ticket get <id> --json`. If another holder has it, stop and reply `failed`. Don't write anything.
2. **Memory proposals.** Only for something durable that would save the next agent time: a convention, a gotcha, a command. Skip if there's nothing.
   - On a ticket branch: write `.agent-team/memory/proposals/<ticket-id>.md` in the worktree and commit it.
   - Otherwise: add it to the handoff note under `Memory proposal:`. Don't write files outside a branch.
3. **Handoff note.** `at-ticket comment <id> --body "<note>" --idempotency-key <session-id>:wrap-comment`. Start the body with `Handoff:`, or `Escalation:` when someone above you must act (a decision above your authority, or a hand-off to the Senior Engineer). Include:
   - what you did and what's left
   - where: branch, commit, paths
   - evidence: checks run and exit codes, test counts
   - engineers submitting code: `author-family: <family>` (the bot gives it)
   - blocked: what's blocking and exactly what would unblock it
4. **Status.** `at-ticket transition <id> --to <S> --from <current> --expect-version <N> --holder <holder> [--role <R>] --note "<one line>" --release --idempotency-key <session-id>:wrap-status --json`
   - Exit `4`: someone changed it. Re-read with `get`, report both versions, don't retry.
   - Exit `2`: transition not allowed. Report it; don't pick another status yourself.
5. **Retro.** One line, under 120 characters: what slowed me down? Be specific. "Nothing" is a fine answer.
6. **Log line.** Append one JSON line to `logs/<YYYY-MM-DD>.jsonl` (UTC date). Never rewrite existing lines.

```json
{"ts":"2026-10-28T14:02:11Z","ticket":"T-12","role":"junior-software-engineer","holder":"jse-1/6f1c...","playbook":"build-ticket","status":"in_review","tokens":48210,"cost_usd":0.41,"retro":"test fixture setup took three tries"}
```

7. **Render.** `at-ticket render`. A failure here is reported but doesn't undo the steps above.

## Reply

```yaml
status: done
summary: T-12 → in_review, released; handoff posted
evidence:
  - at-ticket comment T-12 → exit 0
  - at-ticket transition T-12 --to in_review --expect-version 9 → exit 0, version 10
  - logs/2026-10-28.jsonl +1 line
  - at-ticket render → exit 0
retro: test fixture setup took three tries
next: code review
questions: []
```

## Rules

- Run each write once. Idempotency keys make a retried prompt safe; don't change them between tries.
- Never put secrets in the note, proposals, retro or log.
- Wrap up even when the work failed. A blocked ticket with a clear note is a good outcome.
