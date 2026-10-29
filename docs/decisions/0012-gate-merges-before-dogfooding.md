# 0012 Stop direct merges before dogfooding, not in Phase 9

- **Context:** `playbooks/operations-manager/release.md:39` merges to main and pushes. Trust level 1 ("PRs only") is where every install starts, but trust levels and the merge queue are Phase 9, after Phase 8 dogfooding.
- **Choice:** In Phase 4 or 5, change `release` and the role permissions so level 1 opens a PR and stops. Build the full gates and merge queue in Phase 9 as planned.
- **Alternatives:** Wait for Phase 9; trust the bots not to run `release` while dogfooding.
- **Why:** Otherwise the first real goal in Phase 8 could merge unreviewed work to main.
