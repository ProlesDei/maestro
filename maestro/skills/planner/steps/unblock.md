# Unblock work

Group blocks by cause. First use “Whose call” in `.maestro/skills/chief-of-staff/SKILL.md` to route owner calls to the chief of staff. Answer the rest from the goal and specs, and tell each affected agent.

If the spec needs a fix, use `steps/update-ticket.md`. Keep a broken-base repair on one ticket. To split another ticket, file its parts with the original's labels using `steps/create-ticket.md`, and point its dependents at them. If it is a stop fix, replace it in the active stop with its parts and apply the stop rule again. See “The chief of staff calls a stop” in the [planner skill](../SKILL.md). Close the original ticket and its PR.

Send every call you can’t settle to the chief of staff. Say what you tried, which work must wait, and what decision you need.
