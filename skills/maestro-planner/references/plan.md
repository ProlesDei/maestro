# Plan

Turn one goal into tickets an engineer can finish without asking, and create them in order. Many planner sessions and hundreds of engineers share one board, so plan wide and lock what you split. Open a todo list with one item per phase.

## Phase A: Frame

1. Take the goal from the request. Given a ticket id, run `maestro-ticket get <id> --json` and take the goal from its title and body. The store can't edit a title or body, so a rewrite is a split into one ticket. If its status isn't `backlog`, `todo` or `blocked`, stop and say why.
2. Given an id, claim it before you plan, so no engineer or other planner takes it: pick a holder once, `planner/<id>-<6 random letters>`, run `maestro-ticket claim <id> --holder <holder> --ttl 1h --json`, and keep `claim.token`. On `waiting_on_deps`, take it off the queue with `maestro-ticket transition <id> --from todo --to backlog --expect-version <version>`, then claim again. On `claimed` or a version conflict, stop and name the holder. A draft keeps the claim for the bot's answer; on resume, claim again if it `expired`. If you give up, run `maestro-ticket release <id> --holder <holder> --token <token>`, and move it back to `todo` if you moved it.
3. Write a spec of a page or less: the problem and who has it, the outcome that means done (something a command or the running app shows), scope and non-goals, and risks. If you can't state the outcome, ask for it and stop.
4. Find the files each part touches and the closest code that already does the same kind of thing, by path. Check each path you name exists.
5. Read the open board without the bodies: `maestro-ticket list --status backlog,todo,in_progress,in_review,blocked --json | jq -c '.tickets[] | {id,title,status,areas,depends_on}'`. A ticket that already covers a piece becomes a dep, not a new ticket.

## Phase B: Slice

1. Cut one ticket per reviewable change (a few hundred lines) that ships something and can be tested alone. Slice by behaviour, never by layer. Put the riskiest unknown first.
2. Plan wide, not long: a chain of deps runs one ticket at a time, siblings run at once. Add a dep only where one ticket needs another's code. Give tickets with no dep between them areas that don't overlap, here and on the board.
3. When many tickets would touch one file, put first a small ticket that makes room (splits the file, adds the hook the rest plug into), so the rest can run apart.
4. Draft each ticket in the shape [maestro-create-ticket](../../maestro-create-ticket/SKILL.md) gives, and check each criterion against its rule. Deps name earlier tickets in this plan or ids on the board.
5. Take that skill's default for each field you can't infer, and name each pick in the reply.
6. Stop here, creating nothing, if the request says "draft" or a call is left for the chief of staff. The reply gives the drafts in full, so the bot can resume with "create them" or changes.

## Phase C: Create

1. Follow that skill's create step for each ticket, earliest first, so each later ticket's deps name the ids the earlier ones got. Phases A and B did its look-around and its ask: skip both.
2. On a refusal, fix only the field it names. If it still refuses, stop and show it. A conflict on the idempotency key means another session made a ticket with that title today: read it, then dep on it or retitle yours.

## Phase D: Close the old ticket

Skip this phase unless you split or rewrote a ticket.

1. Find each dependent: each ticket in `maestro-ticket list --json` whose `depends_on` holds `<id>`. A ticket whose deps still name a cancelled ticket can never be claimed, so move each onto the new tickets first, then cancel with your claim:

   ```sh
   maestro-ticket update <dependent> --add-deps T-a,T-b --remove-deps <id>
   maestro-ticket comment <id> --holder <holder> --token <token> --body 'Split into T-a, T-b'
   maestro-ticket transition <id> --to cancelled --holder <holder> --token <token>
   ```

2. List again and move any dependent another session added in the meantime.

## Reply

The spec in five lines or fewer, then one line per ticket (`<id> <title> [status, priority, deps, areas]`, or the full draft if you created nothing), the old ticket's new status and each dependent moved, how many new tickets can start now and the longest dep chain, then `Start now: <ids>`, the one most tickets wait on first, for the planner to hand to its engineers.
