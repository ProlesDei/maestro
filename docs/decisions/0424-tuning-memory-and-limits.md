# 0424 Limits come from preflight; tuning and memory land as pull requests

- **Context:** Playbooks read budgets, thresholds and the memory budget from `.maestro/environment.md`, which 0406 removed. The improvement loop "changed `environment.md`", which sessions may not edit. Memory curation committed inbox files that live outside git, kept role memory outside the repo against `team-rules.md`, and had no worktree to commit in.
- **Choice:** Limits and thresholds live in `.maestro/project/budgets.json` (0406), and preflight returns them. Tuning is a commit to `budgets.json` on a `maestro/tuning-<date>` worktree, opened as a pull request. Memory is curated on a `maestro/memory-<date>` worktree, role lessons in `.maestro/project/memory/roles/`. Handled inbox files are deleted, never committed. `docs/runner.md` lets Ops create those two kinds of worktree and edit those paths.
- **Alternatives:** Let the runner write tuning directly (no review at trust level 1); keep limits in `.maestro/local/environment.md` (sessions can't edit it, and it isn't shared by clones).
- **Why:** At trust level 1 nothing reaches the main branch without a human, and every limit has one home.
