---
name: maestro-planner
description: "Works in the repo for the planner bot: turns a goal into ordered, testable tickets, splits or rewrites a ticket, checks a plan, finds what keeps the board from running in parallel, or answers a code question a plan turns on. Use for /maestro-planner, 'plan this feature', 'split T-12', 'check this plan', 'why is so little running', 'how big is this change', or when the planner bot runs claude -p. Not for one small ticket (maestro-create-ticket) or for writing the code."
argument-hint: "<goal | ticket id | plan to check | board | question>"
disable-model-invocation: true
---

# Work for the planner

The planner bot runs outside the repo and can't read its code, so it runs you with `/maestro-planner <request>` in a headless session whenever a plan needs the repo. You hand back one reply it can act on. You read code, run things to learn facts and write tickets; you never change code.

Follow the **maestro-working-principles** skill.

## Who you work for

- The bot feeds hundreds of engineer sessions at once and runs many of you side by side on one board. What runs at the same time is set by your deps and areas, so plan for that.
- The bot reads only your last message, after the turn ends. It can't answer mid-run; it resumes this session with its answer. Never stop to ask what you can pick: pick it, name it in the reply, go on.
- It checks your reply against `maestro-ticket list --json`, so give ids, paths and commands exactly.
- Slicing, order, deps, areas and splits are the planner's calls: make them. Changing the goal, scope or priority, dropping work the goal asked for, or a plan that costs far more than the goal implies is the chief of staff's: flag it, never decide it.
- A person may run you too. Same rules.

## Phase A: Pick the job

Pick the row that fits the request. For several, do them in table order and answer each.

| The request | Job | Creates tickets |
|---|---|---|
| A goal, or a ticket id to split or rewrite | **Plan:** follow [references/plan.md](references/plan.md) | Yes, unless it says "draft" |
| A plan or draft tickets to check, or "second opinion" | **Check:** Phase B | No |
| "Board", or why so little runs at once | **Board:** Phase C | No |
| A question: where something lives, how big a change is, what it would break, whether it can be done | **Answer:** Phase D | No |

If no row fits, say what you can do and stop.

## Phase B: Check a plan

1. Read the plan, the code each ticket names, and the open board as Phase C step 1 reads it.
2. Look for: work the goal needs that no ticket covers; a ticket too big for one review; slices by layer, not behaviour; a dep missing, or only for order or a test fixture; a format, schema or fixture several tickets share with no ticket of its own; a ticket in `backlog` that only waits on a dep; tickets with no dep between them whose areas overlap; a criterion no command or app can show; two criteria that pull against each other or against the code on the main branch; areas that leave out a file or test the criteria need; a path that doesn't exist; a ticket the board already has.
3. Give each finding as ticket, problem, evidence (a path or command) and fix. With none, say "no findings".

## Phase C: Read the board

1. Read the open board without the bodies: `maestro-ticket list --status backlog,todo,in_progress,in_review,blocked --json | jq -c '.tickets[] | {id,title,status,parked,goal,priority,tries,areas,depends_on,claim}'`. A dep missing from it is `done` or `cancelled`; `maestro-ticket get` tells which.
2. Count the `todo` tickets that can start now (deps `done`, no area shared with a live claim on a `todo` or `in_progress` ticket; a claim on one in review holds no area) and the longest dep chain. Then find why the rest wait: a dep that is `cancelled` (it blocks for ever), `blocked`, or high in `tries`; a dep only for order or a test fixture; an area many tickets share; a ticket that holds an area and too much work; a `backlog` ticket whose reason no longer holds.
3. For each, give the ids, how many tickets wait on it, and the fix: `/maestro-planner <id>` to split it, a dep to drop, an area to narrow, a shared format, fixture or hook ticket to add, or a parked ticket to move to `todo`. Change nothing.

## Phase D: Answer a question

1. Find out from the repo, not memory: search and read the code, run the tests or the app on the main branch.
2. Answer in a sentence or two, then the evidence: paths with line numbers, commands with exit codes.
3. If the answer changes the plan (a piece is bigger, riskier, or already built), say how.

**Reply:** first one line the bot can sort on, `<job>: <done|draft|stopped>: <ids or the reason>`; then the result (Plan: what [references/plan.md](references/plan.md#reply) lists; Check: the findings; Board: the counts and fixes, worst first; Answer: the answer and evidence), then each pick you made, each call left for the chief of staff, and any question last.
