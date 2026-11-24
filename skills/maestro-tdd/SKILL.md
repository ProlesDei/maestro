---
name: maestro-tdd
description: "Write a failing test, make it pass, and prove it catches the change. Use for /maestro-tdd, 'write a failing test', 'add a regression test', 'write tests for X' or a ticket that asks for tests. Not for unrequested tests, proving finished work or changes no test can show, such as docs."
---

# Test first, then change

Turn the behaviour you want into a test that fails before your change and passes after it. You hand back the test, its runs and the commits, and you never write a test that only looks like it covers the change. Maestro writes no tests unless a person asks for them. No one asked? Stop, and build the change as the **maestro-software-engineer** skill says.

## Phase A: Name the behaviour

1. Write one sentence: "given X, doing Y gives Z; it should give W". For a feature, one per acceptance criterion. If you can't, you don't understand it yet: read more before you write a test.
2. Pick the path. A test path is cheap when it runs locally, finishes in about a minute and needs no production access.

| Work | Path |
|---|---|
| bug | B, then C |
| feature | B, then C, for each criterion in turn |
| refactor or perf | Follow [maestro-refactor-code](../maestro-refactor-code/SKILL.md). Phases B and C cover pins and changes. |
| no cheap test path | D |

## Phase B: Watch it fail

1. Write the smallest test that shows it, at the lowest level where it still shows. Put it beside the existing tests for that code, in their style, with one assertion and a name that states the expected behaviour.
2. Run only that test and read the failure. An import error, a fixture typo or a timeout is the wrong reason: fix the test until it fails because the behaviour is missing.
3. For a bug, commit the test alone, naming the bug it shows.
4. From here on, leave the test as it is. If it must change, run this phase again.

## Phase C: Make it pass

1. For a bug, find the `file:line` where it starts, not where it shows, and make the smallest change there. For a feature, write the least code that passes.
2. Search for the same pattern. Fix each place the task covers and list the rest in your reply.
3. Run the test and the tests beside it. The repo's full checks, as the agent notes name them, run once, before the PR, as **maestro-working-principles** says: on a busy machine they cost minutes per run.
4. Commit, naming the cause and the test.
5. Prove the test catches the change. For a bug, temporarily undo the fix, run the test, then restore the fix. For a feature, break the new code by hand where it matters most, run the test, then restore the code. The test must fail without the change.

## Phase D: No cheap path

1. Say so in one line, with the reason: it needs production, takes too long or needs hardware.
2. Write a repro script in a fresh temp folder. Run it before and after the fix, and keep both outputs.
3. If nothing reproduces it, stop with what you tried. A guessed fix is not a fix.

## Never

- Edit, weaken or skip a test, or mark it flaky, to make it pass.
- Mock the code under test. Mock only what crosses the process edge: network, clock, and the filesystem when you must. Build an outside API's fake from a real response, recorded and redacted, never by hand, and name its source (with none to record, say so, so QA checks that shape).
- Use a fake secret shorter than a real one. Make it full length, and check no part of it, not even a prefix, survives redaction.
- Sleep or use real time (use a fake clock and fixed seeds), or put the change and unrelated edits in one commit.

**Reply:** each test (path and name) with its failure before and pass after; for a bug, its failure with the fix undone, the cause `file:line` and each other place with it; the commits. With no cheap path: the reason and both repro outputs.
