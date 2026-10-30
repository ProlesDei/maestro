---
name: maestro-tdd
description: "Use when fixing a bug that has a cheap local test path, when the user asks for TDD, a failing test or a regression test, and in a Maestro build step. Write the failing test first, watch it fail for the right reason, fix at the cause, then prove the test catches the bug."
---

# Test first, then fix

A test path is cheap when it runs locally, finishes in about a minute and needs no production access. With no cheap path, go to phase 5.

## 1. Name the behaviour

Write one sentence: "given X, doing Y gives Z; it should give W". If you can't, you don't understand the bug yet. Read more before you write a test.

## 2. Write the test and watch it fail

Write the smallest test that shows the bug, at the lowest level where the symptom still shows. Put it beside the existing tests for that code, in their style, with one assertion that pins the bug and a name that states the expected behaviour.

Run only that test and read the failure. An import error, a fixture typo or a timeout is the wrong reason: fix the test until it fails because of the bug. Commit it alone, as `test: reproduce <bug>`.

## 3. Fix the cause

Find the `file:line` where the bug starts, not where it shows, and make the smallest change there. `git grep` for the same pattern; fix each other place or list it.

Run the test, then each command under `## Checks` in `.maestro/project/project-setup.md`. Then undo only the fix, run the test, see it fail, and put the fix back. A test that passes without the fix tests nothing. Commit the fix, naming the cause and the test.

## 4. Use the same loop for features and refactors

- **Feature:** each acceptance criterion gets a test that fails before the change and passes after. Write the test, see it fail, write the code, see it pass, then take the next criterion.
- **Refactor:** first commit passing tests that pin today's behaviour at the public edges. Then change the code in small commits that keep them green.

## 5. No cheap path

Say so in one line, with the reason: it needs production, takes too long or needs hardware. Write a repro script in the scratch folder, run it before and after the fix, and keep both outputs. Never write a test that only looks like it covers the bug. If nothing reproduces it, stop with what you tried. A guessed fix is not a fix.

## Never

- Edit, weaken or skip a test, or mark it flaky, to make it pass.
- Mock the code that has the bug. Mock only what crosses the process edge: network, clock, and the filesystem when you must.
- Sleep or use real time. Use a fake clock and fixed seeds.
- Put the fix and unrelated changes in one commit.

Reply: the test (path and name), its failure before the fix, its pass after, its failure with the fix undone, the cause `file:line` and each other place with it, and the fix commit. With no cheap path: the reason and both repro outputs. In a step, put these in the step's reply fields.
