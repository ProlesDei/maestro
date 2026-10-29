---
name: maestro-build-change
description: Implement a planned change or a list of fixes on the ticket's branch, with tests, and commit it clean; use it at a playbook's build or fix step.
disable-model-invocation: true
metadata:
  internal: true
---

# Build a change

You make the change the plan or the fix list asks for, prove it with tests, and leave the worktree clean.

## Inputs

- Ticket id, holder, the worktree path and its `maestro/<id>-<slug>` branch.
- The plan in the scratchpad, or the numbered list of items to fix.
- Optional: the output path of a failed check run to start from.

## Steps

1. **Check where you are:** `git -C <worktree> rev-parse --abbrev-ref HEAD` prints the ticket's branch. If not, reply `failed`.
2. **Change** what the plan or each listed item asks, the way the named example does it.
3. **Test:** each acceptance criterion has a test that fails without the change. Run the project's test command from "Automated checks" in `.maestro/project/project-setup.md` and fix until it passes.
   - Bug: the committed repro now passes.
   - Refactor: the captured tests stay unchanged; commit in small steps, tests passing after each.
   - Performance: change only the hot spots the trace named.
4. **Commit:** `git add` only the files you changed, then `git commit` with a message that says what changed and why. `git -C <worktree> status --porcelain` prints nothing.

## Reply

```yaml
status: done
summary: --json flag added to list, 2 tests
evidence:
  - npm test → exit 0, 214 passed
  - commit 3f2a9c1: src/cli/list.ts, test/list.test.ts
items: []        # fix lists only: each item and the files changed, or why not
next: run the checks
questions: []
```

## Rules

- Touch only what the ticket needs: no drive-by refactors, unrelated formatting, or new dependencies the ticket doesn't ask for. Reply `blocked` instead.
- Never edit a repro, captured test or trace script to make it pass, and never skip or weaken a test.
