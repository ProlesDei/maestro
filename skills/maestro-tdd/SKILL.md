---
name: maestro-tdd
description: Use when you're fixing a bug and there's a cheap local test path. Write the failing test first and watch it fail for the right reason. Then make the smallest fix at the cause, and prove the test catches the bug. Also used for each acceptance criterion in a build.
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Test first, then fix

A cheap test path is one that runs locally, finishes in under a minute or so, and needs no production access. If there isn't one, see "No cheap path" below.

## The loop

1. **Name the behaviour.** Write one sentence: "given X, doing Y gives Z, but it should give W". If you can't write it, you don't understand the bug yet, so read more before you write a test.
2. **Write the smallest test that shows it.** Test at the lowest level where the user-visible symptom still shows. Put it next to the existing tests for that code and follow their style. Give it one assertion that pins the bug, and a name that states the expected behaviour.
3. **Watch it fail for the right reason.** Run only that test and read the failure message. An import error, a fixture typo or a timeout is the wrong reason. Fix the test until it fails because of the bug.
4. **Commit the failing test on its own** (`test: reproduce <bug>`). In a playbook, the baseline step does this and the build step finds the test already there.
5. **Fix the cause, not the symptom.** Find the `file:line` where the cause is, not where the bug shows. Make the smallest change that fixes it. Then look for other places with the same cause (`git grep` the pattern), and fix each one or list it.
6. **Watch it pass.** Run the test, then the whole suite with the command under "Automated checks" in `.maestro/project/project-setup.md`.
7. **Prove the test bites.** Undo only the fix in your working copy, run the test and see it go red. Then put the fix back. If the test passes without the fix, it tests nothing.
8. **Commit the fix**, saying what caused the bug and naming the test.

## Features use the same loop

Every acceptance criterion gets a test that fails before the change and passes after it. Write the test, see it fail, write the code, see it pass. Then do the next criterion.

## Never

- Never edit or weaken the test to make it pass, or mark it skipped, flaky or expected-to-fail.
- Never mock away the code that has the bug. Mock only what crosses the process boundary (network, clock, filesystem when needed).
- Never use sleeps or real time. Use a fake clock and fixed seeds.
- Never put the fix and unrelated changes in the same commit.

## No cheap path

Say so in one line, with the reason (it needs production, takes too long, or depends on hardware). Write a repro script in the scratch folder instead, run it before and after, and save both outputs. Don't write a unit test that only looks like it covers the bug. If no repro is possible at all, stop with what you tried. A guessed fix is not a fix.
