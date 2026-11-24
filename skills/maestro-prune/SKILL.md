---
name: maestro-prune
description: "Clean up a repo's workspace: stale worktrees, merged branches, dependency and build folders, temp files and folders, and Maestro leftovers, each with proof that nothing is lost, asking before anything that costs work to get back. Removes unused code only when there is nothing else to clean, or with --code. Use for /maestro-prune, 'clean up worktrees', 'free up disk space', 'delete merged branches', 'remove node_modules', or 'prune this repo'. Not for reshaping code that is still used (maestro-refactor-code) or cleaning up after your own change (maestro-working-principles)."
argument-hint: "[--dry-run] [--safe-only] [--code [path]]"
disable-model-invocation: true
---

# Prune

You clear out what no one needs (worktrees, branches, rebuildable folders, temp files and folders, Maestro leftovers), each with its proof. You never remove work that exists nowhere else, or anything a live session holds. Follow the **maestro-working-principles** skill. Below, `<main>` is the main branch the agent notes name, else the remote's default branch (default `main`), and `<common>` is the absolute git common dir plus `/maestro`.

The planner alone starts unattended runs. It owns the lock and stamp for those `--safe-only` runs. An interactive run uses neither.

Fetch from `origin` in every mode with remote ref pruning on (`--prune`) and tag pruning off (`--no-prune-tags`). Retry once if the fetch fails on a lock. If it still fails in `--safe-only`, report failure and stop. Run `df -h .`. Merges land on the remote, so every merge check below uses `origin/<main>`. Save each shared list once to a file in `S=$(mktemp -d)`, and test items against those files instead of making one call per item: each ticket's status and claim, `git worktree list --porcelain` and `lsof -d cwd -Fn`.

## Phase A: Find candidates (read only)

| Class | How to find it |
|---|---|
| Worktrees | each `git worktree list` entry other than the main checkout and your own, and entries marked `prunable` |
| Branches | local branches no worktree has checked out that are `merged` (Phase B), whose upstream `git branch -vv` shows `gone`, or whose ticket is `done` or `cancelled` |
| Maestro leftovers | `<common>/evidence/<id>/` for tickets the saved ticket list shows `done` or `cancelled`, and each `<common>/evidence/<job>/<run>/` of a QA job with no ticket (`answer`, `repro`, `second-look`) |
| Rebuildable folders | `!!` lines of `git status --ignored --porcelain` named `node_modules/`, `.venv/`, `venv/`, `target/`, `dist/`, `build/`, `.next/`, `.turbo/`, `__pycache__/`, `.pytest_cache/`, `.mypy_cache/` or `coverage/`. No other `!!` line is a candidate: ignored files also hold secrets, local config, databases and worktrees |
| Temp files | `find . -path ./.git -prune -o -name node_modules -prune -o -type f \( -name '*.orig' -o -name '*.rej' -o -name '*.tmp' -o -name '*.swp' -o -name '*~' -o -name .DS_Store \) -print`, minus tracked files and anything inside a worktree from the saved list; empty folders from the same `find` with `-type d -empty` |
| Temp folders | `find "${TMPDIR:-/tmp}" -maxdepth 1 -name 'tmp.*' -type d -user "$(id -un)"` (what `mktemp -d` leaves) where `find <dir> -mmin -10080 -print -quit` prints nothing: untouched for 7 days |

## Phase B: Label and sort each candidate

- `in-use`: the ticket board shows its ticket claimed, `git worktree list` marks it `locked`, the worktree has a non-empty `maestro/servers` ledger in its own git dir, or the saved `lsof` list has a line that is `n<path>` or starts `n<path>/`. With `--safe-only`, keep every worktree if `lsof` is missing or you can't check live use.
- `dirty`: a worktree whose `git -C <path> status --porcelain` prints anything, or whose ignored paths include anything outside the rebuildable folders in Phase A. `unpushed`: a worktree head or branch tip that isn't `merged` and that `git branch -r --contains <sha>` doesn't list. `recent`: `find <path> -name node_modules -prune -o -mmin -1440 -print -quit` prints something. `merged`: `git merge-base --is-ancestor <sha> origin/<main>` passes. In interactive runs, a squash merge also counts if `git merge-tree --write-tree origin/<main> <sha> | head -1` prints `git rev-parse 'origin/<main>^{tree}'`. `unverified`: a check needed for removal failed or could not run.

| Sort | What goes there |
|---|---|
| Safe | no keep label, and nothing lost: `merged` worktrees and branches, those of `cancelled` tickets (a remote branch holds the tip), `qa-<id>-*` and `review-<id>-*` worktrees whose ticket isn't `in_review`, `prunable` entries |
| Ask first | the rest with none of the keep labels: other pushed but unmerged worktrees and branches, evidence folders, rebuildable folders, temp files, empty folders and temp folders |
| Keep | anything `in-use`, `recent`, `dirty`, `unpushed` or `unverified`; harness folders (`.claude/`, `.agents/`), `AGENTS.md`, `CLAUDE.md`, `.env*` and other secrets files, and every `*.lock` file: a live git or tool call holds it |

With `--safe-only`, Safe is limited to two groups with no keep label:

- Local `maestro/*` branches checked out nowhere whose tips are ancestors of `origin/<main>` and that plain branch deletion accepts as merged.
- Worktrees on `maestro/*` branches or with `qa-` or `review-` run names, with a `done` or `cancelled` ticket. Keep `prunable` entries.

Put every other candidate under `Waiting`. Skip code candidates, even with `--code`. In interactive runs, if no cleanup candidate sorts Safe or Ask first, or with `--code`, also find and sort code candidates and remove them after Phase D, as [references/code.md](references/code.md) says.

## Phase C: Show the list

For `--safe-only`, show the Safe candidates with proof and report `Waiting` as counts by class with at most 10 paths in all. Otherwise, show candidates by sort and class, with labels, proof, size from `du -sh` and last change date. For a class over 20 items, show its count, total size and first 10, and write the full list to `$S`. Say how many `tmp.*` folders you own and how old the oldest is. When you show a worktree, include its server ledger lines and any status output that kept it. With `--dry-run`, stop here.

## Phase D: Remove

1. Remove Safe items. Only in interactive runs, ask before removing Ask first items. List the rest under `Waiting`. Before each class, save the shared lists again. Before each removal, check the item's labels again with fresh per-item data and skip it if its sort changed.
2. Worktrees: `git worktree remove <path>` without `--force`. Use `git worktree prune` only in interactive runs and only if every `prunable` entry passes the removal checks. Leave server ledgers and their processes alone. Files and folders: `rm -r <path>` or `rmdir <path>`, one explicit path at a time, never a glob or `git clean`. Skip paths that contain a listed worktree. If another session removed an item first, report that. If a `.lock` blocks removal, keep the item and the lock.
3. Branches: `git branch -d` can check the current HEAD, and local `<main>` lags, so run it from a checkout of `origin/<main>`: `D=$(mktemp -d)`, `git worktree add --detach $D/main origin/<main>`, `git -C $D/main branch -d <branch>...` with every branch in one call, then `git worktree remove $D/main && rmdir $D`. Check the branch of each removed worktree against the same rules. In `--safe-only`, put any branch that `-d` refuses under `Waiting`. Only in interactive runs, if `-d` refuses a squash-merged branch, use `git branch -D <branch>`. This is the sole exception to the force-delete rule in **maestro-working-principles**. Report its sha so `git branch <branch> <sha>` brings it back.

```
Removed:  <class>: <path> (<proof>)
Kept:     <path>: <labels or reason>
Waiting:  <items waiting, in the Phase C format>
Freed:    <df -h . before> -> <after>
Code:     <PR url | "skipped: --safe-only" | "skipped: cleanup found work" | "nothing unused">
```

**Reply:** the report above, filled in.
