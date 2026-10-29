# 0400 Phase 4: one rule, one file

- **Context:** Phase 4 fixes G4.1–G4.11 (0001) and 0006. This run may edit `bot-profiles/`, `skills/`, `docs/`, `README.md` and `AGENTS.md`, but not `playbooks/` or `tools/`, which other sessions own.
- **Choice:** Delete `docs/design/agent-team.mermaid` and `docs/design/bot-anatomy.mermaid` (0006). Every rule gets one home, and the rest link to it: bot rules in `bot-profiles/team-rules.md` (0402), harness rules in `skills/harness/rules.md` (0402), the runner's contract in `docs/runner.md` (0403), model choice in `skills/bot/model-selection`, prompts and sessions in `skills/bot/prompt-contract`. Where a playbook still uses an old name or path, list it in 0419 for the playbooks session instead of editing it.
- **Alternatives:** Edit playbooks too (another session owns them); leave the rules spread out and only add links.
- **Why:** Phase 4 is done when every rule lives in exactly one file. Two copies of anything drift.
