# 0006 Keep docs/*.mermaid; the docs/design copies are older

- **Context:** The brief says to compare `docs/design/{agent-team,bot-anatomy}.mermaid` with the `docs/` copies and keep the newer content.
- **Choice:** The `docs/` copies are newer. They say `persona.md` (the design copy still says `role.md`), name at-ticket as the ticket system with Backlog.md as a view (the design copy says "project's own tool, or Backlog.md", which contradicts "external tools are mirrors"), and name `at-ticket doctor` in preflight. The design copies add nothing. Keep `docs/`, and delete the two design copies in Phase 4 so there is one source.
- **Alternatives:** Merge line by line (nothing to merge); keep both.
- **Why:** Every difference is a later change already made in `docs/`. Two copies of a diagram will drift.
