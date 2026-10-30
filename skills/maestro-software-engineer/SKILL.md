---
name: maestro-software-engineer
description: "Make one code change by hand in this session: plan it against the closest existing code, build it test first, review your own diff as a stranger would, and fix what review or QA sent back. Use for /maestro-software-engineer, 'make this change', 'fix the kickback on T-12', or work you want to drive yourself instead of the `work` playbook."
argument-hint: "[ticket id | what to change]"
disable-model-invocation: true
---

# Make a change by hand

Read the code, change it and prove it, in your own session. `maestro-play run work --ticket <id>` does the same job through the playbook and moves the ticket; this skill is for a change you drive. You write no ticket.

Follow the **maestro-working-principles** skill.

## Start

Open a todo list with one item per phase below.

## Phase A: Read and plan

1. Read the user's words, or the whole ticket (`maestro-ticket get <id> --json`). Restate the goal in one sentence and number the acceptance criteria.
2. Read the latest `Kickback:` and `QA: fail` comments. Their items come first.
3. Find the closest code that already does the same kind of thing (a command, endpoint, test or migration), by path and lines, and copy its shape.
4. Give each criterion the test that will prove it. `git grep` each symbol you change and read every caller.
5. Name the risks (behaviour callers rely on, data, security, speed, concurrency) and how you cover each.
6. Stop and ask if the change needs a decision the ticket doesn't make, a new dependency, or a change to an interface other code relies on. If it is really two tickets, propose the split.

## Phase B: Build

1. Work on a branch, never the main branch (the line under `## Main branch` in `.maestro/project/project-setup.md`).
2. Build each criterion test first, the way the **maestro-tdd** skill says. Make the smallest change that passes, in the style of the example: no abstraction or flag nobody asked for.
3. Update the docs the change makes wrong.
4. Run each command under `## Checks` in `project-setup.md` until it passes. Never skip, weaken or edit a test to make it pass.
5. Commit in small steps that each pass, saying what changed and why.

## Phase C: Review your own diff

Read `git diff <main>...HEAD` top to bottom as a stranger would. Look for:

- debug prints, commented-out code, stray TODOs, unrelated formatting;
- what happens when each call fails, times out or returns empty;
- names that don't say what the thing is in the codebase's own words;
- tests that assert no behaviour, or wouldn't fail if the change were wrong;
- secrets, hard-coded paths, values that only work on this machine;
- files outside the ticket's areas.

Fix what you find and run the checks again.

## Phase D: Fix what came back

1. Number each review or QA item. Fix it, answer it with evidence, or disagree in one or two sentences.
2. Fix exactly what was asked. Rerun the checks and the proof for every item you touched.
3. Put a problem outside the change in the reply; don't fix it.

**Reply:** the branch and commits, one line per criterion with its test and result, each check's command and exit code, what self-review found and fixed, each review or QA item and what you did, and anything outside the change.
