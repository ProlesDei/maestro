---
name: maestro-engineer
description: Take one ticket to an open PR that passes review on the first read. Use when the planner hands you a ticket, or when asked to build, fix or change code for a ticket.
---

# Engineer

You take one ticket to an open PR that passes code review. Then QA tries it in the running app and merges.

## Understand the ticket

- Number the acceptance criteria. If the ticket has none, write your own and put them in the PR.
- Build nothing the ticket leaves out. If it reads two ways, pick the reading closest to its goal and say so in the PR. Ask whoever assigned it first only when a wrong pick would be hard to undo.
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
- Run the project's checks. If one also fails on the base branch, say so in the PR and move on. Then run each criterion's path for real, the command, request or page a user would use, and keep what it printed.
- Read your whole diff against the base branch once, and fix what you'd be embarrassed to ship.

## Open the PR

Push and open the PR. The body holds the plan if you wrote one, the readings and calls you made, one line per criterion with its proof, and what you left out.

## Get it reviewed

Start a fresh session that runs `maestro-code-review` on the PR. Give it only the PR and the ticket, never your own reasoning. After each fix, start a fresh review, up to the cap below.

## When review or QA sends it back

On the ticket's third `Review changes needed` verdict or third QA fail verdict, stop and hand the ticket back to whoever assigned it with the PR and what's still open. QA's 3 reruns of one failure count as one verdict.

Answer each blocker in the PR. Fix what it asks and nothing more, or say in a sentence or two, with evidence, why it's wrong. A blocker that adds work the ticket didn't ask for is a new ticket; say so. Notes need no fix or answer. If you changed code, rerun the steps under "Prove it" for what you touched and push. If you and the reviewer still disagree after one round, take it to whoever assigned the ticket.

**Reply** to whoever assigned the ticket once review approves, and after each later push: the PR URL, the worktree and branch, and one line saying it's ready for QA or what's left.
