---
name: maestro-investigate
description: Answer a question by reading, with a source for every claim, changing nothing that ships; use it to frame and run a scout investigation, or to gather facts for a plan or an escalation answer.
disable-model-invocation: true
metadata:
  internal: true
---

# Investigate

You find out what's true, with evidence someone else can check. You don't fix what you find.

## Inputs

- Ticket id and the question, or what the step needs to understand.
- Where to look, if the prompt says.

## Steps

1. **Frame:** the question in one sentence, the possible answers, what evidence would settle each, and where to look. Write it in the scratchpad.
2. **Gather:** read code, docs, tickets (`maestro-ticket get`, `list`, `events`), `.maestro/project/memory/` and git history (`git log`, `git show`). Each finding gets a source: `file:line`, a ticket id and comment, or a command with its output path under `$(git rev-parse --git-common-dir)/maestro/evidence/<ticket-id>/`.
3. **Experiment** only when reading can't settle it, and only with commands your session may run: in a temp dir under `$(git rev-parse --git-common-dir)/maestro/scratch/<bot-id>/`, recorded as `temp_dir` in the scratchpad. Never on a branch that ships. If you can't run it, list it as unknown.
4. **Weigh** each possible answer: the evidence for and against, and how sure you are.

## Reply

```yaml
status: done
summary: likely cause is the cache TTL; one unknown
evidence:
  - src/cache.ts:41 TTL is read in seconds, set in ms
findings:
  - {claim: "TTL is 1000x too long", source: "src/cache.ts:41"}
unknowns: [production TTL setting]
next: write the report
questions: []
```

## Rules

- Read only outside your temp dir.
- A claim without a source goes under `unknowns`.
