# 0408 Only the Operations Manager opens PRs, merges and deploys, by trust level

- **Context:** G4.9 and 0012: role permissions ignored trust levels (Ops could push at level 1), and engineers couldn't open PRs. The brief doesn't say who opens the pull request.
- **Choice:** Sessions never push, merge or deploy. Those are runner requests for the Operations Manager only: `open pr` from level 1, `merge` through the merge queue from level 2 (low-risk classes) or 3 (all but high-risk), `deploy` at level 4. At level 1 the ticket waits in Ready to release until the user merges, then Ops sets Done. Engineers commit on their `maestro/` branch and stop.
- **Alternatives:** Engineers open a PR at submit (humans would see every change before Maestro's own review and QA, and every send-back would churn it); Ops merges at level 1 when tests pass (breaks "PRs only").
- **Why:** One PR per ticket, opened once the work passed review and QA, and the trust level is enforced in one program instead of per-role deny rules.
