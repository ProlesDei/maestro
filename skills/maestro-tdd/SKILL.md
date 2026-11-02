---
name: maestro-tdd
description: "Fix a bug or build a feature test first: write the failing test, watch it fail for the right reason, make it pass at the cause, then prove the test catches the change. Use for /maestro-tdd, 'fix this bug', 'X crashes' or 'X returns the wrong value', 'write a failing test', 'add a regression test', building a feature one criterion at a time, pinning behaviour before a refactor, or a Maestro build step. Load it with maestro-working-principles, not instead of it. Not for proving finished work (the maestro-prove-complete skill) or a change no test can show, such as docs."
---

# Test first, then change

Turn the behaviour you want into a test that fails before your change and passes after it. You hand back the test, its runs and the commits, and you never write a test that only looks like it covers the change.

## Start

Open a todo list with one item per phase your path in Phase A names.

## Phase A: Name the behaviour

1. Write one sentence: "given X, doing Y gives Z; it should give W". For a feature, one per acceptance criterion. If you can't, you don't understand it yet: read more before you write a test.
2. Pick the path. A test path is cheap when it runs locally, finishes in about a minute and needs no production access.

| Work | Path |
|---|---|
| bug | B, then C |
| feature | B, then C, for each criterion in turn |
| refactor or perf | D |
| no cheap test path | E |

## Phase B: Watch it fail

1. Write the smallest test that shows it, at the lowest level where it still shows. Put it beside the existing tests for that code, in their style, with one assertion and a name that states the expected behaviour.
2. Run only that test and read the failure. An import error, a fixture typo or a timeout is the wrong reason: fix the test until it fails because the behaviour is missing.
3. For a bug, commit the test alone, naming the bug it shows.
4. From here on, leave the test as it is. If it must change, run this phase again.

## Phase C: Make it pass

1. For a bug, find the `file:line` where it starts, not where it shows, and make the smallest change there. For a feature, write the least code that passes.
2. `git grep` for the same pattern. Fix each place the task covers; list the rest in your reply.
3. Run the test, then each command under `## Checks` in `.maestro/project/project-setup.md`.
4. Commit, naming the cause and the test.
5. For a bug, undo the fix and see the test fail: `git revert --no-commit HEAD`, run the test, then `git revert --abort`. A test that passes without the fix tests nothing.

## Phase D: Pin, then change shape

1. Commit passing tests that pin today's behaviour at the public edges.
2. For each pin, break the code it covers by hand, see the pin fail, then restore it with `git checkout -- <path>`.
3. Change the code in small commits; run the pins after each. For perf, measure before and after the way the **maestro-prove-complete** skill says, and change only what the numbers show.

## Phase E: No cheap path

1. Say so in one line, with the reason: it needs production, takes too long or needs hardware.
2. Write a repro script in a `mktemp -d` folder. Run it before and after the fix, and keep both outputs.
3. If nothing reproduces it, stop with what you tried. A guessed fix is not a fix.

## Never

- Edit, weaken or skip a test, or mark it flaky, to make it pass.
- Mock the code under test. Mock only what crosses the process edge: network, clock, and the filesystem when you must.
- Sleep or use real time. Use a fake clock and fixed seeds.
- Put the change and unrelated edits in one commit.

**Reply:** each test (path and name) with its failure before and pass after; for a bug, its failure with the fix undone, the cause `file:line` and each other place with it; for a refactor or perf, each pin and the break it caught, and the before and after numbers; the commits. With no cheap path: the reason and both repro outputs.
