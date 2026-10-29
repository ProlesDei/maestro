# 0405 Skills assume the claim's background heartbeat

- **Context:** G4.2: `prompt-contract` told sessions to run `maestro-ticket heartbeat` during long steps. The brief says heartbeats come from a background process started by the claim, which Phase 2 builds.
- **Choice:** No skill or prompt runs `heartbeat`, and session permissions deny it (`docs/runner.md`). `maestro-claim` says the claim keeps the lease alive, and reports when the claim JSON shows no background heartbeat, so the bot can use a TTL longer than the playbook's longest step until Phase 2 lands. A write that exits `4` for a lost lease stops the session.
- **Alternatives:** Keep LLM heartbeats until Phase 2 (a single step can outlast a lease anyway, and the turn can't heartbeat mid-step); have the runner heartbeat (the brief puts it in the claim).
- **Why:** The skills match the settled design now, with a safe fallback for the current CLI.
