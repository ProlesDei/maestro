---
name: example-skill
description: One sentence on what this skill does and when to use it.
---

# Example skill

What this skill is for, in one or two sentences. Harness skills use the `at-` prefix; bot skills don't.

## Inputs

What the prompt gives you: ticket id, holder, paths, values from `.agent-team/environment.md`.

## Steps

1. Step one. Name the exact command.
2. Step two. Say what to check before moving on.

## Reply

What you send back. Harness skills use the prompt-contract structured reply:

```yaml
status: done | partial | blocked | failed
summary: one or two sentences
evidence:
  - what proves it: command and exit code, file path, ticket version
next: what should happen next
questions: []
```

## Rules

- Hard limits: what you must never do.
- When to stop and reply `blocked` instead of guessing.
