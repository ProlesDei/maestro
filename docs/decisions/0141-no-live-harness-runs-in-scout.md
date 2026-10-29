# 0141 No live model calls while verifying harness facts

- **Context:** The JSON shapes in 0140 could have been confirmed by a real headless run on each harness, but that spends real usage, and the brief makes real headless runs Phase 3's done criterion.
- **Choice:** Verify with zero-token checks only: `--help`, the docs installed with each harness, `grok inspect`, binary strings and web docs. Leave live runs to Phase 3.
- **Alternatives:** Run one tiny `claude -p` and one `grok -p` with `--output-format json`.
- **Why:** The brief says to ask before spending real money, and Phase 3 records those runs anyway.
