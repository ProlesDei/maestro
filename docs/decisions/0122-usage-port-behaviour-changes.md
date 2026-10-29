# 0122 Where the Go port differs from the prototype

- **Context:** The prototype is the reference, but a few of its behaviours are unsafe for a gate that bots trust.
- **Choice:**
  - Usage errors exit 64 and internal errors 70. The prototype's argparse exits 2 on a typo, which reads as "over budget".
  - Budgets are strict: unknown fields, bad limit keys (`usd_24x`), negative limits or prices and a missing `--budgets`/`MAESTRO_BUDGETS` path are errors. `record --manual` rejects negative tokens and costs. A zero limit sets no `usd_remaining` cap (the prototype reported 0, which would stop every session). A missing default project file still means no limits.
  - `run` opens the db and budgets before starting the harness, streams output as it arrives instead of after exit, forwards Ctrl-C and SIGTERM, records a row when the harness binary can't start (exit 127, `unparsed`), and fills `duration_ms` from the wall clock when the harness doesn't report one. If saving fails, it warns on stderr and still exits with the harness's code.
  - Claude Code's `--verbose` JSON array is parsed. When several models tie on cost, the one with the most tokens, then the first by name, is named, since Go maps have no order.
- **Kept:** one row per `run`, output-hash keys for `record`, a zero limit means no limit, `unparsed` counts as a breaker failure.
- **Alternatives:** A byte-for-byte port.
- **Why:** A gate that fails open on a typo, or silently drops a budget, is worse than none.
