---
name: maestro-settle-dispute
description: Decide whether a disputed review finding needs a fix. Use when an engineer and reviewer disagree.
---

# Settle a dispute

Read the finding and the engineer's reply. Check both against the ticket and the evidence. Run the relevant check if the evidence leaves the result unclear.

Send the change back only if the finding blocks acceptance or makes the change unsafe to merge. Tell both agents what you decided and why. If the spec caused the dispute, use `maestro-update-ticket`. If you need a decision beyond the ticket, use `maestro-unblock`.
