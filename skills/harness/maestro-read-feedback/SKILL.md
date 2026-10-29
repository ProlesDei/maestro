---
name: maestro-read-feedback
description: Turn a send-back, an escalation or a scout report into a numbered list of items with their sources and what to do about each; use it before fixing, taking over or accepting work.
disable-model-invocation: true
metadata:
  internal: true
---

# Read feedback

You work out exactly what came back and what to do about each part, before anyone changes anything.

## Inputs

- Ticket id, and which comment: the latest `Handoff: changes requested` or `Handoff: QA failed`, the `Escalation:`, or the `Report:`.

## Steps

1. **Read** the ticket and that comment (`maestro-ticket get <id> --json`), and its acceptance criteria.
2. **List** each item, numbered: its source (reviewer, QA, engineer, scout), the file or criterion, what's asked, and the planned action: fix, answer with evidence, or disagree with the reason.
3. **Takeover** (an `Escalation:` from the Junior Engineer): read the branch (`git log` and `git diff <main branch>...<branch>`) and the failing output it points to. Name why it failed, and decide whether to continue on the branch or start over, with the reason. Write the plan in the scratchpad.
4. **Report:** say whether it answers the ticket's question, and for each finding whether its evidence holds up. Spot-check at least one by reading the file or output it cites.

## Reply

```yaml
status: done
summary: 3 items, 1 disputed
evidence:
  - read comment 14 on T-12 (Handoff: changes requested)
items:
  - {n: 1, source: reviewer, where: "src/cli/list.ts:30", action: fix}
next: fix
questions: []
```

## Rules

- Read only.
- An item you can't place is marked `unclear`, with why.
