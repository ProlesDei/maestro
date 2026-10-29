# 0501 Playbooks name a fixed set of runner commands

- **Context:** 0011 says a runner runs no-token steps from an allowlist, but not which commands exist. Playbooks need names to point at.
- **Choice:** `playbooks/README.md` lists the runner commands (`ticket`, `worktree`, `checks`, `budget`, `trust`, `open-pr`, `pr-status`, `merge`, `deploy`/`smoke`/`rollback`, `summary`, `logs`, `memory-scan`, `housekeep`, `evals`, `install`). Every `Run:` line must use one; the check enforces it. The allowlist per role and trust level belongs in bot skill `runner` (Phase 4), and the runner itself in `tools/` (Phase 3/4).
- **Alternatives:** Raw shell commands in `Run:` (the bot would be choosing commands, and the allowlist couldn't be a short list); one runner command per playbook step.
- **Why:** A short, named vocabulary is easy to allowlist by trust level and to log, and keeps git and deploy details out of playbooks.
