# 0502 How Tries is counted and acted on

- **Context:** G5.1. The brief: review or QA send-backs add one to Tries; Junior escalates after 2; Senior blocks after 3 more; failed checks don't count. It doesn't say who increments, what "3 more" counts from, or what bounds failing checks.
- **Choice:**
  - The reviewer's or QA's wrap-up adds the try (`add_try` on the move back to `in_progress`). A branch missing or a head mismatch is still a review send-back, so it counts. An owner sending a scout report back doesn't count.
  - Failing checks loop back to the build step, bounded by the step budget from `.maestro/environment.md`. Junior out of budget hands to the Senior; Senior out of budget blocks to the owner.
  - Junior: a ticket coming back with Tries at 2 goes to the Senior without another attempt.
  - Senior: the takeover records `tries-at-takeover` (0 on the senior's own tickets); a ticket coming back with Tries 3 above it is blocked with an `Escalation:` to the owner.
- **Alternatives:** Count the Senior's 3 from Tries 0 (a ticket the Junior escalated at 2 would get only one senior try); a per-role counter in the store (needs schema work for little gain).
- **Why:** Matches the brief's numbers exactly, needs one counter, and the takeover note makes "3 more" checkable.
