---
name: maestro-review-change
description: Review a ticket's branch read-only and list findings as must fix or suggestion, with file:line, for correctness, security, maintainability and tests; use it at code review.
disable-model-invocation: true
metadata:
  internal: true
---

# Review a change

You judge a change against its ticket and list what's wrong precisely enough to fix without asking. You never fix it yourself.

## Inputs

- Ticket id, branch, and the commit sha from the latest `Handoff:`.

## Steps

1. **Read** the ticket, its acceptance criteria, and the latest `Handoff:`: sha, check results, `author-family`.
2. **Check the head:** `git log -1 --format=%H <branch>` equals the handoff sha. If the branch is missing or the sha differs, reply `failed` with both.
3. **Read the change:** `git diff <main branch>...<branch>`, the whole of each changed file, and the callers of changed functions.
4. **Check:**
   - Correctness: each criterion is implemented, and a test proves it. Edge cases, errors, limits, ordering.
   - Security: input validation, authorization, injection, secrets in code or tests, unsafe defaults.
   - Maintainability: matches the code around it; no duplication, dead code or change beyond the ticket.
   - Tests: they'd fail if the change were wrong. Nothing skipped or weakened.
5. **Each finding:** `must fix` (wrong, unsafe, a criterion unmet, changed behaviour untested) or `suggestion`, with `file:line`, what's wrong, why, and a suggested fix.

## Reply

```yaml
status: done
summary: 1 must fix, 2 suggestions
evidence:
  - git diff main...maestro/12-json-flag → 3 files read
findings:
  - {severity: must fix, where: "src/cli/list.ts:30", what: "empty list prints null", fix: "print []"}
must_fix: 1
next: decide
questions: []
```

## Rules

- Read only.
- A secret in the diff: don't repeat it; give its `file:line` and say how bad it is.
