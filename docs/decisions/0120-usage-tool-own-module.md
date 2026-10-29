# 0120 maestro-usage is its own module with its own database

- **Context:** The brief says to port the usage prototype "under `tools/`, behind the store interface", but not whether it joins the ticket module or the ticket database.
- **Choice:** A separate module, `tools/maestro-usage`, with its own `usage.Store` interface and its own SQLite file (`<git common dir>/maestro/usage.db`, override `MAESTRO_USAGE_DB`). Same driver and versions as maestro-ticket (modernc.org/sqlite v1.61.0), so no new dependencies. Timestamps are unix milliseconds rather than the prototype's float seconds.
- **Alternatives:** Add a `usage` table to the ticket store (couples two schemas mid-hardening, and Phase 2 is reworking that store); share one module with `tools/maestro-ticket` (it was mid-rename in Phase 1 when this was decided).
- **Why:** Phases 2 and 3 run in parallel without touching each other's code, and usage writes never contend with ticket claims for the write lock. Merging them later is one `Store` implementation.
