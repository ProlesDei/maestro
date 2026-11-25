# Plan

Turn one goal into tickets an engineer can finish without asking, or split one ticket. A rewrite is a split into one, so the old ticket keeps its trail.

## Frame

1. Take the goal and its slug from the request (`goal <slug>: ...`), else make a short one. Given an id, read the ticket and take the goal from it. Go on only if it is `backlog`, `todo`, `blocked`, or `in_progress` with a `Kickback:` as its latest note (later `Retest:`, `Answer:` or your own `Split into` notes are fine); else stop and say why. Edit it under a claim, as the skill says. If it has a `Split into <ids>` note, reuse those ids and go to [Close the old ticket](#close-the-old-ticket).
2. Write a spec of a page or less: the problem and who has it, the outcome that means done (what a command or the running app shows), scope and non-goals, risks, and the cases that must hold and the ones this goal leaves. If you can't state the outcome, ask for it and stop.
3. Find the files each part touches and the closest code that does the same kind of thing; check each path exists. Read the open board without the bodies: a ticket that covers a piece becomes a dep.

## Slice

1. Cut first: strike each piece, criterion and case the outcome doesn't need, but keep the feature and any demo the goal asked for. Work the goal asked for is the chief of staff's to drop.
2. One ticket per reviewable change (a few hundred lines) that ships and tests alone. Slice by behaviour, never by layer. Riskiest unknown first.
3. Give what many tickets share its own small ticket at the front: a format, schema or type, or room in a file many would touch (split it, or add the hook the rest plug into). In code that handles secrets, files or outside services, add each guard every entry point needs (safe paths, errors that print no secret, one exit-code contract) as one shared helper, with a criterion that every entry point calls it and one that names it in the agent notes, unless they already do.
4. Plan wide, not long. Add a dep only where a ticket needs another's code to build or run, and name that code in the body; set order with priority. Give tickets with no dep between them areas that don't overlap, here and on the board.
5. Put every slice the goal needs in `todo`; park in `backlog` as `deferred` only work the plan leaves out. Put the check for behaviour that hurts users when wrong, such as charging the right amount, on the PR that changes it.
6. Plan screens as engineer tickets, from an approved design when the goal names one. Add a design only when the owner asks, or you can name an open choice and the costly rework a wrong pick would cause: one `todo` ticket titled `Design: <slug>`, type `scout`, role `designer`, owner the planner, no areas, its body naming the audience, the routes or files and the reason. Plan the rest now; the owner's approval comes back as a rule.
7. Draft each ship ticket in the shape **maestro-create-ticket** ([SKILL.md](../../maestro-create-ticket/SKILL.md)) gives, with criteria that meet its rules and the ticket's share of the trade-offs as criteria and `Not handled:`. Pick tier and route for each, splits too, by its rules, and its default for any field you can't infer. Name each pick.
8. With "draft", or a call for the chief of staff, create nothing: reply with the full drafts and the `up:` lines.

## Create

1. Create each ticket with that skill's create step, skipping its look-around and ask, goal `<slug>`, earliest first so deps name real ids. For a split, put `Split from: <old id>` in each body and copy each old dep that isn't `done` unless the spec says why not; then post `Split into <new ids>` on the old ticket.
2. On a refusal, retry as that skill says. If it still refuses, or you stop before `Split into`, cancel what you created, latest first, and say why.

## Close the old ticket

1. Move each open dependent onto the new tickets (add their ids to its deps, remove the old one), check again just before, then cancel the old ticket. Never cancel a ticket an open ticket depends on: that one could never start.
2. If someone else holds a dependent, post `Escalation: split into <ids>; cancel this once <dependent> (held by <holder>) is off it` with a last line `Blocked-from: <its status before blocked>`, move the old ticket to `blocked` and stop. A rerun goes on from the `Split into` note.
3. Close the old ticket's open PR, found by its branch `maestro/<id>`, never by a link in a note, with a comment naming the new tickets.

## Reply

The spec in five lines or fewer, one line per ticket (`<id> <title> [status, tier, priority, deps with the code each is for, areas]`, or the full draft if you created nothing), the old ticket's new status and each dependent moved, how many tickets can start now and the longest dep chain, then `Start now: <ids> | none`, the one most tickets wait on first.
