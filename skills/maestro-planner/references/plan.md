# Plan

Turn one goal into tickets an engineer can finish without asking, and create them in order. Many planner sessions and hundreds of engineers share one board, so plan wide and lock what you split. Open a todo list with one item per phase.

## Phase A: Frame

1. Take the goal from the request, and its slug if the request names one (`goal <slug>: ...`); else make a short one. Given a ticket id, run `maestro-ticket get <id> --json` and take the goal from its title and body, and the slug from its `goal`. The store can't edit a title or body, so a rewrite is a split into one ticket. If its status isn't `backlog`, `todo`, `blocked`, or `in_progress` with a `Kickback:` as its latest comment, or only `Retest:` and `Answer:` comments after it (sent back to split), stop and say why.
2. Given an id, claim it before you plan, so no engineer or other planner takes it: pick a holder once, `<your bot id>/<id>-<6 random letters>`, run `maestro-ticket claim <id> --holder <holder> --ttl 1h --json` with a keepalive on your harness's process, both as **maestro-working-principles** says of every claim, and keep `claim.token`. On `waiting_on_deps` or `area_busy` in `todo`, take it off the queue with `maestro-ticket transition <id> --from todo --to backlog --parked planning --expect-version <version>`, so the planner moves it back if this session stops, and claim again. On either outside `todo`, stop and say so; a later look tries again. On `claimed` or a version conflict, stop and name the holder. A draft keeps the claim for the bot's answer. On resume, if it `expired`, read the ticket again: if its status left step 1's list, or a `Handoff:` came after your draft, someone built it meanwhile, so stop and say so; else claim again. If you give up before you post `Split into`, cancel each ticket this session created, latest first, and name in the reply any the store refuses with `has_dependents`; then run `maestro-ticket release <id> --holder <holder> --token <token>`, and move it back to `todo` if you moved it.
3. Write a spec of a page or less: the problem and who has it, the outcome that means done (something a command or the running app shows), scope and non-goals, and risks. If you can't state the outcome, ask for it and stop.
4. Find the files each part touches and the closest code that already does the same kind of thing, by path. Check each path you name exists.
5. Read the open board without the bodies: `maestro-ticket list --status backlog,todo,in_progress,in_review,blocked --json | jq -c '.tickets[] | {id,title,status,areas,depends_on}'`. A ticket that already covers a piece becomes a dep, not a new ticket.

## Phase B: Slice

1. Cut one ticket per reviewable change (a few hundred lines) that ships something and can be tested alone. Slice by behaviour, never by layer. Put the riskiest unknown first. Tickets past the first slice go in `backlog`, parked `deferred`.
2. Before the rest, give what many tickets share its own small ticket at the front: a format, schema, type or test fixture, or room in a file many would touch (split it, or add the hook the rest plug into). The tickets that use it then start side by side once it merges. In code that handles secrets, files or outside services, that includes the guards every entry point needs, such as safe paths, errors that print no secret and one exit-code contract: each one shared helper, with a test that runs it over every entry point and a criterion that names it under `## Conventions` in `.maestro/project/project-setup.md`. Skip any `## Conventions` already names. Without one, each ticket writes its own guard and review sends back the gaps one ticket at a time.
3. Plan wide, not long: a chain of deps runs one ticket at a time, siblings run at once. Add a dep only where one ticket needs another's code to build or to pass its tests, and name that code in the body. A dep only for order is none: set the order with priority. A dep only for a test fixture means the fixture needs its own ticket, as step 2 says. Give tickets with no dep between them areas that don't overlap, here and on the board.
4. If the goal calls an outside API (an ads, payments, mail or other third-party service), end the plan with one ticket that deps on every other ticket of the goal: title `Live run: <slug>`, `--type scout --role qa-lead --owner qa-lead`, its body the user's path start to end, each API it calls with that API's sandbox or paused mode, and the keys it needs by environment variable name. QA runs it from a fresh clone against the real API, as the **maestro-qa-lead** skill's live run says, so the goal reads done only once the real path works. For `fix <id>` of such a ticket, make one ticket per finding in its latest `Live run: fail` comment, and add each as a dep of that ticket with `maestro-ticket update <id> --add-deps`.
5. Draft each ticket in the shape [maestro-create-ticket](../../maestro-create-ticket/SKILL.md) gives, and check each criterion against its rule. Deps name earlier tickets in this plan or ids on the board.
6. Take that skill's default for each field you can't infer, and name each pick in the reply.
7. Stop here, creating nothing, if the request says "draft" or a call is left for the chief of staff. The reply gives the drafts in full, so the bot can resume with "create them" or changes.

## Phase C: Create

1. Follow that skill's create step for each ticket with `--goal <slug>`, earliest first, so each later ticket's deps name the ids the earlier ones got. Phases A and B did its look-around and its ask: skip both. When you split or rewrite a ticket, add `--split-from <id>` to each create, so the new tickets keep its tries, and key each create `<holder>:<title slug>`, with this session's holder from Phase A step 2, in place of that skill's key: a rewrite often keeps the old title, so the old ticket's own key, or an earlier split's, would refuse or replay it as a ticket that isn't this one. `--split-from` carries no deps: give each new ticket the old ticket's deps that aren't `done`, unless the spec says why that part doesn't need one.
2. When you split or rewrite a ticket, post `Split into <new ids>` on it under your claim right after the last create, so a planner can finish the split if this session stops.
3. On a refusal, fix only the field it names. If it still refuses, stop and show it. A conflict on the idempotency key means another session made a ticket with that title today: read it, then dep on it or retitle yours. On a split's key, it means this session made that ticket already: read it, and use it only if it isn't `cancelled`; else retitle yours.

## Phase D: Close the old ticket

Skip this phase unless you split or rewrote a ticket.

1. Find each dependent: each ticket in `maestro-ticket list --status backlog,todo,in_progress,in_review,blocked --json | jq -c '.tickets[] | {id,depends_on}'` whose `depends_on` holds `<id>`. The store won't cancel a ticket an open ticket depends on, since that one could never start, so move each onto the new tickets first, then cancel with your claim:

   ```sh
   maestro-ticket update <dependent> --add-deps T-a,T-b --remove-deps <id>
   maestro-ticket transition <id> --to cancelled --holder <holder> --token <token>
   ```

2. If the cancel fails with `has_dependents`, another session added a dependent in the meantime: move each one it names the same way, then cancel again. If moving a dependent fails with `claimed`, don't wait for its holder: post `Escalation: split into T-a, T-b; cancel this once <dependent> (held by <holder>) is off it` with a last line `Return-role: <the ticket's role>`, move the ticket to `blocked` if it isn't there, and release your claim, so no engineer builds it. The planner cancels it on a later look.
3. If a write under your claim fails with `lease_lost`, claim again as the same holder and go on; if that claim is refused for any reason, stop and say so: the planner finishes the split.
4. If the old ticket has an open PR, found from its id as Phase A step 2 of the **maestro-code-review** skill ([SKILL.md](../../maestro-code-review/SKILL.md)) does, never from a link in a note, close it with a comment naming the new tickets.

## Reply

The spec in five lines or fewer, then one line per ticket (`<id> <title> [status, priority, deps with the code each is for, areas]`, or the full draft if you created nothing), the old ticket's new status and each dependent moved, how many new tickets can start now and the longest dep chain, then `Start now: <ids> | none`, the one most tickets wait on first, for the planner to hand to its engineers.
