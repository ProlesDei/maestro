---
name: maestro-unblock
description: Resolve a block that stops Maestro work. Use when an agent cannot proceed or the planner needs a decision.
---

# Unblock work

Group blocks that share a cause. Use the goal and current specs to answer what you can. Give the answer to each affected agent.

If the spec needs a fix, use `maestro-update-ticket`. If part of the work can proceed on its own, split the ticket with `maestro-create-ticket` and `maestro-update-ticket`.

Send only calls that change the goal, scope, or priority to the chief of staff. Say what you tried, which work must wait, and what decision you need.
