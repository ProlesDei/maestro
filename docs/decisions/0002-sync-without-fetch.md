# 0002 Treat the checkout as synced without a fetch

- **Context:** Phase 0 says to sync the checkout once a token arrives, and the brief says the checkout is behind `main`. The box still can't fetch, but Paul says local `main` is byte-identical to origin/main (tree `81260e5`).
- **Choice:** Audit local `main` at `17b6a3e` (tree `81260e5d68f4`) as if it were origin/main. Leave "checkout matches main" unverified from the box until a fetch works.
- **Alternatives:** Wait for the token before auditing; audit the old PR #3 base.
- **Why:** The audit doesn't need the network, and the tree hash is a stable thing to check later with `git rev-parse origin/main^{tree}`.
