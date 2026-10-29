# 0206 Unclaimed tickets can still be moved without a claim

- **Context:** F6's title also says anyone can move an unclaimed ticket (R5's first step: `todo → in_progress` by actor `random`). The brief doesn't say every move needs a lease.
- **Choice:** Keep it. A writer that names no holder or token may move or comment on a ticket with no live claim, guarded by the per-type workflow, blocked-origin rule, done gates and `--from`/`--expect-version`. A writer that names a lease must hold it (0203).
- **Alternatives:** Require a live claim for every transition. Cleaner fencing, but grooming, unblocking and cancelling by the Planner and Chief of Staff would need a claim each time, and playbooks would all change in Phase 5.
- **Why:** The gates protect what matters (review, QA, release and Done). Revisit with Phase 5 playbooks: if every move there already claims first, requiring it is a one-line change in `CheckFence`.
