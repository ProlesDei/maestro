# 0741 How the engineer, review and QA playbooks changed on the way to YAML

- **Context:** Turning the 12 Markdown playbooks into steps an engine can follow (0700, 0740) forced a choice wherever a step said "decide", "skip to" or "escalate".
- **Choice:**
  - **Decisions are gates.** The Tries limits are gates before any tokens are spent: the Junior stops at `Tries` 2, the Senior at 3 above the `tries-at-takeover:` comment that take-escalation now posts. Review approves when `must_fix` is empty and the reported head equals the handoff's `sha:`. QA passes when the checks and `verify` both pass. A failed QA check skips `verify` (its `when:` records the skip) instead of stopping the run.
  - **Facts travel as trailer lines.** Every engineer handoff ends with `branch:`, `sha:` and `author-family:`; review's approval repeats `sha:` (the reviewed commit, which QA checks out) and adds `review-family:`; QA's pass repeats `sha:` (the tested commit, for release).
  - **Fewer steps.** Refactor's "prove the tests can fail" is part of the `characterise` step, as in the old skill. Scout's frame, investigate and report steps are one `investigate` prompt, and its `done` note is the `Report:` comment. Review's read and review steps are one `review` prompt.
  - **Hand-offs.** Everything the Junior can't finish, including an unclear ticket, goes to the Senior at `todo` (`block.to`); the Senior asks the owner if it needs to. Take-escalation always reuses the junior's branch: starting over reverts on it, so nothing is deleted and the worktree path stays the same.
  - **Sent-back work (0436).** Both `fix-sent-back` playbooks take every unleased `in_progress` ticket for their role, including one back from `blocked`, and `feedback` reads whatever came back. Scout and the verification guide take theirs through a second `intake` entry, skipping `start`.
  - **Golden tasks** name step ids, and expect a gate's stop as "no later step ran", not as `Skipped:` lines.
- **Alternatives:** A bot step for each decision (tokens and drift for a comparison); one playbook per ending (copies); a new branch when the Senior starts over (needs a second worktree path and a branch name from the model's reply).
- **Why:** Each choice keeps the old behaviour or makes it stricter, and moves every comparison out of a model's hands.
