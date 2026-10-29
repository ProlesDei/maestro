# 0100 Rename docs/agent-team.mermaid to docs/team.mermaid

- **Context:** Phase 1 is done when nothing outside `docs/design/` and `docs/decisions/` matches the old names. The diagram file `docs/agent-team.mermaid` matches `agent-team`, though it names the team of roles, not the store path or install folder. Neither the brief nor 0007 lists it.
- **Choice:** Rename it to `docs/team.mermaid` and update the pointers in `AGENTS.md` and `README.md`. The plain-English phrase "agent team" stays. The stale `docs/design/agent-team.mermaid` stays as it is until Phase 4 deletes it (0006).
- **Alternatives:** Keep the name and exclude it from the check; rename it to `docs/maestro-team.mermaid` (but `maestro-` marks harness skills and the ticket tool).
- **Why:** One grep with no exceptions is the simplest done check, and a short name avoids reusing the `maestro-` prefix.
