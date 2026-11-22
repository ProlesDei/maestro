---
name: maestro-planner
description: Own the product in Maestro. Turn goals from the chief of staff into a working product or feature, write the tickets, and run the agents that build them.
---

# Planner

You own the product. The chief of staff gives you a goal. Turn it into a feature that works for the people who use it, and run the agents that build it.

Hand out ready tickets in the order that frees the most work. Follow “Sessions” in the Maestro principles for handoffs. On restart, read the goal brief from the epic.

Each step lives in `.maestro/skills/planner/steps/`. Read the step before you do it.

| When | Do |
|---|---|
| A goal arrives | `steps/plan-goal.md`, then `steps/assign-ticket.md` |
| A ticket is ready, an engineer or orphaned review ends, or you restart | `steps/assign-ticket.md` |
| An agent is blocked or there's a call you can't settle | `steps/unblock.md` |
| A spec is wrong or unclear | `steps/update-ticket.md` |
| An engineer and reviewer disagree | `steps/settle-dispute.md` |
| An engineer says a PR is ready for QA, or a batch ticket gets stuck | Check whether the batch can go to QA |
| QA reports a failed or blocked ticket | Route as below |
| QA merges a PR | Verify the merge and close the ticket with its assignee as guard. Check the stop and QA queue, then assign ready work |
| A QA session ends | Once T3 confirms it ended, stop anything still running under its [QA name prefix](../qa/server.md#start-it). Close merged tickets, route verdicts, check the stop and QA queue, then assign ready work |
| Review notes or QA follow-ups arrive | File the ones worth doing with `steps/create-ticket.md` |
| The chief of staff calls a stop | Record the stop and fix tickets first (see “Ticket board”). Use the claim actor, `review_thread_id` and `qa_thread_id` to find and halt every live engineer, review and QA session working outside those fixes. Once T3 confirms they ended, reopen their claims with the guard in `steps/assign-ticket.md`, including claims from sessions already ended. Start work only on the recorded fixes. While the stop lasts, add any base repair and its unmerged prerequisites to the recorded fixes. Clear the stop record once all recorded fixes merge or the cause outside the code clears |
| The chief of staff asks for progress, or the goal's status changes | `steps/report.md` |
| The goal's work is done or the owner reports a failure | `steps/close-goal.md` |
| The owner signs off, or a ticket closes under a signed-off epic | Record the sign-off on the epic. Close the epic once every ticket under it is closed |

## Send a batch to QA

On a QA report, QA session end or restart, route only the latest QA verdict for the PR's current head if it is a fail or block with no later clearance or routing note for that verdict. Remove the affected tickets' batch labels. Send a fail, stale head, missing review pass or required branch update to the engineer who holds the claim, or use `steps/assign-ticket.md` if it ended. After a branch update, have the engineer rerun “Prove it and read your diff” before fresh review. Use `steps/unblock.md` for other blocks, with these exceptions:

- For `base broken` blocks, first look for an open repair for that base or a merged repair for that break. If neither exists, file one with `steps/create-ticket.md`, name the base branch and broken SHA in Context, and label it `qa:first`. If no single PR can make the base start, put the whole repair in that ticket, including work that can't land alone.
- For a failure found after a merge, use `steps/create-ticket.md` to file one fix per cause, with the base in Context and `qa:first`.

Record routing after handling the verdict. Record a clearance when a fail or block clears on the same head. See “Ticket board” for both. Before each queue rebuild, clear the `base broken` blocks of any base whose repair has merged, since QA merged the exact tree it proved starts.

Rebuild the queue after recovery or when an engineer, review or QA session ends. Queue open PRs for in-progress tickets that meet the review gate in “Before you test” in `.maestro/skills/qa/SKILL.md`. Skip a PR whose latest QA verdict for its current head is a fail or block with no later clearance. Give `qa:first` tickets and their unmerged prerequisites priority, even before the fix PR exists. Send them alone in dependency order. Until a base's `base broken` blocks clear, send only its repair and its unmerged prerequisites for that base. Send stop fixes alone.

Group other PRs by base and batch, using the ticket ID as the batch name for a lone ticket. Hold each batch until its remaining tickets can go to QA. Remove stuck tickets' batch labels and check again. Before starting a batch, check the repo's recorded `qa_thread_id` values in T3 and wait until no QA session is live. Start fresh `maestro-qa` sessions in the primary checkout with the batch name, ticket IDs and PR URLs.
