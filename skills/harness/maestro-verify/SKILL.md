---
name: maestro-verify
description: Prove a change works by running it against each acceptance criterion and the definition of done, saving evidence files; use it before submitting and at QA.
disable-model-invocation: true
metadata:
  internal: true
---

# Verify

You show, with evidence from the running app, whether a ticket does what it says. You run it; reading the code is not proof. You never fix anything.

## Inputs

- Ticket id, holder, the ticket's worktree and type.
- Optional: the criteria to focus on, or a draft guide to follow in place of `.maestro/project/verify.md`.

## Steps

1. **Read** the ticket's acceptance criteria (`maestro-ticket get <id> --json`), and the definition-of-done rows for its type in `.maestro/project/project-setup.md`. If `.maestro/project/verify.md` exists, it says how to start and drive this project's app. Follow it.
2. **For each criterion and each row,** check it the way it says:
   - **Command:** run it in the worktree. Record the command, the exit code and test counts.
   - **Evidence:** exercise the real app: run it, call it or click through it. Save the output, response or screenshot under `$(git rev-parse --git-common-dir)/maestro/evidence/<ticket-id>/<n>-<slug>.<ext>`.
   - **Judgment:** state what you looked at and what you concluded, with paths.
   - **Later gate:** a row that review, QA or the merge queue proves (for example "review passed") is marked `later` before submit, never failed or passed.
3. **Edge cases:** empty and wrong input, limits, error paths, and one check that something nearby still works.
4. **Processes** you start (a dev server, for example) go in the scratchpad under `processes` with their PID, so clean-up can stop them.

## Reply

```yaml
status: done            # done if every item passed; failed if any failed
summary: 3 of 3 criteria pass; definition of done met
evidence:
  - "AC1 export has a header row: pass, evidence/T-12/1-header.csv"
  - "Tests pass: npm test → exit 0, 212 passed"
  - "Verified on the real app: pass, evidence/T-12/2-export.txt"
failures: []            # each: criterion, steps to reproduce, expected, actual, evidence path
next: ready for review
questions: []
```

## Rules

- An item you couldn't check is not a pass. Mark it `not checked` with the reason, and reply `blocked` if it can't be checked as written.
- Evidence files hold excerpts, never whole logs.
- Problems outside the criteria don't fail the ticket. List them under `next` as possible new tickets.
