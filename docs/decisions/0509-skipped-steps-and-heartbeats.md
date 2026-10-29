# 0509 Skipped steps are recorded; steps never heartbeat

- **Context:** G5.4 found silent skips; G4.2 found playbooks telling turns to heartbeat. The brief wants skipped steps kept with a reason, and heartbeats from a background process.
- **Choice:** A step may be skipped only by its own `Skip when:` line or by a jump the playbook names, and each skip is written as `Skipped: <step> — <reason>` in the scratchpad and the handoff. Every golden task's Expected lists the skips. The playbooks README says heartbeats come from the claim's background process; no step mentions them.
- **Alternatives:** Log skips only in the scratchpad (deleted at clean-up, so lost).
- **Why:** The handoff survives, so the next role and the evals can see what didn't run and why.
