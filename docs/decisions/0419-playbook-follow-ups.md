# 0419 Playbook changes Phase 4 needs but didn't make

- **Context:** Phase 4 changed names and paths that playbooks use, but another session owns `playbooks/` (0400). Playbooks work as they are once these are updated.
- **Choice:** Leave `playbooks/` alone, and list here what the playbooks session should change:
  - `playbooks/README.md`: wrap-up names `maestro-propose-memory`, `maestro-handoff-note` or `maestro-escalate`, and `maestro-log-entry` instead of `maestro-wrap-up` (0404). Drop "Long steps run `maestro-ticket heartbeat`" (0405). Limits come from preflight, not `.maestro/environment.md`. The rule for closing tickets with no code must not let anyone but Ops set Done on a ship ticket (`team-rules.md`).
  - Paths (0406): `.maestro/project-setup.md` → `.maestro/project/project-setup.md` (19 lines), `.maestro/memory/` → `.maestro/project/memory/` (3), `.maestro/environment.md` → preflight values (3).
  - Every `Tier: No tokens` step becomes a runner request (`docs/runner.md`): `worktree add`, `check <name>`, `ticket …`.
  - `operations-manager/release.md`: `git merge` and push become `open pr`, then `merge` or `deploy` only as the trust level allows (0408, 0012).
  - `planner/plan-project.md` and `planner/groom-board.md`: no `maestro-ticket render` (0404), and one Planner session per workstream (0407).
  - `chief-of-staff/triage-request.md` and `handle-escalation.md`: route requests to workstreams and answer escalations from Planners; never write or edit tickets (0407).
  - `qa-lead/verify-ticket.md` and the engineers' submit steps: use `maestro-verify`; send-backs from review and QA add a try (G5.1).
- **Alternatives:** Edit the playbooks in this run (another session owns them and would conflict).
- **Why:** Nothing gets lost between sessions, and the stale-reference check for Phase 4 can stay limited to the files it owns.
