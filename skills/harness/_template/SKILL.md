---
name: example-skill
description: One sentence on what this skill does and when to use it.
disable-model-invocation: true
metadata:
  internal: true
---

# Example skill

What this skill does, in one or two sentences. Harness skills use the `maestro-` prefix.

## Inputs

What the prompt gives you: ticket id, holder, paths, values the bot got at preflight.

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
