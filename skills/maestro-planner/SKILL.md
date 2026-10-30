---
name: maestro-planner
description: "Turns a goal into a short spec and ordered, testable Maestro tickets, or splits or rewrites a ticket that is too big or unclear. Use for /maestro-planner, 'plan this feature', 'break this into tickets', or 'split T-12'."
argument-hint: "[goal | ticket id to split or rewrite]"
disable-model-invocation: true
---

# Plan

You turn one goal into tickets an engineer can finish without asking, and create them in order. You read code and write tickets; you never change code.

Follow the **maestro-working-principles** skill.

## Start

Open a todo list with one item per phase below.

## Phase A: Frame

1. Take the goal from the user's words. Given a ticket id, read it with `maestro-ticket get <id> --json` and take its goal: you split it, or rewrite it as a split into one. Only a `backlog`, `todo` or `blocked` ticket can be split.
2. Write a spec of a page or less: the problem and who has it, the outcome that means done (something a command or the running app shows), scope and non-goals, and risks. If you can't state the outcome, ask for it and stop.
3. Find the files each part touches and the closest code that already does the same kind of thing, by path. Check each path you name exists.
4. Run `maestro-ticket list --json`. A ticket that already covers a piece becomes a dep, not a new ticket.

## Phase B: Slice

1. Cut one ticket per reviewable change (a few hundred lines) that ships something and can be tested alone. Slice by behaviour, never by layer. Put the riskiest unknown first.
2. Draft each ticket in the shape the **maestro-create-ticket** skill gives. Deps name earlier tickets in this plan or ids on the board.
3. Check each criterion against that skill's rule and rewrite any that fails.
4. If the goal leaves a field or a choice you can't infer, ask once, in one message, with your pick for each so the user can say "yes". Otherwise go on.

## Phase C: Create

1. Read the installed **maestro-create-ticket** `SKILL.md` (in `.claude/skills/maestro-create-ticket/`, or `.agents/skills/` in Codex) and follow it for each ticket. Phase B was its one ask: don't ask again.
2. Go earliest first, so each later ticket's deps name the ids the earlier ones got.
3. On a refusal, fix only the field it names. If it still refuses, stop and show it.

## Phase D: Close a split

Skip this phase unless you split a ticket.

1. If the split ticket has a live claim, leave it and say who holds the claim.
2. Find each dependent: each ticket in `maestro-ticket list --json` whose `depends_on` holds `<id>`. A ticket whose deps still name a cancelled ticket can never be claimed, so move each onto the pieces first, then cancel:

   ```sh
   maestro-ticket update <dependent> --add-deps T-a,T-b --remove-deps <id>
   maestro-ticket comment <id> --body 'Split into T-a, T-b'
   maestro-ticket transition <id> --to cancelled
   ```

**Reply:** the spec in five lines or fewer, one line per ticket created (`<id> <title> [status, priority, deps, areas]`), the split ticket's new status and each dependent moved, then `Next: maestro-play run work --ticket <first id>`.
