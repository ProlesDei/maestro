# 0422 The project's verification guide is `.maestro/project/verify.md`

- **Context:** Phase 5's `qa-lead/verification-skill` installed a project-local skill, `skills/maestro-verify-app/SKILL.md`. Phase 4 put the same thing at `.maestro/project/verify.md` (0406), and `maestro-verify` already reads it.
- **Choice:** The playbook drafts `.maestro/project/verify.md` with `maestro-draft-setup` and writes it with `install write`. A fresh session proves the draft once with `maestro-verify`. The playbook keeps the brief's name, `verification-skill`; the ticket is titled `Verification guide`.
- **Alternatives:** A project-local skill (it would need generating into each harness's skill folder, and `maestro-verify` would load two skills for one step).
- **Why:** One file in the committed footprint, one skill that reads it.
