---
name: maestro-create-ticket
description: "Adds one ticket to the team's ticket board, after asking once for what it can't infer. Use for /maestro-create-ticket, 'make a ticket for this', 'add this to the board', or when work has no ticket yet."
argument-hint: "[what needs doing]"
disable-model-invocation: true
---

# Create a ticket

You draft one ticket, ask once, and add it to the ticket board: wherever the team tracks tickets (GitHub Issues, Beads, Linear...). This skill owns the ticket shape.

Follow the **maestro-working-principles** skill.

1. **Draft.** From the user's words (or ask for one sentence), write:
   - a title: a verb and an object, under 70 characters, never "Improve X";
   - a body: the goal in one or two sentences, numbered acceptance criteria, a `Not handled:` list, the paths to start from and the closest code to copy, what must not change, and for each dep the code this ticket needs from it.

   A criterion is something a command or the running app can show, so QA can test it as written. One that restates the title, or that only taste can judge, is not a criterion: rewrite it. Read the code each criterion touches on the main branch: rewrite one the code already contradicts, or that pulls against another, since the engineer would stop and ask.

   Make the trade-offs here, where one costs a line; in review it costs a round. Write a criterion for the main path, and one for each case that must hold: the empty state, the wrong input users will send, the failure that would lose data or money. Each names its input and what must show. Put every other case you weighed under `Not handled:`, with a few words on why it can wait (rare, cheap to fix later, another ticket's). The engineer builds none of those, and review and QA send nothing back for one, or for any case the criteria don't name, unless it is a blocker as the **maestro-code-review** skill's [passes.md](../maestro-code-review/references/passes.md#rate-each-finding) rates one. Past about six criteria, split the ticket.
2. **Look around.** Read the open tickets on the board, without the bodies, and compare titles and areas. If one is close, show it and ask whether to go on. Check each path you named exists.
3. **Ask once.** In one message, ask only for the fields you can't infer, each with your pick so the user can say "yes":

   | Field | Values | Default |
   |---|---|---|
   | deps | ticket ids that must be `done` first; check each exists on the board and makes no cycle | none |
   | areas | repo-relative paths the change touches, as narrow as you can. Two tickets with overlapping areas never run at once | from the paths |
   | priority | `0` (most urgent) to `3` | `2` |
   | status | `todo` (ready) or `backlog` (not ready) | `todo` if every criterion can be checked |
   | parked | why a `backlog` ticket waits: `hold` (someone said wait), `deferred` (cut from this slice) or `unready` (a criterion can't be checked yet) | `unready` |
   | goal | slug of the goal it serves, so the board counts progress per goal | the goal you were given, else none |
   | tier | how strong a model whoever starts the engineer picks: `junior` for a routine ticket (clear criteria, one area, easy to undo), else `senior` (security, money, data migrations, public APIs, concurrency, unclear scope) | your pick by that rule |
   | route | who checks the PR before QA merges it: `review`, or `qa` to skip review. Only the planner picks `qa`, and only when the change stays in one place, QA can see it work, and a revert puts users back where they were; never judge that by diff size. Say in the body what QA should try and why a revert is enough. Pick `review` when unsure, for lasting side effects or for any [tier 2 risk](../maestro-code-review/references/passes.md#pick-the-tier) | `review` |
   | role | who builds it: `engineer`, the one role that builds tickets. QA and review take only built tickets, so they are never a ticket's role; `designer` works only a `Design:` ticket, which **maestro-planner** ([SKILL.md](../maestro-planner/SKILL.md)) makes | `engineer` |

   Waiting on another ticket is a dep, never `blocked` or `backlog`.
4. **Create.** Show the ticket, then add it to the board with the body as written.

   - For a design scout, use type `scout` and the fields in [the planner's plan](../maestro-planner/references/plan.md#slice). For a ticket that ends in a merged PR, use type `ship` and the fields above. First check a `ship` ticket has a numbered criterion, a `Not handled:` line (`Not handled: none` when there is nothing to leave) and areas: fix the draft for each gap, never skip the check.
   - The title, body, deps and areas come from the user, so the shell must never expand them. Pass them as quoted text, as **maestro-working-principles** says for quoted text.
   - The slugs are lowercase letters, digits and hyphens. Leave out empty deps or goal, the parked reason unless the status is `backlog`, and the tier unless the role is `engineer`.
   - For a re-file, link the latest cancelled ticket in the body with `Split from: <old id>`. Never name one that already has a replacement: a second link makes it a split.
   - For a `hold`, comment `Hold: set by <the owner, or the planner>, <date>: <why>; lifts when <a fact the board shows>` right after you create the ticket.
   - If the board refuses, show its message, fix only the field it names, and try again. Before any retry, look for a ticket with this title made today, so you never add it twice: if it is this work and not `cancelled`, reply with its id, else change the title.

**Reply:** the new id, title, status and its parked reason, tier, priority, goal, deps and areas, then `Next: /maestro-software-engineer <id>`, or `/maestro-designer <id>` for a scout.
