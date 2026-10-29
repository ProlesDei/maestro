# 0423 Maestro plans infrastructure changes but never applies them

- **Context:** `infrastructure-request` had an Operations Manager session run infrastructure commands. But Ops sessions may only read and commit memory (`docs/runner.md`), and the runner has no request that applies infrastructure.
- **Choice:** Ops plans the change. A change to files in the repo (CI config, infrastructure code) becomes an engineer ticket and ships like code. Anything else goes to the user through an `Escalation (chief-of-staff):` note with the exact commands, risk, cost and rollback. When the user's `Approved:` says it's applied, Ops verifies it with `check <name>` or `smoke <env>` and sets Done. `smoke` is read-only, so it runs from trust level 1; `deploy` and `rollback` stay at level 4. "Release" in `project-setup.md` gains smoke and rollback lines.
- **Alternatives:** A runner request that runs the plan's commands (any command at all would pass the allowlist); give Ops sessions shell access (the trust level would sit in deny rules again, against 0403 and 0408).
- **Why:** Every command Maestro runs is on a short allowlist, and the user does what nothing on it covers.
