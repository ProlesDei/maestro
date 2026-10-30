---
name: maestro-planner
description: "Works in the repo for the planner bot: turns a goal into ordered, testable tickets, splits or rewrites a ticket, checks a plan, or answers a code question a plan turns on. Use for /maestro-planner, 'plan this feature', 'split T-12', 'check this plan', 'how big is this change', or when the planner bot runs claude -p. Not for one small ticket (maestro-create-ticket) or for writing the code."
argument-hint: "<goal | ticket id | plan to check | question>"
disable-model-invocation: true
---

# Work for the planner

The planner bot runs outside the repo and can't read its code, so it runs you with `claude -p "/maestro-planner <request>"` whenever a plan needs the repo. You hand back one reply it can act on. You read code, run things to learn facts and write tickets; you never change code.

Follow the **maestro-working-principles** skill. Ticket commands are `maestro-ticket`'s; if `## Tickets` in `.maestro/project/project-setup.md` names another tool, use its equivalent.

## Who you work for

- The bot reads only your last message, after the turn ends. It can't answer mid-run; it comes back with `claude -p --resume <session_id> "<answer>"`. Never stop to ask what you can pick: pick it, name it in the reply, go on.
- It checks your reply against `maestro-ticket list --json`, so give ids, paths and commands exactly.
- Slicing, order, deps, areas and splits are the planner's calls: make them. Changing the goal, scope or priority, dropping work the goal asked for, or a plan that costs far more than the goal implies is the chief of staff's: flag it, never decide it.
- A person may run you too. Same rules.

## Phase A: Pick the job

Pick the row that fits the request. For several, do them in table order and answer each.

| The request | Job | Creates tickets |
|---|---|---|
| A goal, or a ticket id to split or rewrite | **Plan:** follow [references/plan.md](references/plan.md) | Yes, unless it says "draft" |
| A plan or draft tickets to check, or "second opinion" | **Check:** Phase B | No |
| A question: where something lives, how big a change is, what it would break, whether it can be done | **Answer:** Phase C | No |

If no row fits, say what you can do and stop.

## Phase B: Check a plan

1. Read the plan, the code each ticket names, and `maestro-ticket list --json`.
2. Look for: work the goal needs that no ticket covers; a ticket too big for one review; slices by layer, not behaviour; a dep missing or not needed; tickets with no dep between them whose areas overlap; a criterion no command or app can show; a path that doesn't exist; a ticket the board already has.
3. Give each finding as ticket, problem, evidence (a path or command) and fix. With none, say "no findings".

## Phase C: Answer a question

1. Find out from the repo, not memory: search and read the code, run the tests or the app on the main branch.
2. Answer in a sentence or two, then the evidence: paths with line numbers, commands with exit codes.
3. If the answer changes the plan (a piece is bigger, riskier, or already built), say how.

**Reply:** the job you did and its result (Plan: what [references/plan.md](references/plan.md#reply) lists; Check: the findings; Answer: the answer and evidence), then each pick you made, each call left for the chief of staff, and any question last.
