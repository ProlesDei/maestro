---
name: maestro-capture-baseline
description: Capture the before-state as committed evidence before code changes, and rerun it after, as a failing repro for a bug, characterisation tests for a refactor, or a repeatable benchmark for performance.
disable-model-invocation: true
metadata:
  internal: true
---

# Capture a baseline

You pin down how the code behaves today, so the change can be judged against it. The baseline is committed before any change to the code it measures.

## Inputs

- Ticket id, holder, worktree and branch.
- Kind: `repro`, `behaviour` or `trace`. `after`: rerun an existing baseline on the changed code.

## Steps

Evidence goes under `$(git rev-parse --git-common-dir)/maestro/evidence/<ticket-id>/`.

- **repro:** write the smallest test that fails on the unchanged code for the reason the ticket describes (a script if no test can reach it). Run it and save the output as `repro-before.txt`. Commit it. It passes, or fails for another reason: reply `blocked` with what you tried.
- **behaviour:** write tests at the public edges the refactor could touch (exported functions, CLI output, API responses) that pass on the unchanged code. List what can't be tested. Commit them. Then prove they can fail: make one deliberate break in the code, not the tests, run them, see one fail, and edit the break back until `git status --porcelain` prints nothing.
- **trace:** write a benchmark or trace script for the slow path and commit it. Run it at least five times on the unchanged code. Save the numbers (median and spread) and a profile, and name the hot spots by `file:line`.
- **after:** rerun the same baseline the same way (same script, run count and machine) and put the numbers next to the earlier ones.

## Reply

```yaml
status: done
summary: repro fails on unfixed code with the reported off-by-one
evidence:
  - npm test -- test/paging.repro.test.ts → exit 1, evidence/T-31/repro-before.txt
  - commit 9b1e004
baseline: {kind: repro, command: "npm test -- test/paging.repro.test.ts", result: fail}
next: find the cause
questions: []
```

## Rules

- Never change a baseline to fit a result.
- Read the code under test, but don't change it here, except for the reverted break in `behaviour`.
