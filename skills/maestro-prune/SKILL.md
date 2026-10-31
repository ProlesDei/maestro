---
name: maestro-prune
description: "Clean up a repo's workspace: stale worktrees, merged branches, dependency and build folders, temp files and Maestro leftovers, each with proof that nothing is lost, asking before anything that costs work to get back. Removes unused code only when there is nothing else to clean, or with --code. Use for /maestro-prune, 'clean up worktrees', 'free up disk space', 'delete merged branches', 'remove node_modules', or 'prune this repo'. Not for reshaping code that is still used (maestro-refactor-code) or cleaning up after your own change (maestro-working-principles)."
argument-hint: "[--dry-run] [--code [path]]"
disable-model-invocation: true
---

# Prune

You clear out what no one needs (worktrees, branches, rebuildable folders, temp files, Maestro leftovers), each with its proof. You never remove work that exists nowhere else, or anything a live session holds. Follow the **maestro-working-principles** skill. Below, `<main>` is the branch under `## Main branch` in `.maestro/project/project-setup.md` (default `main`), `<common>` is `$(git rev-parse --git-common-dir)/maestro`, and ticket commands are `maestro-ticket`'s; if `## Tickets` there names another tool, use its equivalent.

## Start

Open a todo list with one item per phase below. From the main checkout, run `git fetch --prune origin` and `df -h .`. Merges land on the remote, never on local `<main>`, so every merge check below uses `origin/<main>`.

## Phase A: Find candidates (read only)

| Class | How to find it |
|---|---|
| Worktrees | each `git worktree list` entry other than the main checkout and your own, and entries marked `prunable` |
| Branches | local branches no worktree has checked out, whose tip `git merge-base --is-ancestor <branch> origin/<main>` passes or whose upstream `git branch -vv` shows `gone` |
| Maestro leftovers | `<common>/scratch/*/<id>.md` and `<common>/evidence/<id>/` for tickets `maestro-ticket get <id> --json` shows `done` or `cancelled` |
| Rebuildable folders | `!!` lines of `git status --ignored --porcelain` named `node_modules/`, `.venv/`, `venv/`, `target/`, `dist/`, `build/`, `.next/`, `.turbo/`, `__pycache__/`, `.pytest_cache/`, `.mypy_cache/` or `coverage/`. No other `!!` line is a candidate: ignored files also hold secrets, local config, databases and `.maestro/worktrees/` |
| Temp files | `find . -path ./.git -prune -o -path ./.maestro/worktrees -prune -o -name node_modules -prune -o -type f \( -name '*.orig' -o -name '*.rej' -o -name '*.tmp' -o -name '*.swp' -o -name '*~' -o -name .DS_Store \) -print`, minus tracked files; empty folders from the same `find` with `-type d -empty` |

## Phase B: Label and sort each candidate

- `in-use`: its ticket's `get --json` shows a `claim` with `expired: false`; `grep -lF <path> <common>/runs/*.json` names it; or `lsof -d cwd -Fn | grep -F n<path>` finds a process inside it (skip this test where `lsof` is missing).
- `dirty`: a worktree whose `git -C <path> status --porcelain` prints anything. `unpushed`: a worktree head or branch tip `git branch -r --contains <sha>` doesn't list.
- `recent`: `find <path> -name node_modules -prune -o -mmin -1440 -print -quit` prints something. `merged`: `git merge-base --is-ancestor <sha> origin/<main>` passes. `unverified`: the fetch failed.

| Sort | What goes there |
|---|---|
| Safe | no keep label, and nothing lost: `merged` worktrees and branches, `qa-<id>` and `review-<id>-*` worktrees whose ticket isn't `in_review`, scratch files, `prunable` entries |
| Ask first | the rest with none of the keep labels: pushed but unmerged worktrees and branches (`cancelled` tickets, upstream `gone`), evidence folders, rebuildable folders, temp files, empty folders |
| Keep | anything `in-use`, `recent`, `dirty`, `unpushed` or `unverified`; `.maestro/project/`, `.maestro/worktrees/` itself, harness folders (`.claude/`, `.agents/`, `.grok/`), `AGENTS.md`, `CLAUDE.md`, `.env*` and other secrets files, `<common>/tickets.db*`, `<common>/backups/`, `<common>/runs/`, `<common>/STOP` |

If no cleanup candidate sorts Safe or Ask first, or with `--code`, also find and sort code candidates, and remove them after Phase D, as [references/code.md](references/code.md) says.

## Phase C: Show the list

Show every candidate by sort and class, with its labels, proof (the merge check, the ticket status, the status output), size from `du -sh` and last change date. For a `dirty` worktree, show `git -C <path> status --short`. With `--dry-run`, stop here.

## Phase D: Remove

1. Remove safe items now, ask-first items only after the user says yes; with no one to answer (`claude -p`), list those under `Waiting`. Before each removal, test its labels again and skip it if its sort changed.
2. Worktrees: `git worktree remove <path>`, never `--force`, and delete its branch in step 3 if that branch is safe too. Then `git worktree prune`.
3. Branches: `git branch -d` checks the current HEAD, and local `<main>` lags, so run it from a checkout of `origin/<main>`: `D=$(mktemp -d)`, `git worktree add --detach $D/main origin/<main>`, `git -C $D/main branch -d <branch>` for each, then `git worktree remove $D/main && rmdir $D`. Never `branch -D`.
4. Files and folders: `rm -r <path>` or `rmdir <path>`, one explicit path at a time, never a glob or `git clean`.

```
Removed:  <class>: <path> (<proof>)
Kept:     <path>: <labels or reason>
Waiting:  <ask-first items still waiting on the user>
Freed:    <df -h . before> -> <after>
Code:     <PR url | "skipped: cleanup found work" | "nothing unused">
```

**Reply:** the report above, filled in.
