# 0505 Release at trust level 1: open a PR, then close on merge

- **Context:** 0012 says level 1 opens a PR and stops. Only Ops sets Done after release, and the brief has no "PR open" status.
- **Choice:** At level 1, `release` runs `open-pr`, leaves the ticket `ready_to_release` with `Handoff: PR opened <url>`, and releases the lease. On later runs, `pr-status` decides with no tokens: `open` ends the run, `merged` skips to checking main and Ops sets `done`, `closed` escalates to the owner. Level 2+ uses `merge` (the merge queue, Phase 9); deploy needs level 4.
- **Alternatives:** Set Done when the PR opens (it isn't released); a new `in_pr` status (changes a settled list).
- **Why:** No merge or main-branch push from a bot at level 1, and Done still means landed.
