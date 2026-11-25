---
name: maestro-prune
description: "Clean up a repo's workspace: stale worktrees, merged branches, dependency and build folders, temp files and folders, and Maestro leftovers, each with proof that nothing is lost, asking before anything that costs work to get back. Removes unused code only when there is nothing else to clean, or with --code. --safe-only runs one script that reads no board; a person or the outer layer schedules it. Use for /maestro-prune, 'clean up worktrees', 'free up disk space', 'delete merged branches', 'remove node_modules', or 'prune this repo'. Not for reshaping code that is still used (maestro-refactor-code) or cleaning up after your own change (maestro-working-principles)."
argument-hint: "[--dry-run] [--safe-only] [--code [path]]"
disable-model-invocation: true
---

# Prune

You clear out what no one needs, each with its proof. You never remove work that exists nowhere else, or anything a live session holds. Follow the **maestro-working-principles** skill. Below, `<main>` is the main branch the agent notes name, else the remote's default branch.

## Safe: the script

[scripts/prune-safe.sh](scripts/prune-safe.sh) reads no board. It fetches `origin` with pruning, then removes, never with force:

- worktrees on `maestro/*` branches whose head `origin/<main>` holds, with a clean status, no ignored files but [rebuildable](#rebuildable-folders) ones, no server ledger, no lock, no git activity in the last day and no process working in them. It keeps them all if `lsof` is missing, and never touches the main checkout or yours.
- local `maestro/*` branches checked out nowhere whose tip `origin/<main>` holds. Each `removed=` line gives the sha that brings it back.
- `maestro/evidence/pr-<n>/` in the git common dir once `gh` shows PR n merged or closed and nothing in it changed for an hour. Without `gh` it keeps them.

Pass `--dry-run` through, `--main <main>` when the agent notes name one, and any regenerable path the agent notes name. It prints `removed=<class> <path>`, `kept=<path> <label>`, `gone=<class> <path>` (another run got there first) and a `summary=` line. It exits 1 with a reason if the fetch or git fails, 2 on bad usage. Two runs at once are safe.

With `--safe-only`, run the script and report its output. Nothing else.

## Rebuildable folders

The names in `rebuildable` at the top of [scripts/prune-safe.sh](scripts/prune-safe.sh), plus any regenerable path the agent notes name. A finished worktree may lose these. Any other ignored file may hold secrets, local config or data, so it keeps its worktree.

## Interactive run

1. Note free disk space. Run the script.
2. Find the Ask first candidates below. Save each shared list once (ticket statuses and claims, the worktree list, process working folders) and test items against it, not one call per item.
3. Show them by class with proof, size and last change. For a class over 20 items, show its count, total size and first 10. For a worktree, show its server ledger and any status that kept it. Say how many of your `mktemp -d` folders exist and how old the oldest is. With `--dry-run`, stop here.
4. Ask, then remove what the user approves, one explicit path at a time: worktrees without force, never a glob or a git clean. Check each item again just before. If another session removed it first, say so. If a lock file blocks one, keep both.
5. If nothing sorted Ask first and the script removed nothing, or with `--code`, prune code as [references/code.md](references/code.md) says.

| Sort | What goes there |
|---|---|
| Ask first | worktrees and branches the script kept as `unmerged` whose tip a remote branch holds, or whose change `origin/<main>` already has as a squash merge; detached `qa-<id>-*` and `review-<id>-*` worktrees whose PR is not under review; evidence folders of tickets `done` or `cancelled` and of QA jobs with no ticket (`answer`, `repro`, `second-look`); rebuildable folders in the main checkout and kept worktrees; untracked `*.orig`, `*.rej`, `*.tmp`, `*.swp`, `*~` and `.DS_Store` files and empty folders outside worktrees; your own temp folders untouched for 7 days; `prunable` worktree entries, all at once and only if each passes these checks |
| Keep | anything a claimed ticket, a lock, a server ledger or a live process holds; anything changed in the last day, dirty, unpushed, or that a check could not prove; harness folders (`.claude/`, `.agents/`), `AGENTS.md`, `CLAUDE.md`, `.env*` and other secrets files, and every `*.lock` file |

A branch that plain delete refuses after a squash merge is the sole exception to the force-delete rule in **maestro-working-principles**: force it only after a yes, and report its sha so it can come back.

**Reply:** with `--safe-only`, the script's output. Otherwise:

```
Script:   <its summary= line, and its kept= lines grouped by label>
Removed:  <class>: <path> (<proof>)
Kept:     <path>: <labels or reason>
Freed:    <free space before> -> <after>
Code:     <PR url | "skipped: cleanup found work" | "nothing unused">
```
