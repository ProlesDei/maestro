---
name: review-change
description: Review a submitted change for correctness, security and maintainability, on the opposite model family to the author, and approve only when nothing must be fixed.
role: code-reviewer
trigger: new-work
claims: in_review
---

# Review a change

## Goal

Nothing that must be fixed reaches QA. Every finding is specific enough to fix without asking.

## When to use

An `in_review` ticket whose latest `Handoff:` has an `author-family` different from your model's family. Pick only those at intake. Never claim one you can't review on the opposite family; leave it for another reviewer.

## Steps

### 1. Read the change

- **Goal:** Know what the ticket asks and what changed.
- **Skill:** none
- **Tier:** Mid-tier, on the opposite family to the author
- **Done when:** The reply lists the acceptance criteria, the files changed, and the check results the engineer reported. The branch head matches the commit sha in the handoff note.
- **Evidence:** `maestro-ticket get <id> --json` version; `git diff <main branch>...maestro/<id>-<slug>` and `git log` exit codes; the branch head sha.

Branch missing, or head doesn't match the handoff: send it back without reviewing.

### 2. Review

- **Goal:** A complete list of findings.
- **Skill:** none
- **Tier:** Mid-tier, on the opposite family to the author
- **Done when:** The diff has been checked for:
  - Correctness: does what the ticket asks, edge cases, errors handled, tests prove each acceptance criterion.
  - Security: input handling, injection, auth and access, secrets, new dependencies.
  - Maintainability: matches the code around it, clear names, no drive-by changes, no dead code.
  
  Each finding is `must fix` or `suggestion`, with `file:line`, what's wrong, why, and a suggested fix.
- **Evidence:** the findings list; the files and lines read.

Read only. Never edit, commit or push, and don't fix it yourself.

### 3. Decide

- **Goal:** Approve, or request changes.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Approve if and only if there are no `must fix` findings. Otherwise, changes requested.
- **Evidence:** the count of `must fix` findings.

## Stop and block when

- The diff contains a secret, or a security problem that may already be live. Don't repeat the secret. Block with an `Escalation:` note saying where it is and how bad it is.
- The change needs a decision beyond the ticket (for example a new dependency or an API change no one approved): block with an `Escalation:` note.

## Wrap-up

- Status: approved → `in_qa`; changes requested → `in_progress` (the role stays the author's).
- Handoff note: `Handoff: approved` with any suggestions, or `Handoff: changes requested` with the numbered `must fix` list. Add your model family.
