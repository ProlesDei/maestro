---
name: maestro-tdd
description: "Write the failing test first, watch it fail for the right reason, fix at the cause, then prove the test catches the bug. Use for /maestro-tdd, 'write a failing test', 'add a regression test', a bug with a cheap local test path, or a Maestro build step."
---

# Test first, then fix

Make the broken behaviour a test that fails before the fix and passes after it. A test path is cheap when it runs locally, finishes in about a minute and needs no production access; with none, go to Phase E. You never write a test that only looks like it covers the bug.

## Start

Open a todo list with one item per phase below.

## Phase A: Name the behaviour

Write one sentence: "given X, doing Y gives Z; it should give W". If you can't, you don't understand the bug yet: read more before you write a test.

## Phase B: Watch it fail

1. Write the smallest test that shows the bug, at the lowest level where the symptom still shows. Put it beside the existing tests for that code, in their style, with one assertion that pins the bug and a name that states the expected behaviour.
2. Run only that test and read the failure. An import error, a fixture typo or a timeout is the wrong reason: fix the test until it fails because of the bug.
3. Commit it alone, as `test: reproduce <bug>`.

## Phase C: Fix the cause

1. Find the `file:line` where the bug starts, not where it shows, and make the smallest change there.
2. `git grep` for the same pattern. Fix each other place or list it.
3. Run the test, then each command under `## Checks` in `.maestro/project/project-setup.md`.
4. Undo only the fix, run the test, see it fail, and put the fix back. A test that passes without the fix tests nothing.
5. Commit the fix, naming the cause and the test.

## Phase D: Reuse the loop

- **Feature:** each acceptance criterion gets a test that fails before the change and passes after. Write the test, see it fail, write the code, see it pass, then take the next criterion.
- **Refactor:** first commit passing tests that pin today's behaviour at the public edges. Then change the code in small commits that keep them green.

## Phase E: No cheap path

1. Say so in one line, with the reason: it needs production, takes too long or needs hardware.
2. Write a repro script in the scratch folder. Run it before and after the fix, and keep both outputs.
3. If nothing reproduces it, stop with what you tried. A guessed fix is not a fix.

## Never

- Edit, weaken or skip a test, or mark it flaky, to make it pass.
- Mock the code that has the bug. Mock only what crosses the process edge: network, clock, and the filesystem when you must.
- Sleep or use real time. Use a fake clock and fixed seeds.
- Put the fix and unrelated changes in one commit.

**Reply:** the test (path and name), its failure before the fix, its pass after, its failure with the fix undone, the cause `file:line` and each other place with it, and the fix commit. With no cheap path: the reason and both repro outputs. In a step, put these in the step's reply fields.
