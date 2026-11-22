---
name: maestro-qa
description: Test a batch of reviewed PRs in the running app and merge each pass. Use when the planner sends a batch or a single PR, or to reproduce a bug. Not for reading code for bugs; that is review's job.
---

# QA

Never change the code.

Read this skill, its linked files and `.maestro/memory/qa.md` from the primary checkout, and use its board launcher. Read the project's rules from the PR's base branch.

## Before you test

- Read each PR and its board ticket for the goal, checks, scope and review notes. Record each current head sha and check that the latest review comment opens `Review pass at <sha>` for that head. Block any PR without that pass and report it to the planner.
- In one fresh worktree of your own, record the latest base SHA and merge the reviewed heads into it in ticket filing order. For a conflict, try the PR on the base alone. If it conflicts there, fail that PR. Otherwise fail the later-filed ticket and tell the planner which paths overlap.

## Run it

For a batch of docs or memory changes alone, read the changed text against each ticket and go to "Judge".

Build and start the app once for the batch, the way production does, on fresh data and a free port. Use one browser session. Never touch a shared or production service. Memory says how; if neither memory nor the repo does, block and ask.

Prove each ticket's checks in the running app the way a user would. For anything a server runs, follow [server.md](server.md). For anything a user sees, also follow [browser.md](browser.md). For a CLI, run the command. Markup, a 200 or a green test proves nothing about what a user sees. Tests the author wrote repeat the author's guess; trust tests a person wrote or approved, and your own run.

Match the depth to the stakes and the ticket's risk. A change to stored data, logins or money also gets test data made on the base, a second user, and a rollback to the base.

Run the project checks and walk each critical flow in memory once. For a failure outside a ticket's checks, try the same step on the base.

If a failure might come and go, rerun it from a fresh start 3 times and give the rate. Skip reruns for failures that always happen the same way, such as build errors. A failure that comes and goes still fails.

## Judge

Record the Git tree ID for each pass.

Fail only on blockers in the Both roles and QA only sections of `.maestro/skills/code-review/blockers.md`.

List other problems as follow-ups, worst first.

Missing a tool, account or secret you need is a block, not a pass. Test the rest, then ask for what's missing.

## Find the source

If the app won't build or start, try the base alone with the same setup. If it fails there, post `QA blocked` with the reason `base broken` on every PR in the batch. Stop the batch and report one shared block with the base SHA and affected PRs to the planner.

For other failures, name one source ticket. Use logs and traces as clues, even when they point into one PR.

If the cause is unclear, split the batch in half on the base, keeping the code the check needs to run. Rerun only the failing step and split a failing half until one source PR remains. If both halves pass alone, find a smallest subset that still fails, drop one PR whose removal clears that subset's failure, then retest the rest.

After a fail or block, drop each source PR and any blocked PRs. If any remain, rebuild them on the latest base. Rerun the project checks, critical flows and any failing step, and run any ticket checks you missed. Rerun kept tickets' checks that a drop could affect, including checks that use shared middleware, auth, config or data the dropped PR touched. Keep the other passes and skip the dropped tickets' checks. Repeat for any further failure.

## Report

Post each PR's own verdict, including any fail or block. Keep the first line exact so code can sort on it.

```text
QA <pass | fail | blocked> at <head sha> on <base branch> at <base sha> in <batch>
- <check>: <pass | fail | not checked>: <what you did, what you saw, evidence>
Flows: <each critical flow: pass | fail | not checked> | not run
Setup: <each way your run differs from production>
Follow-ups: <problems that didn't block, worst first> | none
[Remember: <qa.md section>: <line>]
```

A fail says how you found the source, the exact steps from a fresh start, what you expected, what you saw and the rate.

## Merge the passes

Take the passing PRs in batch order. Before each merge, build the exact tree the project's merge method would land from that PR's current base and reviewed head. If the head moved, block and tell the planner. Reuse a pass only for the tree and checks it covered. If the tree changed, rerun the project checks and affected critical flows and ticket checks. Run every added or edited ticket check even if the tree is unchanged. Update the verdict after a rerun. On a build or start failure, follow [Find the source](#find-the-source).

Wait for the host's required checks on the tested head to pass. If any fail, post a `QA fail` verdict, tell the planner and skip the merge.

Right before each merge, check the base SHA and ticket checks again. If either changed, rebuild and check again first. Read the epic and skip the merge during an active stop unless the ticket is a recorded fix (see “Ticket board”). Merge directly with the tested head SHA as the guard. If the merge landed on a different base, rerun the affected checks there. Post any new failure as a QA fail and flag it to the planner as a post-merge fix. If the host refuses after the required checks pass, post a `QA blocked` verdict with the host's reason and tell the planner. Handle any fail or block before the next merge.

Stop everything you started. Give the planner each merged PR URL and ticket ID, each sent-back ticket and its failure, and any blocks.

## Remember the app

Never edit `.maestro/memory/qa.md`. In the verdict, add one `Remember:` line per verified fact the next run would relearn. Use your own words and leave out secrets.
