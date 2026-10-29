# 0435 Forwarding an escalation posts a note, not a move

- **Context:** `planner/answer-escalation` forwarded a question by moving the `blocked` ticket to `blocked` with `--role chief-of-staff`. maestro-ticket has no `blocked → blocked` move (it didn't before 0200 either), so the forward exited 2 and the question never reached the Chief of Staff.
- **Choice:** A forward is `maestro-escalate` with target `keep`: post the `Escalation (chief-of-staff):` note and release, with no transition. The Chief of Staff's intake takes any `blocked` ticket whose latest comment has that prefix, whatever its role. When it unblocks, it passes `--role` for whoever acts next.
- **Alternatives:** Let the store allow `blocked → blocked` as a role change (a store change for one hop; the prefix already routes it, per 0503).
- **Why:** The prefix is the routing rule in 0503, and the store keeps `blocked_from` untouched.
