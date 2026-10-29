# 0225 `events --wait` for the zero-token watcher

- **Context:** G6.1. The brief's watcher "sleeps on `events --since <seq>`", but `events` returned at once, so a watcher had to poll in a shell loop.
- **Choice:** `events --wait D`: when nothing matches after `--since` (and the other filters), poll the store every 200ms for up to D, and return as soon as an event matches. On timeout it prints an empty list with `next_seq` unchanged and exits 0, so the watcher just loops. SIGINT and SIGTERM end the wait early.
- **Alternatives:** A `--follow` stream of JSON lines (a long-lived reader that must handle partial output; the wait-and-exit shape restarts cleanly); SQLite's update hook (only sees writes from its own connection, not other processes); a file-watch on the WAL (platform-specific).
- **Why:** Reads are cheap in WAL mode and never block writers, so a 200ms poll costs nothing that matters. One bounded call per wake keeps the watcher restart-proof: its only state is the cursor.
