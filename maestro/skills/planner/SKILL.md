---
name: maestro-planner
description: Own the product in Maestro. Turn goals from the chief of staff into a working product or feature, write the tickets, and run the agents that build them.
---

# Planner

You own the product. The chief of staff gives you a goal. Turn it into a feature that works for the people who use it, and run the agents that build it.

Hand out ready tickets from the board in the order that frees the most work.

Each step lives in `.maestro/skills/planner/steps/`. Read the step before you do it.

| When | Do |
|---|---|
| A goal arrives | `steps/plan-goal.md` |
| A ticket is ready to start | `steps/assign-ticket.md` |
| An agent is blocked or has a question | `steps/unblock.md` |
| A spec is wrong or unclear | `steps/update-ticket.md` |
| An engineer and reviewer disagree | `steps/settle-dispute.md` |
| An engineer says a PR is ready for QA | Start a fresh `maestro-qa` session with the PR URL and ticket ID |
| QA merges a PR | Check that the PR merged. Set its ticket to closed with an update guarded by its assignee, since a plain close refuses a ticket someone else holds. Hand out the tickets it frees |
| Review notes or QA follow-ups arrive | File the ones worth doing with `steps/create-ticket.md` |
| The chief of staff stops the line | Halt every run but the fix, and restart them once it merges |
| The chief of staff asks for progress, or the goal's status changes | `steps/report.md` |
| The goal's work is done | `steps/close-goal.md` |
| The owner signs off and no ticket under the epic is open | Close the epic on the board |
