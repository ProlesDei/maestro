---
name: memory-curation
description: On a schedule, accept or reject memory proposals from the inbox, flag memories whose files changed, and archive the rest to stay under the size budget.
role: operations-manager
trigger: scheduled
claims: none
types: none
---

# Memory curation

## Goal

Project and role memory stays true, small and useful: every memory cites its evidence and the files it's about, stale ones are flagged, and the total stays under budget.

## When to use

Memory curation is due, per the schedule the bot runtime keeps.

## Steps

### 1. Wake gate

- **Goal:** Spend no tokens when there's nothing to curate.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `memory scan`
- **Done when:** Exit `0`. No proposals, no changed files, and under budget: end here, with no harness session.
- **Evidence:** proposal count; flagged memory count; size against budget.

### 2. Set up the memory worktree

- **Goal:** A branch for the memory change.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `worktree add memory-<date> --branch maestro/memory-<date> --from <main branch>`
- **Done when:** Exit `0`.
- **Evidence:** exit code; branch and path.

### 3. Judge the proposals

- **Goal:** Each proposal accepted, merged into an existing memory, or rejected.
- **Role:** harness
- **Skill:** `maestro-curate-memory`
- **Model:** Mid-tier
- **Skip when:** the inbox is empty.
- **Done when:** Each proposal has a verdict with the reason. Accepted ones are durable (a convention, pitfall, decision, codebase fact or role lesson), cite evidence, name their files, and say which tier they go in: project or role. One-offs and anything a reader could get from the code or git history are rejected. Rejected entries are removed from the inbox now; accepted and merged ones after step 6 commits them.
- **Evidence:** proposal paths; verdict and reason per proposal.

### 4. Recheck flagged memories

- **Goal:** Memories about changed files are still true, or are fixed or archived.
- **Role:** harness
- **Skill:** `maestro-curate-memory`
- **Model:** Mid-tier
- **Skip when:** `memory scan` flagged none.
- **Done when:** Each flagged memory was checked against the current files and kept (with its file checksums refreshed), corrected, or archived.
- **Evidence:** memory paths; the files rechecked; the verdict per memory.

### 5. Stay under budget

- **Goal:** The memory size is under the budget from preflight.
- **Role:** harness
- **Skill:** `maestro-curate-memory`
- **Model:** Mid-tier
- **Skip when:** already under budget after steps 3 and 4.
- **Done when:** The least-used and oldest memories are archived, not deleted, until under budget.
- **Evidence:** what was archived; size before and after.

### 6. Commit

- **Goal:** The curated memory is a reviewable change.
- **Role:** harness
- **Skill:** `maestro-curate-memory`
- **Model:** Mid-tier
- **Skip when:** steps 3 to 5 changed nothing.
- **Done when:** The changes to project and role memory (`.maestro/project/memory/`, role lessons in `roles/<role>.md`) are committed in the memory worktree. Then the inbox entries they came from are removed; the inbox is never committed.
- **Evidence:** commit sha; files changed.

### 7. Open the pull request

- **Goal:** A human can review and merge the memory change.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `open pr --branch maestro/memory-<date>`
- **Skip when:** step 6 was skipped.
- **Done when:** Exit `0` with the pull request URL.
- **Evidence:** exit code; the URL.

## Stop and block when

- A proposal or memory contains a secret: reject it without repeating the value, and say where it was in the log.
- `memory scan` fails: report the exit code; change nothing.

## Rules

- Memory never holds secrets, ticket chatter or copies of the code.
- Harness auto-memory stays off or points at the curated memory. Never accept a memory straight from a session.

## Wrap-up

- Skill: `maestro-log-entry` (nothing claimed, no status change). Log the counts: accepted, merged, rejected, archived, and any `Skipped:` steps.
- Handoff note: none; the pull request is the handoff.
