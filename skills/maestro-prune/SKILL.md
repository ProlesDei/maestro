---
name: maestro-prune
description: "Clean up a repo's workspace: stale worktrees, merged branches, dependency and build folders, temp files and folders, and Maestro leftovers, each with proof that nothing is lost, asking before anything that costs work to get back. Removes unused code only when there is nothing else to clean, or with --code. Use for /maestro-prune, 'clean up worktrees', 'free up disk space', 'delete merged branches', 'remove node_modules', or 'prune this repo'. Not for reshaping code that is still used (maestro-refactor-code) or cleaning up after your own change (maestro-working-principles)."
argument-hint: "[--dry-run] [--code [path]]"
disable-model-invocation: true
---

# Prune

You clear out what no one needs (worktrees, branches, rebuildable folders, temp files and folders, Maestro leftovers), each with its proof. You never remove work that exists nowhere else, or anything a live session holds. Follow the **maestro-working-principles** skill. Below, `<main>` is the branch under `## Main branch` in `.maestro/project/project-setup.md` (default `main`), and `<common>` is `$(git rev-parse --git-common-dir)/maestro`.

## Start

Open a todo list with one item per phase below. From the main checkout, run `git fetch --prune origin` (retry once if it fails on a lock) and `df -h .`. Merges land on the remote, never on local `<main>`, so every merge check below uses `origin/<main>`. Hundreds of sessions may share this repo, and its ticket store. Save each shared list once to a file in `S=$(mktemp -d)`, and test items against those files instead of making one call per item: `maestro-ticket list --json`, `git worktree list --porcelain` and `lsof -d cwd -Fn`.

## Phase A: Find candidates (read only)

| Class | How to find it |
|---|---|
| Worktrees | each `git worktree list` entry other than the main checkout and your own, and entries marked `prunable` |
| Branches | local branches no worktree has checked out that are `merged` (Phase B), whose upstream `git branch -vv` shows `gone`, or whose ticket is `done` or `cancelled` |
| Maestro leftovers | `<common>/scratch/*/<id>.md` and `<common>/evidence/<id>/` for tickets the saved ticket list shows `done` or `cancelled` |
| Rebuildable folders | `!!` lines of `git status --ignored --porcelain` named `node_modules/`, `.venv/`, `venv/`, `target/`, `dist/`, `build/`, `.next/`, `.turbo/`, `__pycache__/`, `.pytest_cache/`, `.mypy_cache/` or `coverage/`. No other `!!` line is a candidate: ignored files also hold secrets, local config, databases and `.maestro/worktrees/` |
| Temp files | `find . -path ./.git -prune -o -path ./.maestro/worktrees -prune -o -name node_modules -prune -o -type f \( -name '*.orig' -o -name '*.rej' -o -name '*.tmp' -o -name '*.swp' -o -name '*~' -o -name .DS_Store \) -print`, minus tracked files; empty folders from the same `find` with `-type d -empty` |
| Temp folders | `find "${TMPDIR:-/tmp}" -maxdepth 1 -name 'tmp.*' -type d -user "$(id -un)"` (what `mktemp -d` leaves) where `find <dir> -mmin -10080 -print -quit` prints nothing: untouched for 7 days |

## Phase B: Label and sort each candidate

- `in-use`: its ticket shows a `claim` with `expired: false`; `git worktree list` marks it `locked`; or the saved `lsof` list has a line that is `n<path>` or starts `n<path>/` (skip this test where `lsof` is missing).
- `dirty`: a worktree whose `git -C <path> status --porcelain` prints anything. `unpushed`: a worktree head or branch tip that isn't `merged` and that `git branch -r --contains <sha>` doesn't list. `recent`: `find <path> -name node_modules -prune -o -mmin -1440 -print -quit` prints something. `merged`: `git merge-base --is-ancestor <sha> origin/<main>` passes, or, after a squash merge, `git merge-tree --write-tree origin/<main> <sha> | head -1` prints `git rev-parse 'origin/<main>^{tree}'`. `unverified`: the fetch failed.

| Sort | What goes there |
|---|---|
| Safe | no keep label, and nothing lost: `merged` worktrees and branches, those of `cancelled` tickets (a remote branch holds the tip), `qa-<id>` and `review-<id>-*` worktrees whose ticket isn't `in_review`, scratch files, temp folders with no `.git` inside, `prunable` entries |
| Ask first | the rest with none of the keep labels: other pushed but unmerged worktrees and branches, evidence folders, rebuildable folders, temp files, empty folders, temp folders holding a `.git` |
| Keep | anything `in-use`, `recent`, `dirty`, `unpushed` or `unverified`; `.maestro/project/`, `.maestro/worktrees/` itself, harness folders (`.claude/`, `.agents/`, `.grok/`), `AGENTS.md`, `CLAUDE.md`, `.env*` and other secrets files, `<common>/tickets.db*`, `<common>/backups/`, `<common>/STOP`, and every `*.lock` file: a live git or tool call holds it |

If no cleanup candidate sorts Safe or Ask first, or with `--code`, also find and sort code candidates, and remove them after Phase D, as [references/code.md](references/code.md) says.

## Phase C: Show the list

Show every candidate by sort and class, with its labels, proof (the merge check, the ticket status, the status output), size from `du -sh` and last change date. For a `dirty` worktree, show `git -C <path> status --short`. Say how many `tmp.*` folders you own and how old the oldest is, so stale ones show up even before they qualify. For a class over 20 items, show its count, total size and first 10, and write the full list to a file in `$S`. With `--dry-run`, stop here.

## Phase D: Remove

1. Remove safe items now, ask-first items only after the user says yes; with no one to answer (`claude -p`), list those under `Waiting`. Before each class, save the shared lists again. Before each removal, check the kill switch, then test the item's labels again with fresh per-item calls (its ticket's `get --json`, `git -C <path> status --porcelain`) and skip it if its sort changed.
2. Worktrees: `git worktree remove <path>`, never `--force`, then `git worktree prune`. Files and folders: `rm -r <path>` or `rmdir <path>`, one explicit path at a time, never a glob or `git clean`. Another session can act between your test and your command: if one fails because the item is gone, count it as removed by someone else; if it fails on a `.lock` file, skip the item, list it under `Kept`, and never delete the lock.
3. Branches: `git branch -d` checks the current HEAD, and local `<main>` lags, so run it from a checkout of `origin/<main>`: `D=$(mktemp -d)`, `git worktree add --detach $D/main origin/<main>`, `git -C $D/main branch -d <branch>...` with every branch in one call, so the shared ref file is rewritten once, then `git worktree remove $D/main && rmdir $D`. Include the branch of each worktree removed in step 2 when that branch sorts Safe or got a yes. If `-d` refuses a squash-merged branch, `git branch -D <branch>`: the one `-D` this skill allows, an exception to **maestro-working-principles**, for no other branch. Report its sha, so `git branch <branch> <sha>` brings it back.

```
Removed:  <class>: <path> (<proof>)
Kept:     <path>: <labels or reason>
Waiting:  <ask-first items still waiting on the user>
Freed:    <df -h . before> -> <after>
Code:     <PR url | "skipped: cleanup found work" | "nothing unused">
```

**Reply:** the report above, filled in.
