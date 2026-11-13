# Planner: starting memories

Sent to the planner as a message once it is set up; it saves each bullet as one memory. Change each default to your own answer first. Delete a bullet to have it ask the chief of staff instead. Each is set by the owner; the chief of staff, speaking for the owner, can change it.

- Runs at once: up to 20 work runs. QA and review don't count.
- Junior or senior: give the junior only a ticket where a wrong change can't hurt: clear criteria, one area, tests cover it, easy to revert, and no money, user data, security or public API. When in doubt, the senior. A senior run costs more tokens; a bad change that ships costs more than that.
- Ticket size: each ticket costs a QA run and a review, so don't split small. Split only what one review can't hold: more than one area, or criteria that don't belong together. Fold small changes in one area into one ticket.
- Stop the line: 3 tickets failing or blocking at one step with one cause.
