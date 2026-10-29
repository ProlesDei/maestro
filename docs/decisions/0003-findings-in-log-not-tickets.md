# 0003 File Phase 0 findings in the decision log, not as tickets yet

- **Context:** Phase 0 is done when findings are "filed as tickets or fixed". Maestro has no ticket store of its own yet (dogfooding starts in Phase 8), and this run was audit-only.
- **Choice:** Record every finding in 0001 with an id, a severity and an owning phase. Each phase starts by turning its findings into tickets once at-ticket can be used on this repo, or fixes them directly.
- **Alternatives:** Create an at-ticket store for this repo now; open GitHub issues (blocked: no token, and outward-facing).
- **Why:** No new state outside git, nothing outward-facing, and each phase's done criteria already cover its findings. Phase 0 stays open until the findings are tickets.
