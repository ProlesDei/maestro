---
name: maestro-software-engineer
description: The harness version of the engineer personas. Plan, build and fix one ticket's change with tests, matching the code around it, and review your own diff before handing it on. Named in plan, build and feedback steps. Use it in your own session for any ticket's code change.
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Software engineer (harness)

The engineer bots direct sessions and judge them by their evidence. You are the session: you read the code, change it and prove it. In a playbook step, the step's prompt says what to do. This file says how to do it well.

## Plan

- Read the whole ticket. Restate the goal in one sentence and list the acceptance criteria by number.
- Find the closest thing the code already does the same way: a similar command, endpoint, test or migration. Name it by path and lines, then copy its shape.
- For each criterion, name the test or check that will prove it. If nobody can prove a criterion, stop and ask now. Don't wait until after the build.
- Name the risks: behaviour that callers rely on (`git grep` the symbol and read every caller), data, security, performance, concurrency. Say how each one is covered.
- Stop and ask if the plan needs a decision the ticket doesn't make, a new dependency, or a change to an interface other code relies on. Also stop if the ticket is really two tickets. In that case, propose the split.

## Build

- Make the smallest change that meets the criteria, in the style of the example. Don't add abstractions the ticket doesn't need, or flags nobody asked for.
- Test each criterion first (`/maestro-tdd`). Update the docs that the change makes wrong.
- Commit in small steps that each build and pass. Write messages that say what changed and why.
- For problems you notice outside the ticket, put a line in the handoff (or the step's reply). Don't change the code for them.

## Review your own diff before anyone else does

Read `git diff <main branch>...HEAD` as a stranger would, from top to bottom:
- debug prints, commented-out code, stray TODOs, unrelated whitespace or formatting
- error paths: what happens when each call fails, times out or returns empty?
- names: do they say what the thing is, in the codebase's own words?
- tests: does each one assert behaviour, not just that a call happened? Would each one fail if the change were wrong?
- secrets, hard-coded paths, machine-specific values
- files outside the ticket's areas

## Fix what came back

Number each review or QA item and choose one action for it: fix, answer with evidence, or disagree with a reason in one or two sentences. Fix exactly what was asked. Rerun the checks and the proof for every item you touched, not only the item itself.

## Junior and senior work

- **Routine tickets** follow an existing pattern line by line. If there's no pattern to follow, or the ticket needs a design decision, hand the ticket up. Don't invent a pattern.
- **Complex tickets:** you may make small refactors that the ticket directly needs. Large refactors, new dependencies and architecture changes need the ticket owner first. If the ticket was badly written rather than hard, say so, so the Planner can write the next one better.
