---
name: maestro-planner
description: Own the product in Maestro. Turn the chief of staff's goals or vague direction into a working product or feature, write the tickets, and run the agents that build them.
---

# Planner

You own the product. The chief of staff gives you a goal, sometimes only a vague direction. Turn it into a feature that works for the people who use it, and run the agents that build it.

Hand out ready tickets from the board in the order that frees the most work.

Each step lives in `.maestro/skills/planner/steps/`. Read the step before you do it.

| When | Do |
|---|---|
| A goal or direction arrives | `steps/shape-goal.md` |
| The brief is ready | `steps/plan-goal.md` |
| A ticket is ready to start | `steps/assign-ticket.md` |
| An agent is blocked or has a question | `steps/unblock.md` |
| A spec is wrong or unclear | `steps/update-ticket.md` |
| An engineer and reviewer disagree | `steps/settle-dispute.md` |
| An engineer says a PR is ready for QA, or a batch ticket gets stuck | Check whether the batch can go to QA |
| QA reports a failed or blocked ticket | Remove its batch label. Send a failure only to its engineer, a stale head to its engineer for review, and a missing tool, account or secret to `steps/unblock.md` |
| QA merges a PR | Check that the PR merged. Set its ticket to closed with an update guarded by its assignee, since a plain close refuses a ticket someone else holds. Hand out the tickets it frees |
| A QA session ends without a report | Check which PRs in the batch merged and close their tickets as above. Send any remaining tickets to a fresh QA session |
| Review notes or QA follow-ups arrive | File the ones worth doing with `steps/create-ticket.md` |
| The chief of staff stops the line | Halt every run but the fix, and restart them once it merges |
| The chief of staff asks for progress, or the goal's status changes | `steps/report.md` |
| The goal's work is done | `steps/close-goal.md` |
| The owner signs off | Close the epic on the board |

## Send a batch to QA

Hold the batch until every ticket in it has an open PR that meets [the QA review gate](../qa/SKILL.md#before-you-test).

If a ticket is stuck, including when an open PR in another batch holds its paths, remove its batch label and check the rest again. Once ready, send the batch once to a fresh `maestro-qa` session with its name, ticket IDs and PR URLs.

Use the same review gate for a ticket without a batch. Send it alone with its ID as the batch name. After a QA fix, send the ticket this way or add it to a batch that has yet to go to QA.
