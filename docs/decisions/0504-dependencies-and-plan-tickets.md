# 0504 Dependencies instead of Blocked; plan tickets close

- **Context:** G5.5 and G5.2: `plan-project` created waiting tickets as `blocked` and left the plan as a `blocked` tracking ticket that grooming closed as `done`. The brief puts dependencies in the store and Done with the Operations Manager.
- **Choice:** Waiting tickets get `--deps` and stay `todo` or `backlog`; claiming skips them until their dependencies are `done`. The `Plan:` ticket is an investigation owned by the Planner: it stands in for the workstream until the store has workstreams, ends with the spec as a `Report:`, and the Planner closes it as owner (0009). Workstream progress comes from events, not an open ticket. Grooming never sets `done`.
- **Alternatives:** Keep a tracking ticket open until the workstream finishes (Blocked misused, and someone other than Ops sets Done); have the Chief of Staff close plan tickets (puts it back at ticket level).
- **Why:** Blocked means "needs a decision" only, and every Done follows the ship or scout rule.
