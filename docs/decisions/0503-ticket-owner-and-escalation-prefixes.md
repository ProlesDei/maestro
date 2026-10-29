# 0503 Ticket owner and escalation routing

- **Context:** G5.5. The chain is session → ticket owner → Chief of Staff → user, but no file said who the owner is, and `Escalation:` went straight to the Chief of Staff.
- **Choice:** The owner is the role that owns the ticket's workstream, normally its Planner (`Owner: <role>` in the body until the store has the field). `Escalation:` on `blocked` goes to the owner (`planner/answer-escalation`); `Escalation (chief-of-staff):` goes to the Chief of Staff, used when the owner forwards, when the owner itself is asking, for Gate approvals and live security problems. `Escalation:` on `todo` stays the Junior → Senior hand-off.
- **Alternatives:** Owner = the ticket's current role (then a blocked engineer escalates to itself); route everything through the owner, Gates included (the owner can't approve above the trust level, so it's a wasted hop).
- **Why:** Most blocks are questions the Planner can answer, so the Chief of Staff sees only what needs it. Needs `maestro-check-intake` to route the two prefixes.
