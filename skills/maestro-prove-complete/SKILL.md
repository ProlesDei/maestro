---
name: maestro-prove-complete
description: Prove that work is done, with evidence someone else can check without asking you. That means test output, logs, screenshots, HTTP transcripts and before/after numbers, all tied to one commit. Use it before you call anything done, and in verify steps.
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Prove it's complete

"Done" is a claim. The proof is a set of files that a skeptic can open, tied to the exact commit. Reading the code is not proof, and neither is a summary.

## 1. Pin what you're proving

- The commit: run `git rev-parse HEAD` in a worktree where `git status --porcelain` prints nothing. Proof from a dirty tree or another sha doesn't count.
- The claims: each acceptance criterion (`maestro-ticket get <id> --json`), plus the definition-of-done rows for the ticket's kind in `.maestro/project/project-setup.md`. Number them.
- The place for evidence: the evidence dir your step gives you. In your own session, use `$(git rev-parse --git-common-dir)/maestro/evidence/<ticket-id>/`. Name each file `<n>-<slug>.<ext>`.

## 2. Pick the strongest proof for each claim

| Claim is about | Proof | Save |
|---|---|---|
| logic, a function | a test that fails without the change and passes with it | command, exit code, pass/fail counts, the test's name |
| a bug | the repro failing on the base commit and passing on yours | `repro-before.txt`, `repro-after.txt` |
| a CLI | the real command run against the built binary | the transcript: command, stdout/stderr excerpt, exit code |
| an HTTP API | `curl -i` (or the equivalent) against the running app | request, status, headers that matter, body excerpt |
| a UI | drive the real page with the harness's browser tool or Playwright | screenshot of the state the criterion names, plus the URL and the step that got you there |
| data, a migration | query before and after, on a scratch database | both results, row counts |
| performance | the same benchmark run at least 5 times, before and after, on the same machine | median and spread for each |
| logs, background work | the log lines that show it happened, with timestamps | excerpt only, never the whole log |

Start the app the way `.maestro/project/verify.md` says, if the file exists. Stop everything you started.

## 3. Try to break it

- Empty input, wrong input, limits (off by one either side), error paths, a restart (does the state persist?), and a double submit.
- Check one nearby feature that used to work and still has to.
- Run a flaky-looking test 3 times and report every result, not the best one.
- Make sure each new test actually fails on the base commit. A test that passes there proves nothing.

## 4. Report

Give one line per claim: `n. <claim>: pass | fail | later | not checked: <evidence path>`. Then add the sha and the evidence dir.

- `not checked` always comes with a reason. It is never a pass.
- `later` is for a row that only review, QA or the merge queue can prove.
- Each failure gives the steps, the expected result, the actual result and the evidence path with the error line.
- Evidence files hold excerpts. Redact secrets and name them by variable.
