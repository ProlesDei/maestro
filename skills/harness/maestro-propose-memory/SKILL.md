---
name: maestro-propose-memory
description: Write a memory proposal into the curated inbox for the Operations Manager to review; use it at wrap-up only when the session learned something durable.
disable-model-invocation: true
metadata:
  internal: true
---

# Propose a memory

You suggest one lasting fact for the project or role memory. You never write memory directly: the Operations Manager curates the inbox and decides what's kept.

## Inputs

- Ticket id (or the duty name) and session id.
- What was learned, and why it would save the next session time: a convention, a pitfall, a command, a part of the codebase map.

## Steps

1. **Is it durable?** It must hold beyond this ticket, and must not already be in `.maestro/project/memory/`. Check there first. One-off details belong in the handoff note instead. If nothing qualifies, reply `done` with `proposals: []`.
2. **Write** one file per fact at `$(git rev-parse --git-common-dir)/maestro/memory-inbox/<ticket-id>-<session-id>-<n>.md`:

```markdown
---
tier: project | role
role: junior-software-engineer      # role tier only
kind: convention | pitfall | command | map | decision
about: [src/export/csv.ts, package.json]
source: T-12
evidence: test run in the handoff note of T-12
---
CSV export tests need TZ=UTC; the fixtures are written in UTC.
```

   - `about` lists the files the fact is about, so it can be flagged when they change.
   - The body is at most five lines.

## Reply

```yaml
status: done
summary: 1 proposal
evidence:
  - wrote memory-inbox/T-12-6f1c-1.md
proposals: [memory-inbox/T-12-6f1c-1.md]
next: hand off
questions: []
```

## Rules

- Write only in `memory-inbox/`. Never on a ticket branch, and never in `.maestro/project/memory/`.
- Propose a change to Maestro itself (a skill, playbook or persona) as `kind: decision` with `tier: role`. Never edit Maestro's files.
