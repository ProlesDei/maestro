# 0420 One runner vocabulary, in `docs/runner.md`

- **Context:** Phase 4 (0403) and Phase 5 (0501) each wrote a list of runner requests, in parallel. They clashed on names (`worktree add` vs `worktree`, `check <name>` vs `checks`, `usage check` vs `budget`, `open pr` vs `open-pr`), and each lacked requests the other needed (`summary`, `logs`, `memory scan`, `housekeep`, `evals`, `install`, `pr status`, `trust`, `smoke`, `rollback`).
- **Choice:** `docs/runner.md` is the only list. It keeps Phase 4's spaced names, adds every request the playbooks use with its roles and trust level, and adds `check all`, `--in <id>-qa` and `--at <sha>`. `playbooks/README.md` links to it instead of keeping a table, and `evals/check.py` reads the table from there. `ticket render` is gone from playbooks (0404). This replaces where 0501 put the list, not its idea.
- **Alternatives:** Keep Phase 5's table in `playbooks/README.md` and make the runner doc point to it (the contract would live away from the runner); keep both (they'd drift again).
- **Why:** One rule, one file, and the contract sits next to the allowlist it describes.
