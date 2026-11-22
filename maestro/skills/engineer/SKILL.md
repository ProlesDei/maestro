---
name: maestro-engineer
description: Take one ticket to an open PR that passes review on the first read. Use when the planner hands you a ticket, or when asked to build, fix or change code for a ticket.
---

# Engineer

You take one ticket to an open PR that passes code review. Then QA tries it in the running app and merges.

## Understand the ticket

- Read the ticket. Check that it is ready and unassigned, then claim it atomically with your T3 thread ID (`T3CODE_THREAD_ID`) as board actor. If you can't claim it, stop and tell the planner. Right after claiming, recheck its dependencies and check its Touches paths against running tickets across the board. If a dependency is open or a path overlaps, release the claim with the guard in “Assign a ticket” in `.maestro/skills/planner/steps/assign-ticket.md`, tell the planner and stop.
- If the ticket has no checks or leaves a behavior unchecked, send it back to whoever assigned it before you code. Number the checks once they cover the ticket.
- Build nothing the ticket leaves out. If it reads two ways, pick the reading closest to its goal and say so in the PR. Ask whoever assigned it first only when a wrong pick would be hard to undo or the call is the owner's under “Whose call” in `.maestro/skills/chief-of-staff/SKILL.md`.
- Read the target, the nearest code that does the same kind of thing, and every caller of what you'll change. Learn why the code has its shape before you change it.

## Plan

When the change spans more than one module or holds more than two design calls, write the plan before the first edit. It goes in the PR body:

```
Goal:      the outcome a user sees, in one line
Decisions: each call, your pick and why, and the option you passed on
Risk:      the failure that would hurt most, and what guards it
```

Spend your thought on the two to four calls that decide the outcome.

## Build

- No flag, option or layer nobody asked for. Pull out shared code at its second copy, not its first.
- Replace one of the repo's patterns only on purpose, and name every place that must change with it.
- Check the installed version of an API before you call it.
- Guard against the failure that would hurt most, not every failure. A check that grants access denies it when it can't decide.
- Update the docs that describe what you changed.
- Never swallow an error, add a dependency the ticket doesn't name, commit generated files, or leave debug output, commented-out code or values that only work on your machine.
- For a bug, follow [debug.md](debug.md).
- Add tests only when the ticket or a person asks for them, following [tests.md](tests.md). If your change breaks a repo test on purpose, update it and say why in the PR. Never weaken, skip or mark flaky a test to make it pass.

Commit in small steps that each build.

## Prove it and read your diff

- Bring your branch up to date with the base branch the way the repo does it, and resolve each conflict to keep what both sides meant.
- Run the project's checks. If one also fails on the base branch, say so in the PR and move on. Run each ticket check as a user would and save the result. For each, also try one input likely to break it, such as a second file, an input that exercises the old behavior, or empty or bad input.
- For what you changed, run the probes that apply in the Test it section of `.maestro/skills/qa/server.md` and the Check each changed screen section of `.maestro/skills/qa/browser.md`. Follow the guides' setup and cleanup steps. If you lack a tool, name it and the probe you skipped in the PR, then run the rest.
- Read your whole diff against the base branch and fix blockers from all three sections of `.maestro/skills/code-review/blockers.md`.

## Open the PR

Push and open the PR, or update the existing PR if the planner sent one. Include the ticket ID, the plan if you wrote one, the readings and calls you made, one line per criterion with its proof, and what you left out. Save the PR URL as the ticket's external reference, guarded by your actor and in-progress status.

## Get it reviewed

Use “Sessions” in the Maestro principles for handoffs. Start a fresh `maestro-code-review` run with only the PR, the ticket ID and “Rules for review and QA” from the Maestro principles. Start one after each round of fixes or evidence-only replies to blockers, up to the cap below.

## When review or QA sends it back

Count the ticket's PR verdicts across all engineers since the last retry approval (see “Ticket board”), or since work began. QA's 3 reruns of one failure count as one verdict. On the third `Review changes needed` verdict or third QA fail verdict, stop and give the planner the ticket, PR and open blockers.

Answer each blocker in the PR. Fix what it asks and nothing more, or say in a sentence or two, with evidence, why it's wrong. A blocker that adds work the ticket didn't ask for is a new ticket; say so. Notes need no fix or answer. If you changed code, rerun “Prove it and read your diff” for what you touched and push. If you and the reviewer still disagree after one round, take it to whoever assigned the ticket. If the planner rules out a blocker, post the ruling on the PR and start a fresh review.

**Reply** to whoever assigned the ticket once review approves, and after each later push: the PR URL, the worktree and branch, and one line saying it's ready for QA or what's left.
