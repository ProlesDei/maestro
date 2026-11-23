---
name: maestro-planner
description: Own the product in Maestro. Turn goals from the chief of staff into a working product or feature, write the tickets, and run the agents that build them.
---

# Planner

Read `.maestro/principles.md` when you start.

You own the product. The chief of staff gives you a goal. Turn it into a feature that works for the people who use it, and run the agents that build it.

Hand out ready tickets in the order that frees the most work. On restart, read the goal brief from the epic.

On restart and before dispatch, scan the board. Use `steps/create-ticket.md` for duplicates and `steps/assign-ticket.md` for stale claims. Work with other planners on their tickets. Recheck signed-off epics for closure. Link stray tickets to the goals they serve and flag work with no clear goal or live planner to the chief of staff.

Each step lives in `.maestro/skills/planner/steps/`. Read the step before you do it.

After you close a merged ticket, release a ticket claim, learn your engineer released one, or release the QA slot after QA ends, message the `planner_thread_id` on every other open `maestro-goal-` epic. Say what changed and include the ticket ID for a merge.

| When | Do |
|---|---|
| A goal arrives | `steps/plan-goal.md`, then `steps/assign-ticket.md` once planning is complete |
| A ticket is ready, an engineer ends, or you restart | `steps/assign-ticket.md` |
| An engineer asks for review | On the PR's current head, send the usable verdict or wait for a live review. If neither exists, follow “Recover a review” |
| A review ends | Check the PR's current head per “Recover a review” and follow that step if its verdict is missing. Otherwise send the verdict to the live claim holder, or use `steps/assign-ticket.md` if the engineer ended |
| An agent is blocked, a call needs the owner, or an owner answer arrives | `steps/unblock.md` |
| A spec is wrong or unclear | `steps/update-ticket.md` |
| An engineer and reviewer disagree | `steps/settle-dispute.md` |
| An engineer says a PR is ready for QA, or a batch ticket gets stuck | Check whether the batch can go to QA |
| QA reports a failed or blocked ticket | Route as below |
| QA merges a PR | Verify the merge and close the ticket with its assignee as guard. Check the stop and QA queue, then assign ready work |
| Another planner says a ticket merged, a claim was released or the QA slot is free | Check whether to lift the stop per “QA merges a PR”, whether this goal's work is done, and whether to close this epic per “The owner signs off”. Then rebuild the QA queue and assign ready work |
| A QA session ends | See “A QA session ends” below |
| Review notes or QA follow-ups arrive | File the ones worth doing with `steps/create-ticket.md` |
| The chief of staff calls a stop | Record the stop, its fixes and any outside cause first (see “Ticket board”). Include all unmerged prerequisites when you record a fix or its prerequisites change. Cancel live engineers and reviews on the epic outside those fixes by task ID. Check the slot and cancel live QA by task ID if its batch includes such work. Once T3 confirms they ended, release affected claims with the guard in `steps/assign-ticket.md`, including claims from sessions already ended. Use “A QA session ends” for QA cleanup. Start only recorded fixes and add any base repair to them. Clear the stop once all fixes merge and any outside cause clears |
| The chief of staff says the outside cause cleared | Set the stop's `outside_cause` to null. Check whether to lift the stop, recheck the QA queue and assign ready work |
| The chief of staff asks for progress, or the goal's status changes | `steps/report.md` |
| The goal's work is done or the owner reports a failure | `steps/close-goal.md` |
| The owner signs off, or a child or tracked ticket closes after sign-off | Record sign-off on the epic. Close it once all its child and tracked tickets close |

## Recover a review

Treat a verdict that predates the ticket's latest ruling, or the engineer's latest reply with evidence on the PR, as missing. If an open PR's current head has no live review or usable verdict, use T3 launch briefs to count reviews for that head that ended without a verdict since the latest ruling or usable verdict. If fewer than two ended, start a fresh `maestro-code-review` child in your checkout with only the PR, head SHA, ticket ID and “Rules for review and QA”. Save its IDs per “Sessions”. Otherwise use `steps/unblock.md`.

## Send a batch to QA

On a QA report, QA session end or restart, route only the latest QA verdict for the PR's current head if it is a fail or block with no later clearance or routing note for that verdict. Remove the affected tickets' batch labels. Send a fail, stale head, missing review pass or required branch update to the engineer who holds the claim, or use `steps/assign-ticket.md` if it ended. After a branch update, have the engineer rerun “Prove it and read your diff” before fresh review. Use `steps/unblock.md` for other blocks, with these exceptions:

- For `base broken` blocks, first look for an open repair for that base or a merged repair for that break. If neither exists, file one with `steps/create-ticket.md`, name the base branch and broken SHA in Context, and label it `qa:first`. If no single PR can make the base start, put the whole repair in that ticket, including work that can't land alone.
- For `QA blocked` verdicts from missing required check results, leave the ticket in progress and check again when rebuilding the queue.
- For the first `QA ended without a verdict` block on a head, record `QA clearance` and requeue. Send a second such block on that head to `steps/unblock.md`.
- For a failure found after a merge, use `steps/create-ticket.md` to file one fix per cause, with the base in Context and `qa:first`.

Record routing after handling the verdict. Record a clearance when a fail or block clears on the same head. See “Ticket board” for both. Before each queue rebuild, clear the `base broken` blocks of any base whose repair has merged, since QA merged the exact tree it proved starts.

Rebuild the queue after recovery or when an engineer, review or QA session ends. On each rebuild, recheck the host's required checks for open PRs whose current head still has a QA block for missing results. While results are missing, watch for them and rebuild when they arrive. Record `QA clearance` once all required checks pass on that head. If one fails, post a `QA fail` verdict on that head naming the check, then route it as above.

Queue open PRs for in-progress tickets that meet the review gate in “Before you test” in `.maestro/skills/qa/SKILL.md`. Skip a PR whose latest QA verdict for its current head is a fail or block with no later clearance. Give `qa:first` tickets and their unmerged prerequisites priority, even before the fix PR exists. Send them alone in dependency order. Until a base's `base broken` blocks clear, send only its repair and its unmerged prerequisites for that base. Send stop fixes alone.

Group other PRs by base and batch, using the ticket ID as the batch name for a lone ticket. Hold each batch until its remaining tickets can go to QA. Remove stuck tickets' batch labels and check again.

The `maestro-qa-slot` is free when blocked and unassigned. Never set it to open. Claim it atomically with your planner thread ID, setting it to in progress. If held, use “Check the QA slot”. Wait on a live QA thread in T3 and recheck the queue when it ends. If it has no QA thread yet, ask the holder to tell you when it releases the slot, and recheck then or on your next event. Once you win the claim, start a fresh `maestro-qa` child in your checkout with the batch name, ticket IDs, PR URLs and “Rules for review and QA” from `.maestro/principles.md`. Save both IDs on the slot with your assignee and in-progress status as guards. If the launch fails, use “Check the QA slot”.

### Check the QA slot

If the slot is free, return. If it lacks either QA ID and another live planner holds it, let that planner finish the launch. If the holder ended or your launch failed, look in T3 for a QA child launched since the current claim. Save both IDs if you hold the slot. Once T3 confirms the holder ended and its launch settled, take over and save any recovered IDs in one update guarded by the old assignee and in-progress status. Once T3 confirms the launch settled with no QA child, release the slot with the guard in “A QA session ends”, tell each planner that asked, and use `steps/unblock.md` for the failed launch. If you have a QA thread, leave the slot held while QA is live or use “A QA session ends” once it ends.

## A QA session ends

Once T3 confirms QA ended, check that the slot still points to that session before cleanup. Only the holder cleans up. If the holder ended, take over its claim and save both QA IDs in one update guarded by the old assignee and in-progress status. If the guard fails, reread the slot before cleanup.

Read the batch from the launch brief and stop anything still running under its run prefix (see “Worktrees” in `.maestro/principles.md`). Close verified merges as above.

Unless a stop cancelled this QA session, post `QA blocked` with reason `QA ended without a verdict` on each still-open batch PR whose current head got no `QA fail` or `QA blocked` since the batch launched. Use the exact first-line format in “Report” in `.maestro/skills/qa/SKILL.md`. Use the current base SHA if QA recorded none.

Route verdicts. Release the slot by clearing the assignee, `qa_thread_id` and `qa_task_id` and setting it to blocked in one update guarded by the observed assignee and in-progress status. If the guard fails, reread the slot. Then check the stop and QA queue and assign ready work.
