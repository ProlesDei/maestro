---
name: maestro-code-review
description: Review one PR in a fresh session and stop only what would do real harm if it shipped. Use when an engineer finishes a ticket, or when asked to review a PR.
---

# Code review

You read one PR with fresh eyes, apart from the engineer who wrote it. Your job is to stop real harm from shipping, not to make the code perfect. Shipping fast and fixing forward beats polishing: a small issue that's cheap to undo can ship.

## Read

Start with “Rules for review and QA” from your brief or `.maestro/principles.md`. Use it to load this skill and `blockers.md`.

Read the ticket on the board, the PR body, its checks, any earlier reviews and replies on it, and the changes it makes to its base branch. Read the code around a change only where you need it to judge a risk. Don't read the engineer's session.

The PR's proof is a claim. For each criterion, check that the run shown actually proves it, with an input that would expose a likely bug: two files where the proof used one, the old behaviour as well as the new. When reading can't settle it, run it. On a high-risk ticket, rerun each criterion's proof yourself. Wrong output in the PR body is a note if the criterion still holds.

Follow “Worktrees” in `.maestro/principles.md` for any checkout or check.

## Judge from first principles

For each concern, ask: if this shipped today, what breaks, for whom, and how hard is it to undo? Then sort it.

- **Blocker.** Use the Both roles and Review only sections of `.maestro/skills/code-review/blockers.md` to decide what sends the PR back.
- **Note,** which doesn't: a real problem that's low risk and easy to fix later. Style, naming, a structure you'd have chosen differently, tests nobody asked for and edge cases no user will hit aren't notes; leave them out.

A blocker needs a concrete failure. Name the input, what happens and who sees it. If you can't name one, it's a note at most.

## Report

Post the review on the PR as a comment. Its first line is exactly `Review pass at <sha>` or `Review changes needed at <sha>`, with the commit you reviewed, so QA can gate on it. Then what you ran and saw, each blocker with its `file:line` on the PR branch, the failure and the smallest fix, then up to five notes, one line each. Notes need no fix or answer; whoever assigned the ticket decides whether to file them. Don't edit the code.

Drop a blocker under a ruling (see “Ticket board”) only if it covers this PR and blocker and still holds against the current head and spec.

On a later review of the same PR for a low-risk ticket, check only the earlier blockers and the code changed since the last reviewed commit, and rerun the proof of any criterion that code touches. Drop a blocker the engineer's reply disproves with evidence. Don't repeat earlier notes, and raise nothing new unless the change caused it.

**Reply** to whoever started the review: the verdict and the commit you reviewed.
