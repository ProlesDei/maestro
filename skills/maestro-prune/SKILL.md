---
name: maestro-prune
description: "Clean up a repo's workspace: stale worktrees, merged branches, dependency and build folders, temp files and folders, and Maestro leftovers, each with proof that nothing is lost, asking before anything that costs work to get back. Removes unused code only when there is nothing else to clean, or with --code. Use for /maestro-prune, 'clean up worktrees', 'free up disk space', 'delete merged branches', 'remove node_modules', or 'prune this repo'. Not for reshaping code that is still used (maestro-refactor-code) or cleaning up after your own change (maestro-working-principles)."
argument-hint: "[--dry-run] [--safe-only] [--code [path]]"
disable-model-invocation: true
---

# Prune

You clear out what no one needs (worktrees, branches, rebuildable folders, temp files and folders, Maestro leftovers), each with its proof. You never remove work that exists nowhere else, or anything a live session holds. Follow the **maestro-working-principles** skill, skipping its startup prune check. Below, `<main>` is the main branch the agent notes name, else the remote's default branch (default `main`), and `<common>` is `$(git rev-parse --git-common-dir)/maestro`.

Except with `--dry-run`, take an exclusive OS file lock on `<common>/last-prune.lock` without waiting. Hold it for the whole run in a process tied to the session, so the OS releases it if the session ends. Leave the lock file in place. Skip if another session holds the lock. With `--safe-only`, check `<common>/last-prune` again under the lock and skip if it records a run less than 24 hours ago.

Run `git fetch --prune origin` (retry once if it fails on a lock) and `df -h .`. Merges land on the remote, never on local `<main>`, so every merge check below uses `origin/<main>`. Hundreds of sessions may share this repo, and its ticket board. Save each shared list once to a file in `S=$(mktemp -d)`, and test items against those files instead of making one call per item: every ticket's status and claim from the ticket board, `git worktree list --porcelain` and `lsof -d cwd -Fn`.

## Phase A: Find candidates (read only)

| Class | How to find it |
|---|---|
| Worktrees | each `git worktree list` entry other than the main checkout and your own, and entries marked `prunable` |
| Branches | local branches no worktree has checked out that are `merged` (Phase B), whose upstream `git branch -vv` shows `gone`, or whose ticket is `done` or `cancelled` |
| Maestro leftovers | `<common>/evidence/<id>/` for tickets the saved ticket list shows `done` or `cancelled`, and each `<common>/evidence/<job>/<run>/` of a QA job with no ticket (`answer`, `repro`, `second-look`) |
| Servers | each `<common>/servers/<holder>` ledger, one line per process with PID, start time, port, worktree and what it is |
| Rebuildable folders | `!!` lines of `git status --ignored --porcelain` named `node_modules/`, `.venv/`, `venv/`, `target/`, `dist/`, `build/`, `.next/`, `.turbo/`, `__pycache__/`, `.pytest_cache/`, `.mypy_cache/` or `coverage/`. No other `!!` line is a candidate: ignored files also hold secrets, local config, databases and worktrees |
| Temp files | `find . -path ./.git -prune -o -name node_modules -prune -o -type f \( -name '*.orig' -o -name '*.rej' -o -name '*.tmp' -o -name '*.swp' -o -name '*~' -o -name .DS_Store \) -print`, minus tracked files and anything inside a worktree from the saved list; empty folders from the same `find` with `-type d -empty` |
| Temp folders | `find "${TMPDIR:-/tmp}" -maxdepth 1 -name 'tmp.*' -type d -user "$(id -un)"` (what `mktemp -d` leaves) where `find <dir> -mmin -10080 -print -quit` prints nothing: untouched for 7 days |

## Phase B: Label and sort each candidate

- `in-use`: the ticket board shows its ticket claimed; `git worktree list` marks it `locked`; or the saved `lsof` list has a line that is `n<path>` or starts `n<path>/` (skip this test where `lsof` is missing).
- Servers are `in-use` if their holder still has any claim on the board or their ledger changed in the last 10 minutes. Mark the ledger `unverified` if you can't check its holder or any process. A process is gone if its PID is dead or its start time differs from the ledger.
- `dirty`: a worktree whose `git -C <path> status --porcelain` prints anything. `unpushed`: a worktree head or branch tip that isn't `merged` and that `git branch -r --contains <sha>` doesn't list. `recent`: `find <path> -name node_modules -prune -o -mmin -1440 -print -quit` prints something. `merged`: `git merge-base --is-ancestor <sha> origin/<main>` passes, or, after a squash merge, `git merge-tree --write-tree origin/<main> <sha> | head -1` prints `git rev-parse 'origin/<main>^{tree}'`. `unverified`: the fetch failed.

| Sort | What goes there |
|---|---|
| Safe | no keep label, and nothing lost: `merged` worktrees and branches, those of `cancelled` tickets (a remote branch holds the tip), `qa-<id>-*` and `review-<id>-*` worktrees whose ticket isn't `in_review`, `prunable` entries, server ledgers with all processes gone |
| Ask first | the rest with none of the keep labels: other pushed but unmerged worktrees and branches, evidence folders, rebuildable folders, temp files, empty folders, server ledgers with any live matching process, temp folders: the machine's temp folder holds other projects' and tools' folders too |
| Keep | anything `in-use`, `recent`, `dirty`, `unpushed` or `unverified`; harness folders (`.claude/`, `.agents/`), `AGENTS.md`, `CLAUDE.md`, `.env*` and other secrets files, and every `*.lock` file: a live git or tool call holds it |

With `--safe-only`, sort a worktree Safe only if it is `prunable` or its ticket is `done` or `cancelled`; move other Safe worktrees to Ask first, since a session with no ticket may still use a clean one. Also skip code candidates, even with `--code`. Otherwise, if no cleanup candidate sorts Safe or Ask first, or with `--code`, also find and sort code candidates, and remove them after Phase D, as [references/code.md](references/code.md) says.

## Phase C: Show the list

Show every candidate by sort and class, with its labels, proof (the merge check, the ticket status, the status output), size from `du -sh` and last change date. Show the lines of each server ledger. For a `dirty` worktree, show `git -C <path> status --short`. Say how many `tmp.*` folders you own and how old the oldest is, so stale ones show up even before they qualify. For a class over 20 items, show its count, total size and first 10, and write the full list to a file in `$S`. With `--dry-run`, stop here.

## Phase D: Remove

1. Remove safe items now. With `--safe-only`, never ask and list all ask-first items under `Waiting`. Otherwise, remove ask-first items only after the user says yes and list the rest under `Waiting`. Before each class, save the shared lists again. Before each removal, test the item's labels again with fresh per-item calls (its ticket on the board, `git -C <path> status --porcelain`) and skip it if its sort changed.
2. Worktrees: `git worktree remove <path>`, never `--force`, then `git worktree prune`. Files and folders: `rm -r <path>` or `rmdir <path>`, one explicit path at a time, never a glob or `git clean`. Another session can act between your test and your command: if one fails because the item is gone, count it as removed by someone else; if it fails on a `.lock` file, skip the item, list it under `Kept`, and never delete the lock.
3. Branches: `git branch -d` checks the current HEAD, and local `<main>` lags, so run it from a checkout of `origin/<main>`: `D=$(mktemp -d)`, `git worktree add --detach $D/main origin/<main>`, `git -C $D/main branch -d <branch>...` with every branch in one call, so the shared ref file is rewritten once, then `git worktree remove $D/main && rmdir $D`. Include the branch of each worktree removed in step 2 when that branch sorts Safe or got a yes. If `-d` refuses a squash-merged branch, `git branch -D <branch>`: the one `-D` this skill allows, an exception to **maestro-working-principles**, for no other branch. Report its sha, so `git branch <branch> <sha>` brings it back.
4. Servers: skip a ledger if it changed since Phase C. For an approved stop, recheck the start time against the ledger just before stopping each matching PID and its children. Remove the ledger only once every recorded process is gone.

When the run finishes, write the current time to `<common>/last-prune`, then release the lock.

```
Removed:  <class>: <path> (<proof>)
Kept:     <path>: <labels or reason>
Waiting:  <ask-first items still waiting on the user>
Freed:    <df -h . before> -> <after>
Code:     <PR url | "skipped: --safe-only" | "skipped: cleanup found work" | "nothing unused">
```

**Reply:** the report above, filled in.
