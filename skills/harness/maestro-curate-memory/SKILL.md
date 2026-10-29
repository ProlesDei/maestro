---
name: maestro-curate-memory
description: Judge memory proposals, recheck memories whose files changed, archive to stay under budget, and commit project and role memory; use it in memory curation.
disable-model-invocation: true
metadata:
  internal: true
---

# Curate memory

You decide what the team remembers. Memory must be true, small, and cite its evidence and the files it's about.

## Inputs

- The `maestro/memory-<date>` worktree.
- The `memory scan` output: proposals, flagged memories, size and budget.
- Which part to do: judge, recheck, budget or commit.

## Steps

Memory lives in `.maestro/project/memory/` in the worktree: project memories by kind, role lessons in `roles/<role>.md`, retired ones in `archive/`. The inbox is `$(git rev-parse --git-common-dir)/maestro/memory-inbox/`.

1. **Judge** each proposal: accept, merge into an existing memory, or reject, with the reason. Accept only durable facts (a convention, pitfall, decision, command, codebase fact or role lesson) that cite evidence, name their `about` files and aren't already there. Reject one-offs, ticket chatter, copies of code, anything a reader could get from the code or git history, and anything holding a secret. Write accepted ones with `about`, `source`, `evidence` and a `git hash-object` checksum per `about` file. Delete rejected inbox files.
2. **Recheck** each flagged memory against its `about` files now: still true, refresh the checksums; partly wrong, correct it; no longer true, move it to `archive/`.
3. **Budget:** move the least-used and oldest memories to `archive/` until the size is under budget. Never delete one.
4. **Commit** the changes in the worktree, with the counts in the message. Then delete the inbox files of the accepted and merged proposals.

## Reply

```yaml
status: done
summary: 3 accepted, 1 merged, 2 rejected, 1 archived
evidence:
  - commit 77c0a1e on maestro/memory-2026-10-28
next: open the pull request
questions: []
```

## Rules

- Write only in `.maestro/project/memory/` in the memory worktree, and delete only inbox files you handled.
- Never repeat a secret. Say which proposal held one.
