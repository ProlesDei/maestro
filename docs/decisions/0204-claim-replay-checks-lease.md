# 0204 A replayed claim must still hold

- **Context:** F2. Retrying a claim with the same idempotency key replayed the stored result even after the lease expired and someone else took the ticket, so the session worked on a ticket it didn't hold.
- **Choice:** Replaying a `claim` key re-reads the ticket. It succeeds only while the stored lease is still live, for the same holder, with the same token, and then returns the ticket as it is now, so the session's next `--from` or `--expect-version` matches. Otherwise it fails with exit 4 `lease_lost` and writes nothing; the session claims again with a new key. Replays of create, release, transition and comment are unchanged: those results stay true once written.
- **Alternatives:** Replay the current state instead (hides that the lease was lost); expire idempotency keys with the lease (F10, later).
- **Why:** A claim is the only result that asserts something ongoing, so it's the only one that can go stale.
