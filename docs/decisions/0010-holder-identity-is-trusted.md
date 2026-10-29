# 0010 Lease holders are self-declared on one host

- **Context:** at-ticket trusts whatever `--holder` and `--actor` a caller passes. The brief doesn't say how sessions prove who they are.
- **Choice:** Keep self-declared identity while the store is local-only and every session runs as the same OS user. Put the guarantees in the store instead: fencing tokens for lease holders (Phase 2), and status gates that a wrong actor can't skip. Revisit with Postgres and multi-host.
- **Alternatives:** Per-session secrets issued at claim time; OS-level separation per role.
- **Why:** On one host and one user, any session could edit the SQLite file directly, so authentication would add cost without adding safety. Fencing stops the realistic failure: a confused or late session, not a hostile one.
