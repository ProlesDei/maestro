# 0201 The store counts tries

- **Context:** F5. The brief's retry rule (Junior escalates after 2, Senior blocks after 3 more) needs a Tries number. It says send-backs from review or QA count and failed automated checks don't.
- **Choice:** `tries` goes up by one on `in_review → in_progress` and `in_qa → in_progress`, in the same write as the move, and the event records the new count. Nothing else changes it: not `ready_to_release → in_progress`, escalation, unblocking or re-claims. The store only counts; the escalate and block thresholds stay in playbooks.
- **Alternatives:** A `--try` flag set by the caller (can be forgotten); count `ready_to_release → in_progress` too (that's a release problem, not a review or QA send-back).
- **Why:** Counting from the move itself can't drift from what happened.
