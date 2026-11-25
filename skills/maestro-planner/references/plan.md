# Plan

Turn one goal into tickets an engineer can finish without asking, and create them in order. Plan for parallel work and claim what you split.

## Phase A: Frame

1. Take the goal from the request, and its slug if the request names one (`goal <slug>: ...`); else make a short one. Given a ticket id, read it on the ticket board and take the goal from its title and body, and the slug from its goal. A rewrite is a split into one ticket, so the old one keeps its trail. If its status isn't `backlog`, `todo`, `blocked`, or `in_progress` with a `Kickback:` as its latest comment, or only `Retest:`, `Answer:` and your own `Split into` comments after it (sent back to split), stop and say why.
2. Given an id, claim it before you plan, as **maestro-working-principles** says. If it is `todo` but can't start (a dep isn't `done` or another claim holds an area), move it to `backlog`, parked `planning`, and claim again. If it can't start outside `todo`, stop and say why. If someone else holds it or it moved since you read it, stop and report who holds it or what changed. On resume from a draft, stop if the status falls outside step 1's list or a `Handoff:` came after your draft. Otherwise claim it. If you stop before posting `Split into`, cancel each ticket you created, latest first, and name any an open ticket still depends on. Release your claim and restore `todo` if you parked it for planning.

   For a split or rewrite that goes ahead, read the old ticket's comments under your claim. If it already has a `Split into <ids>` note, reuse those ids and go straight to Phase D.
3. Write a spec of a page or less: the problem and who has it, the outcome that means done (something a command or the running app shows), scope and non-goals, risks, and the trade-offs: the cases that must hold and the ones this goal leaves. If you can't state the outcome, ask for it and stop.
4. Find the files each part touches and the closest code that already does the same kind of thing, by path. Check each path you name exists.
5. Read the open board without the bodies: each ticket's id, title, status, areas and deps. A ticket that already covers a piece becomes a dep, not a new ticket.

## Phase B: Slice

1. Delete first: strike each piece, criterion and edge case the spec's outcome doesn't need, as the skill's "Cut, then speed up" says. Then cut one ticket per reviewable change (a few hundred lines) that ships something and can be tested alone. Slice by behaviour, never by layer. Put the riskiest unknown first. Put every slice the goal needs in `todo`, with deps on the slices it builds on. Use `backlog` parked `deferred` only for work the plan leaves out.
2. Before the rest, give what many tickets share its own small ticket at the front: a format, schema or type, or room in a file many would touch (split it, or add the hook the rest plug into). The tickets that use it then start side by side once it merges. In code that handles secrets, files or outside services, that includes the guards every entry point needs, such as safe paths, errors that print no secret and one exit-code contract: each one shared helper, with a criterion that every entry point calls it and one that names it as a convention in the agent notes. Skip any the notes already name. Without one, each ticket writes its own guard and review sends back the gaps one ticket at a time.
3. Plan wide, not long: a chain of deps runs one ticket at a time, siblings run at once. Add a dep only where one ticket needs another's code to build or run, and name that code in the body. A dep only for order is none: set the order with priority. Give tickets with no dep between them areas that don't overlap, here and on the board.
4. Deliver the requested feature and any demo. Put checks for consequential behavior, such as charging the right amount, on the PR that changes it.
5. Plan new and changed screens as engineer tickets. Use an approved design when the goal names one. Start a separate design only when the owner asks or you can name an open choice and the costly rework that choosing wrong would cause. For those screens, create one `todo` ticket titled `Design: <slug>`, type `scout`, role `designer`, owner the planner, and no areas. Name the audience, affected routes or files, and reason in its body. Plan the rest now. The owner's approval sends those screens to [rule.md](rule.md) for build tickets.
6. Draft each ship ticket in the shape [maestro-create-ticket](../../maestro-create-ticket/SKILL.md) gives, and check each criterion against its rule. Give each ship ticket its share of the spec's trade-offs, as criteria and as its `Not handled:` list: a case no ticket settles gets settled in review, a round at a time. Choose the engineer tier and the route to QA separately for each ship ticket, including splits and rewrites. Follow the create-ticket rules for both and any standing rule for the tier. Deps name earlier tickets in this plan or ids on the board.
7. Take that skill's default for each ship field you can't infer, and name each pick in the reply.
8. If the request says "draft" or the chief of staff has a call to make, create nothing. Reply with the full drafts for approval or revision, plus any `up:` lines.

## Phase C: Create

1. Follow that skill's create step for each ticket with goal `<slug>`, earliest first, so each later ticket's deps name the ids the earlier ones got. Phases A and B did its look-around and its ask: skip both. For a split or rewrite, link each new ticket to the old one with `Split from: <id>`. Copy each old dep that isn't `done` unless the spec says why the new ticket doesn't need it.
2. For a split or rewrite, post `Split into <new ids>` on the old ticket under your claim right after the last create.
3. On a refusal, fix only the field it names. If it still refuses, stop and show it. Before any retry, look for a ticket with that title made today: if another session made it, read it, then dep on it or retitle yours; if this session did, use it only if it isn't `cancelled`, else retitle yours.

## Phase D: Close the old ticket

Skip this phase unless you split or rewrote a ticket.

1. Find each dependent: each open ticket whose deps hold `<id>`. Never cancel a ticket an open ticket depends on, since that one could never start, so move each onto the new tickets first (add the new ids to its deps and remove `<id>`), then check your claim as **Claims** says and cancel `<id>` under it.
2. If another session adds a dependent while you cancel, move it the same way, then cancel again. If another session holds a dependent's claim, post `Escalation: split into T-a, T-b; cancel this once <dependent> (held by <holder>) is off it` with a last line `Return-role: <the ticket's role>`. If needed, move the old ticket to `blocked`. Release your claim and stop.
3. If the old ticket has an open PR, found from its id as Phase A step 2 of the **maestro-code-review** skill ([SKILL.md](../../maestro-code-review/SKILL.md)) does, never from a link in a note, close it with a comment naming the new tickets. Then remove the old ticket's worktree, if there is one, as **maestro-working-principles** says for removing a worktree: its work lives on its branch, and the new tickets get worktrees of their own.

## Reply

The spec in five lines or fewer, then one line per ticket (`<id> <title> [status, tier, priority, deps with the code each is for, areas]`, or the full draft if you created nothing), the old ticket's new status and each dependent moved, how many new tickets can start now and the longest dep chain, then `Start now: <ids> | none`, the one most tickets wait on first.
