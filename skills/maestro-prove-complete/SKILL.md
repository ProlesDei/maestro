---
name: maestro-prove-complete
description: "Use before you call any work done, when the user asks \"is it done?\" or \"prove it works\", and in a Maestro verify step. Proves each criterion on the running code at one commit, with evidence someone else can check without asking you."
---

# Prove it's complete

"Done" is a claim. The proof is a set of outputs a skeptic can check, tied to one commit. Reading the code is not proof, and neither is a summary. Prove; don't fix. A failure goes back to the build with its evidence.

## 1. Pin what you prove

- **The commit.** In the worktree, `git status --porcelain` prints nothing; your sha is `git rev-parse HEAD`. Proof from a dirty tree or another sha doesn't count.
- **The claims.** Number each acceptance criterion from `maestro-ticket get <id> --json`. Read `## Conventions` in `.maestro/project/project-setup.md` for how the project runs.
- **The place.** In your own session, save each file as `<n>-<slug>.<ext>` in `$(git rev-parse --git-common-dir)/maestro/evidence/<id>/`. In a step, the reply carries the evidence.

## 2. Pick the strongest proof for each claim

| Claim is about | Proof | Keep |
|---|---|---|
| logic | a test that fails without the change and passes with it | command, exit code, counts, test name |
| a bug | the repro failing on the base commit and passing on yours | both outputs |
| a CLI | the real command against the built binary | command, output excerpt, exit code |
| an HTTP API | `curl -i` against the running app | request, status, the headers that matter, body excerpt |
| a UI | drive the real page with a browser tool or Playwright | screenshot of the state the criterion names, URL, the steps to get there |
| data or a migration | a query before and after, on a scratch database | both results, row counts |
| speed | one benchmark run 5 or more times before and after, on one machine | median and spread of each |
| background work | the log lines that show it ran, with timestamps | excerpt, never the whole log |

Stop every process you started.

## 3. Try to break it

- Feed it empty input, wrong input, each limit and one past it, error paths, a restart (does state survive?) and a double submit.
- Check one nearby feature that worked before and must still work.
- Run a test that looks flaky 3 times and report each result, not the best.
- Check that each new test fails on the base commit. A test that passes there proves nothing.

## 4. Report

Give one line per claim: `n. <claim>: pass | fail | not checked: <evidence>`.

- `not checked` always gives the reason. It is never a pass. A criterion nobody can check as written blocks the work.
- Each failure gives the steps, the expected result, the actual result and the error line.
- A problem outside the criteria doesn't fail the work. Name it on its own line.
- Evidence holds excerpts. Name a secret by its variable, never its value.

Reply: the sha, one line per claim (n, pass | fail | not checked, evidence), one line per failure (steps, expected, actual), problems outside the criteria, and in your own session the evidence folder. In a step, put these in the step's reply fields.
