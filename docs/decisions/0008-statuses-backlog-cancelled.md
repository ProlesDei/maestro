# 0008 Status list: add Backlog, keep Cancelled for now

- **Context:** The brief's statuses are Backlog, To do, In progress, Blocked, In review, In QA, Ready to release, Done. The code has no `backlog`, and adds `cancelled`.
- **Choice (proposed):** Add `backlog` (claimable only by the Planner's grooming, never by `claim --next`). Keep `cancelled` as a second terminal status for duplicates and dropped work, so Done keeps meaning "released".
- **Alternatives:** Drop `cancelled` and close dropped work as Done (muddies Done and the "only Ops sets Done" rule); leave abandoned tickets in Backlog forever.
- **Why:** Cancelled is cheap and keeps metrics honest. It changes a settled list, so **Status: needs Paul**.
