---
name: maestro-check-intake
description: List the work waiting for your role, in priority order, at the start of a session; use it before claiming anything.
---

# Check intake

You find what this role should work on next and report it as an ordered list. You don't claim, change or comment on anything. The bot picks the playbook and claims in the next step.

## Inputs

- Your bot id, session id and role.
- Requests the bot passes in the prompt (for example a user message or an approval answer).
- Scheduled duties the bot says are due.

All paths below are relative to `$(git rev-parse --git-common-dir)/maestro/`.

## Steps

1. **Resume.** Look for scratchpads at `scratch/<bot-id>/*.md`. For each, run `maestro-ticket get <id> --json`.
   - Ticket open and no live lease (none, or expired): a resume candidate. List it first.
   - Ticket `done` or `cancelled`, or leased by another holder: report the scratchpad as stale. Don't delete it.
2. **Gather candidates in this order.** Use `maestro-ticket list ... --unclaimed --json` for each queue in the table.
   1. Requests
   2. Work sent back
   3. New work
   4. Scheduled duties
3. **Sort** within each category: priority (0 first), then lowest id.
4. **Read details** with `maestro-ticket get <id> --json` for the top candidates only (at most 5). Note the version and the latest comment.
5. Reply.

| Role | Requests | Work sent back | New work | Scheduled duties |
|---|---|---|---|---|
| chief-of-staff | Prompt items; `blocked` tickets whose latest comment starts `Escalation:` | | | Status brief |
| planner | `todo --role planner` (planning tickets) | | | Groom the board |
| senior-software-engineer | Prompt items; `todo --role senior-software-engineer` whose latest comment starts `Escalation:` | `in_progress --role senior-software-engineer` | `todo --role senior-software-engineer` | |
| junior-software-engineer | Prompt items | `in_progress --role junior-software-engineer` | `todo --role junior-software-engineer` | |
| code-reviewer | | | `in_review` | |
| qa-lead | | | `in_qa` | |
| operations-manager | Prompt items; `todo --role operations-manager` (infrastructure requests) | | `ready_to_release` | Improvement loop |

A ticket appears once, in the first category it matches.

## Reply

```yaml
status: done            # done even when nothing is waiting
summary: 3 candidates; resume T-12 first
evidence:
  - maestro-ticket list --status todo --role junior-software-engineer --unclaimed --json → exit 0, 2 tickets
candidates:
  - {id: T-12, category: resume, status: in_progress, priority: 1, version: 7, scratchpad: scratch/jse-1/T-12.md}
  - {id: T-15, category: sent-back, status: in_progress, priority: 2, version: 4, latest: "Handoff: changes requested ..."}
  - {id: T-18, category: new-work, status: todo, priority: 2, version: 1}
stale_scratchpads: []
next: claim T-12
questions: []
```

For `in_review` tickets, add `author_family` from the latest `Handoff:` comment.

## Rules

- Read only. Run `maestro-ticket list`, `get` and `events`, and read scratchpads. Nothing else.
- Don't read code or diffs. Intake is about tickets, not work.
- Don't claim. If a command exits non-zero, report the exit code and the error JSON, then carry on with the other queues.
- Empty intake is a normal result: `status: done` with no candidates.
