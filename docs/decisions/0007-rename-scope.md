# 0007 What the rename covers

- **Context:** The brief names `at-`, `.agent-team/`, `AT_TICKET_STORE` and the store path. It doesn't mention `AT_TICKET_ACTOR`, the `at/` branch prefix, the Go module path `tools/at-ticket`, or the `T-<n>` ticket ids.
- **Choice (proposed):** Rename all of them except `T-<n>`: `AT_TICKET_ACTOR` → `MAESTRO_TICKET_ACTOR`, `at/` branches → `maestro/`, `tools/at-ticket` → `tools/maestro-ticket` (binary `maestro-ticket`). Keep `T-<n>`.
- **Alternatives:** Rename only what the brief lists.
- **Why:** Phase 1 is done when nothing references the old names, and these are old names. `T-<n>` is a format, not a name. **Status: needs Paul** (part of the rename approval).
