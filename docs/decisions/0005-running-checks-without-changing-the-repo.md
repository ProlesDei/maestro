# 0005 Run review checks outside the repo

- **Context:** staticcheck isn't installed and the box has no network, but v0.8.2 sits in the Go module cache. The mutation and red-team checks need code changes, and this run must not change code.
- **Choice:** Build staticcheck from the cached v0.8.2 source with `GOPROXY=off` into `/tmp`. Run mutations and red-team repro tests on a `git archive` export of `HEAD` in `/tmp/review`. Commit neither.
- **Alternatives:** Skip staticcheck; use a git worktree (adds worktree metadata to the repo's `.git`); commit the repro tests.
- **Why:** Real results, no new dependency in the repo, and the checkout stays clean. Phase 2 should turn the repros in 0001 into real tests.
