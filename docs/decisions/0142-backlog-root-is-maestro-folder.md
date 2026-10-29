# 0142 Backlog.md mirror lives at `.maestro/backlog/`

- **Context:** The brief says everything real lives in one namespaced folder. Backlog.md's `backlog_directory` key only works from a `backlog.config.yml` at the project root, which would put a file at the repo root (0140).
- **Choice:** Make `.maestro/` the Backlog.md project root. Run `backlog init "<name>" --defaults --integration-mode none --cwd .maestro`, and pass `--cwd .maestro` (or `BACKLOG_CWD`) on every later call, so the data and `config.yml` stay in `.maestro/backlog/`. List that folder in the manifest. Not tried on the box yet: the installer's tests must prove it.
- **Alternatives:** A root `backlog.config.yml` with `backlog_directory: .maestro/backlog`. Or skip Backlog.md.
- **Why:** No file outside `.maestro/`, and root discovery walks up from cwd (`find-backlog-root.ts`), so no Backlog.md setting is needed. The cost is that a human must pass `--cwd .maestro` too.
