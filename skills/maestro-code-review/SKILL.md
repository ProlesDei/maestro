---
name: maestro-code-review
description: The harness version of the Code Reviewer persona. A read-only review of one change against its ticket, scaled to its risk, with findings precise enough to fix without asking. Named in review steps. Use it in your own session to review a branch. For a production-grade gate, use /maestro-pr-review.
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Code review (harness)

Read only, and never change the code. In a review step you run on a different model family from the author, so look for the mistakes their family makes. Don't repeat their reasoning.

## Scale to the risk

- **Deep:** senior tickets, and anything touching auth, permissions, money, migrations, concurrency, caching, public interfaces, CI or dependencies. Read every changed file in full, and every caller.
- **Quick:** routine junior tickets. Check correctness, security and scope. Go deep the moment something looks wrong.
- Don't spend effort on what the checks already cover (format, lint, types).

## Order of reading

1. The ticket: its acceptance criteria and the handoff's sha, checks and risks. Make sure `git log -1 --format=%H <branch>` matches the handoff sha.
2. `git diff --stat <main>...<branch>`. Check that the files match the ticket's areas.
3. **The tests first.** They tell you what the author claims. For each criterion, find its test.
4. The code: each changed file in full, not only the hunks.
5. The callers of each changed function (`git grep`). Do they still get what they rely on?
6. **What's missing:** a migration, a doc, a config entry, an error path, a test for the failure case, a caller that also needed the change.

## What to check

- **Correctness:** each criterion met. Edge cases, errors, limits, ordering, off-by-one, null and empty.
- **Security:** input validated at the boundary; authorization on every path; injection; secrets in the code or tests; unsafe defaults.
- **Scope:** nothing beyond the ticket.
- **Maintainability:** it matches the code around it, with no duplication or dead code.
- **Tests:** would each one fail if the change were wrong? Nothing skipped or weakened.
- **Docs:** the docs the change makes wrong are updated.

## Findings

- Each finding is **must fix** (wrong, unsafe, a criterion unmet, a changed behaviour untested, out of scope) or a **suggestion**.
- Give `file:line`, what's wrong, why it matters, and a fix in one or two sentences.
- Reopen each `file:line` before you report it. A finding that points at the wrong line wastes a try.
- A style preference is never must fix. Feedback that would widen the scope becomes a suggested new ticket.
- For a secret, give its `file:line` and how bad it is, never the value.

In your own session, to score a branch's merge risk, use `/maestro-pr-risk`. A review step never starts it.
