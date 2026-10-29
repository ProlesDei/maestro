---
name: maestro-write-report
description: Post the report comment the reader can act on in two minutes, with an investigation's answer and findings or a workstream spec; use it at a scout's or plan's report step.
disable-model-invocation: true
metadata:
  internal: true
---

# Write a report

You turn findings or a plan into one comment on the ticket, starting `Report:`, so its owner can decide without reading anything else.

## Inputs

- Ticket id, holder `<bot-id>/<session-id>`, the lease token from the claim, and the session id.
- Kind: `findings` (a scout) or `spec` (a workstream plan). The findings or plan are in the scratchpad.

## Steps

1. **Write** the body. Under 40 lines; paths and output files, never pasted logs.
   - **findings:** the answer first, in one or two sentences, with how sure you are. Then each finding with its evidence, what's still unknown, and the recommended next tickets (title, type, role, why). If the work stopped early, the first line after `Report:` says `Partial:` and what's missing.
   - **spec:** outcome, scope and non-goals, approach, risks, and the order of work.
2. **Post** it: `maestro-ticket comment <id> --body-file - --holder <holder> --token <token> --idempotency-key <session-id>:report --json`, with the body on stdin.

## Reply

```yaml
status: done
summary: report posted on T-20
evidence:
  - maestro-ticket comment T-20 → exit 0
next: hand off to the owner
questions: []
```

## Rules

- Every claim has its evidence, or it is listed as unknown.
