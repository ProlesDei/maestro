# Plan

Turn one goal into tickets an engineer can finish without asking, and create them in order. Open a todo list with one item per phase.

## Phase A: Frame

1. Take the goal from the request. Given a ticket id, run `maestro-ticket get <id> --json` and take the goal from its title and body. The store can't edit a title or body, so a rewrite is a split into one ticket. Stop and say why if its status isn't `backlog`, `todo` or `blocked`, or if its `claim` is live (`expired` is false): name the holder.
2. Write a spec of a page or less: the problem and who has it, the outcome that means done (something a command or the running app shows), scope and non-goals, and risks. If you can't state the outcome, ask for it and stop.
3. Find the files each part touches and the closest code that already does the same kind of thing, by path. Check each path you name exists.
4. Run `maestro-ticket list --json`. A ticket that already covers a piece becomes a dep, not a new ticket.

## Phase B: Slice

1. Cut one ticket per reviewable change (a few hundred lines) that ships something and can be tested alone. Slice by behaviour, never by layer. Put the riskiest unknown first.
2. Add a dep only where one ticket needs another's code. Give tickets with no dep between them areas that don't overlap, so they can run at once.
3. Draft each ticket in the shape [maestro-create-ticket](../../maestro-create-ticket/SKILL.md) gives, and check each criterion against its rule. Deps name earlier tickets in this plan or ids on the board.
4. Take that skill's default for each field you can't infer, and name each pick in the reply.
5. Stop here, creating nothing, if the request says "draft" or a call is left for the chief of staff. The reply gives the drafts in full, so the bot can resume with "create them" or changes.

## Phase C: Create

1. Follow that skill's create step for each ticket, earliest first, so each later ticket's deps name the ids the earlier ones got. Phases A and B did its look-around and its ask: skip both.
2. On a refusal, fix only the field it names. If it still refuses, stop and show it.

## Phase D: Close the old ticket

Skip this phase unless you split or rewrote a ticket.

1. Find each dependent: each ticket in `maestro-ticket list --json` whose `depends_on` holds `<id>`. A ticket whose deps still name a cancelled ticket can never be claimed, so move each onto the new tickets first, then cancel:

   ```sh
   maestro-ticket update <dependent> --add-deps T-a,T-b --remove-deps <id>
   maestro-ticket comment <id> --body 'Split into T-a, T-b'
   maestro-ticket transition <id> --to cancelled
   ```

## Reply

The spec in five lines or fewer, then one line per ticket (`<id> <title> [status, priority, deps, areas]`, or the full draft if you created nothing), the old ticket's new status and each dependent moved, then `Next: maestro-play run work --ticket <id>` for each new `todo` ticket with no open deps.
