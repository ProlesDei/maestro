# 0430 The store's type is the lifecycle; the kind is a body line

- **Context:** Integrating Phase 2 track A with Phases 4 and 5. The store's `create --type` takes `ship` or `scout` (0200). The playbooks passed `--type investigation` and `--type chore`, which now exit 2, and route by the kinds `feature`, `bug`, `chore`, `docs`, `investigation`.
- **Choice:** `--type` is the lifecycle: `scout` for investigations and infrastructure requests (0433), `ship` for the rest. The kind goes in the body as `Type: <kind>`, which playbook frontmatter `types` keeps matching. `maestro-write-tickets` no longer falls back on a missing `--type`.
- **Alternatives:** Teach the store the five kinds and map them to two workflows (a schema and CLI change to a reviewed store, for a label only the bots read); drop kinds from the playbooks (they pick between playbooks of one role).
- **Why:** The store gates only what differs between lifecycles. The kind is routing data, and a body line already carried it as the fallback.
