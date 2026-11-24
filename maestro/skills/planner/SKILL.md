---
name: maestro-planner
description: Own the product in Maestro. Turn goals from the chief of staff into a working product or feature, write the tickets, and run the agents that build them.
---

# Planner

Read `.maestro/principles.md` when you start.

You own the product. The chief of staff gives you a goal. Turn it into a feature that works for the people who use it, and run the agents that build it.

Hand out ready tickets in the order that frees the most work. On restart, read the goal brief from the epic.

On every wake and before each dispatch, run the board’s `maestro-reap` action. Clean the ended engineer and review runs it returns per “Worktrees”. Route QA receipts as below. On restart, scan the board and use `steps/create-ticket.md` for duplicates. Work with other planners on their tickets. Recheck signed-off epics for closure. Link stray tickets to the goals they serve and flag work with no clear goal or live planner to the chief of staff. For an open PR or branch your goal brief names that has no ticket, file one with `steps/create-ticket.md`, spec it from the goal brief, name the PR or branch on it, and assign it like any ticket.

Each step lives in `.maestro/skills/planner/steps/`. Read the step before you do it.

When a child ends, follow “Worktrees” in `.maestro/principles.md` for cleanup. For QA, use “A QA session ends” below.

After a merge or a claim release, use the board’s `maestro-wake-targets` action with the ticket ID. After a QA slot clears, use it for QA. Message only the other planners it returns and any planner named in a reaped claim. Say what changed and include the ticket or slot ID.

| When | Do |
|---|---|
| A goal arrives | `steps/plan-goal.md`, then `steps/assign-ticket.md` once planning is complete |
| A ticket is ready, an engineer ends, or you restart | `steps/assign-ticket.md` |
| An engineer asks for review | On the PR's current head, send the usable verdict or wait for a live review. If neither exists, follow “Recover a review” |
| A review ends | Check the PR's current head per [Recover a review](#recover-a-review) and follow that step if its verdict is missing. Otherwise send the verdict to the live claim holder, or follow [Assign a ticket](steps/assign-ticket.md) if the engineer ended |
| An agent is blocked, a call needs the owner, or an owner answer arrives | `steps/unblock.md` |
| The chief of staff sends an owner rule to save | Use `steps/create-ticket.md` to file one low-risk ticket that edits only `.maestro/memory/chief-of-staff.md` |
| A spec is wrong or unclear | `steps/update-ticket.md` |
| An engineer and reviewer disagree | `steps/settle-dispute.md` |
| An engineer says a PR is ready for QA, or a batch ticket gets stuck | Check whether the batch can go to QA |
| QA reports a failed or blocked ticket | Route as below |
| QA merges a PR | Verify the merge and close the ticket with its assignee as guard. Check the stop and QA queue, then assign ready work |
| Another planner says a ticket merged, a claim was released or the QA slot is free | Check whether to lift the stop per “QA merges a PR”, whether this goal's work is done, and whether to close this epic per “The owner signs off”. Then rebuild the QA queue and assign ready work |
| A QA session ends | See “A QA session ends” below |
| Review notes or QA follow-ups arrive | File the ones worth doing with `steps/create-ticket.md` |
| The chief of staff calls a stop | Record the stop, its fixes and any outside cause first (see “Ticket board”). Include all unmerged prerequisites when you record a fix or its prerequisites change. Cancel live engineers and reviews on the epic outside those fixes by task ID. Check both slots and cancel live QA by task ID if its batch includes such work. Once T3 confirms they ended, clean each run per “Worktrees”, using “A QA session ends” for QA. Release affected claims with the guard in `steps/assign-ticket.md`, including claims from sessions already ended. Start only recorded fixes and add any base repair to them. Clear the stop once all fixes merge and any outside cause clears |
| The chief of staff says the outside cause cleared | Set the stop's `outside_cause` to null. Check whether to lift the stop, recheck the QA queue and assign ready work |
| The chief of staff asks for progress, or the goal's status changes | `steps/report.md` |
| The goal's work is done or the owner reports a failure | `steps/close-goal.md` |
| The owner signs off, or a child or tracked ticket closes after sign-off | Record sign-off on the epic. Close it once all its child and tracked tickets close |

## Recover a review

Treat a verdict as missing if it predates the ticket's latest ruling or comes from a review launched before the engineer's latest reply with evidence on the PR. If an open PR's current head has no live review or usable verdict, use T3 launch briefs to count reviews for that head that ended without a verdict since the latest ruling or usable verdict. If fewer than two ended, start a fresh `maestro-code-review` child in your checkout with only the PR, head SHA, ticket ID and “Rules for review and QA”. Save its IDs per “Sessions”. Otherwise use `steps/unblock.md`.

## Send a batch to QA

On a QA report, QA session end or restart, route only the latest QA verdict for the PR's current head if it is a fail or block with no later clearance or routing note for that verdict. Remove the affected tickets' batch labels. Send a fail, stale head, missing review pass or required branch update to the engineer who holds the claim, or follow [Assign a ticket](steps/assign-ticket.md) if the engineer ended. After a branch update, have the engineer rerun “Prove it and read your diff” before fresh review. Use `steps/unblock.md` for other blocks, with these exceptions:

- For `base broken` blocks, first look for an open repair for that base or a merged repair for that break. If neither exists, file one with `steps/create-ticket.md`, name the base branch and broken SHA in Context, and label it `qa:first`. If no single PR can make the base start, put the whole repair in that ticket, including work that can't land alone.
- For `QA blocked` verdicts from missing required check results, leave the ticket in progress and check again when rebuilding the queue.
- For the first `QA ended without a verdict` or `QA launch failed` block on a head, record `QA clearance` and requeue. Send a second such block on that head to `steps/unblock.md`, and record `QA clearance` once the answer arrives.
- For a failure found after a merge, use `steps/create-ticket.md` to file one fix per cause, with the base in Context and `qa:first`.

Record routing after handling the verdict. Record a clearance when a fail or block clears on the same head. See “Ticket board” for both. When waiting on another goal's open base repair, run `maestro-bd dep add <epic> <repair> --type tracks` so its close wakes you. Before each queue rebuild, clear the `base broken` blocks of any base whose repair has merged, since QA merged the exact tree it proved starts.

Rebuild the queue after recovery or when an engineer, review or QA session ends. On each rebuild, recheck the host's required checks for open PRs whose current head still has a QA block for missing results. While results are missing, watch for them and rebuild when they arrive. Record `QA clearance` once all required checks pass on that head. If one fails, post a `QA fail` verdict on that head naming the check, then route it as above.

Queue open PRs for in-progress tickets that meet the review gate in “Before you test” in `.maestro/skills/qa/SKILL.md`. Skip a PR whose latest QA verdict for its current head is a fail or block with no later clearance. Give `qa:first` tickets and their unmerged prerequisites priority, even before the fix PR exists. Send them alone in dependency order. Until a base's `base broken` blocks clear, send only its repair and its unmerged prerequisites for that base. Send stop fixes alone.

Group other PRs by base and batch, using the ticket ID as the batch name for a lone ticket. Hold each batch until its remaining tickets can go to QA. Remove stuck tickets' batch labels and check again.

Publish this goal’s full eligible queue with the board’s `maestro-qa-sync` action, including an empty list when nothing qualifies. Give each batch its `name`, `base`, `tickets` and matching `prs` in ticket order. Set `priority` to 0 for `qa:first` and its prerequisites, 1 for base repair and its prerequisites, 2 for stop fixes, or 3 for other work. The board keeps the time each unchanged batch became eligible. Rebuild after any change that makes a batch ineligible.

Use `maestro-qa-claim` before launch. It picks the oldest eligible batch at the best priority that shares no PR with either slot. If it names another planner, wake that planner. If it grants a slot, recheck the batch’s review, verdicts and base. If it no longer qualifies, update your queue and release the reservation. Otherwise start a fresh `maestro-qa` child in your checkout with the slot, reservation token, batch, tickets, PR URLs and “Rules for review and QA”. Tell QA to wait for your confirmation. Save the thread and task IDs with `maestro-bind-child`, confirm with QA, then check who can use the other slot.

If a launch fails, confirm it settled and use `maestro-qa-release` with the reservation token. If the launch reply was lost, find the child by slot and token in the brief before releasing. Keep a legacy slot with unknown PRs exclusive until its run ends and its receipt clears.

## A QA session ends

Read both slots for receipts from `maestro-reap` or `maestro-qa-release`. The current planner for the receipt’s goal handles it. For a legacy receipt, match its saved QA IDs or launch brief to your goal first. Clean the QA run by thread ID per “Worktrees”, then close verified merges as above.

Unless a stop cancelled the run, post `QA blocked` on each still-open batch PR whose current head got no `QA fail` or `QA blocked` since launch. Use reason `QA ended without a verdict` or `QA launch failed` as appropriate and the first-line format in “Report” in `.maestro/skills/qa/SKILL.md`. Use the current base SHA if QA recorded none. Skip a block when you released a reservation because the batch failed the launch recheck.

Route verdicts, clear the receipt with `maestro-qa-ack`, and wake the planner returned by `maestro-wake-targets` for QA. Check the stop and QA queue and assign ready work.
