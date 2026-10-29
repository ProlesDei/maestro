# 0241 What the ship/wave2 merges brought in

- **Context:** `ship/wave2` (int/p2a-p45, `81808f6`) merged `p2b/work` (track B), `p6/wake` and the 0700 design (`3e1f843`). `p6/wake` was described as decision 0600 only, but it sits on `p3/usage` and `scout/facts`, so merging it also brings `tools/maestro-usage/` and decisions 0120-0122 and 0140-0143.
- **Choice:** Merge it as is. Those branches were reviewed on their own (usage: GO with open minors; facts: no blockers or majors), and they would land with 0600 anyway. The `tools/maestro-ticket/README.md` conflict kept both sides: scout send-backs aren't tries (0432) and the dependencies and areas paragraph. `12ea057`'s ship-only tries check survives in track B's rewritten `applyTransition` (`TestScoutSendBackIsNotATry` passes). AGENTS.md keeps int's layout, adds `tools/maestro-usage/` next to `tools/maestro-ticket/`, and says the Go commands work in both modules.
- **Alternatives:** Cherry-pick only `e991ac1` (0600) and leave usage for a later merge (0600 cites 0140-0143, which would then be missing on this branch).
- **Why:** One merge of reviewed work keeps history honest and the decision links whole.
