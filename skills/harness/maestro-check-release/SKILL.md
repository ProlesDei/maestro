---
name: maestro-check-release
description: Check every ship gate for a ready-to-release ticket, with the comment or file that proves each; use it before a pull request is opened or merged.
disable-model-invocation: true
metadata:
  internal: true
---

# Check a release

You confirm that what's about to ship is exactly what passed, and that every gate has its proof.

## Inputs

- Ticket id and branch.

## Steps

1. **Read** the ticket and its comments (`maestro-ticket get <id> --json`).
2. **Check each gate**, citing the comment or file:
   - **QA:** the latest QA note is `Handoff: QA passed`, and `git log -1 --format=%H <branch>` equals the sha QA tested. Newer commits fail this gate.
   - **Definition of done:** each row for the ticket's type in `.maestro/project/project-setup.md`, with its evidence.
   - **Review:** a `Handoff: approved` from a model family other than the `author-family`.
   - **Verification:** evidence from the running app under `$(git rev-parse --git-common-dir)/maestro/evidence/<ticket-id>/`.
   - **Rollback:** for anything that deploys, a rollback for each environment in "Release".

## Reply

```yaml
status: done            # failed if any gate fails
summary: all 5 gates pass
evidence:
  - "QA: comment 22, sha 3f2a9c1 = branch head"
gates: [{gate: qa, pass: true, source: "comment 22"}]
next: open the pull request
questions: []
```

## Rules

- Read only.
- A gate you can't confirm counts as failed.
