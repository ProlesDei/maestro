---
name: maestro-prove-complete
description: "Prove each acceptance criterion on the running code at one commit, with evidence a stranger can check without asking you. Use for /maestro-prove-complete, 'is it done?', 'prove it works', or before you call any work done."
---

# Prove it's complete

"Done" is a claim; the proof is a set of outputs a skeptic can check, tied to one commit. Reading the code or a summary is not proof. You prove and never fix: a failure goes back to the build with its evidence.

## Start

Open a todo list with one item per phase below.

## Phase A: Pin what you prove

1. In the worktree, `git status --porcelain` prints nothing. Your sha is `git rev-parse HEAD`. Proof from a dirty tree or another sha doesn't count, save what step 4 carries.
2. Number each acceptance criterion from `maestro-ticket get <id> --json`.
3. Read `## Run` in `.maestro/project/project-setup.md` for how the project runs, else the README and build files.
4. On a rerun after a `Kickback:`, prove only what changed: each item it names, and each criterion whose code or test, or anything they call, changed since the sha of the latest `Handoff:`. Mark the rest `carried: <that sha>`; it counts as a pass. Never prove a criterion again that nothing touched.
5. Save each file as `<n>-<slug>.<ext>` in `$(git rev-parse --git-common-dir)/maestro/evidence/<id>/<sha>/`, so a rerun's proof never mixes with an older commit's.

## Phase B: Prove each claim

Pick the strongest proof for each:

| Claim is about | Proof | Keep |
|---|---|---|
| logic | a test that fails without the change and passes with it | command, exit code, counts, test name |
| a bug | the repro failing on the base commit and passing on yours | both outputs |
| a CLI | the real command against the built binary | command, output excerpt, exit code |
| an HTTP API | `curl -i` against the running app | request, status, the headers that matter, body excerpt |
| a UI | drive the real page as [browser.md](../maestro-qa-lead/references/browser.md#open-a-browser-of-your-own) says; a run with no `<run>` of its own names one `eng-<id>-<6 random letters from the OS>` and uses Phase A step 5's folder | screenshot of the state the criterion names, URL, steps to get there |
| data or a migration | a query before and after, on a scratch database | both results, row counts |
| speed | one benchmark run 5 or more times before and after, on one machine | median and spread of each |
| background work | the log lines that show it ran, with timestamps | excerpt, never the whole log |

Stop every process you started.

## Phase C: Try to break it

1. Feed it empty input, wrong input, each limit and one past it, error paths, a restart (does state survive?) and a double submit.
2. Check one nearby feature that worked before and must still work.
3. Run a test that looks flaky 3 times and report each result, not the best.
4. Run each new test on the base commit. One that passes there proves nothing.

## Phase D: Report

1. Write one line per claim: `n. <claim>: pass | fail | carried: <sha> | not checked: <evidence>`.
2. Give `not checked` its reason; it is never a pass. A criterion nobody can check as written blocks the work.
3. Give each failure its steps, expected result, actual result and error line.
4. Name a problem outside the criteria on its own line; it doesn't fail the work.

**Reply:** the sha, one line per claim (n, pass | fail | carried | not checked, evidence), one line per failure (steps, expected, actual), problems outside the criteria, and the evidence folder.
