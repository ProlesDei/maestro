---
name: maestro-prune
description: "Find what nothing uses, prove it, list it by class, and remove it so a revert brings it back, asking before anything that can't be recovered. Use for /maestro-prune, 'prune this repo', 'remove dead code', 'delete unused files', or 'clear out stale branches and worktrees'. Not for reshaping code that is still used (maestro-refactor-code) or cleaning up after your own change (maestro-working-principles)."
argument-hint: "[path] [--dry-run]"
disable-model-invocation: true
---

# Prune

You remove what nothing uses, each item with its proof, so a revert brings it back. You never remove what can't be recovered without the user's yes. `[path]` limits the file and code classes; it defaults to the repo root. Follow the **maestro-working-principles** skill. Below, `<main>` is the branch under `## Main branch` in `.maestro/project/project-setup.md` (default `main`), `<common>` is `$(git rev-parse --git-common-dir)/maestro`, and ticket commands are `maestro-ticket`'s; if `## Tickets` there names another tool, use its equivalent.

## Start

Open a todo list with one item per phase below.

## Phase A: Find candidates (read only)

| Class | How to find it |
|---|---|
| Unreferenced files | `git grep -n <basename without extension>` finds nothing outside the file itself; also check dynamic loads (globs, string-built paths, plugin registries) |
| Dead code | exports with no importers, functions with no callers, flags read nowhere, tests for deleted code |
| Orphaned docs | docs for removed features, and links to them |
| Duplicates | blob shas that repeat in `git ls-files -s`, except the empty blob `e69de29` |
| Generated output | `!!` lines of `git status --ignored --porcelain` |
| Worktrees | in `git worktree list`, other than the main checkout and your own: `maestro/<id>` worktrees whose ticket is `done` or `cancelled` or whose branch `git branch --merged <main>` lists; `qa-<id>` and `review-<id>-*` worktrees whose ticket left `in_review`; entries marked `prunable` |
| Merged branches | `git branch --merged <main>`, local only, minus `<main>` and lines marked `*` or `+` |
| Maestro leftovers | `<common>/evidence/<id>/` and `<common>/scratch/*/<id>.md` for tickets `maestro-ticket get <id> --json` shows `done` or `cancelled` |
| Empty folders | `find . -path ./.git -prune -o -type d -empty -print` |

## Phase B: Sort each candidate

| Sort | What goes there |
|---|---|
| Safe | tracked and proven unused. Its removal commit comes back on revert. |
| Ask first | anything that can't be recovered (untracked or ignored files, empty folders, worktrees, branches, Maestro leftovers), duplicates (which copy stays is a call), anything large, and anything whose only proof is "no grep hits" in code that loads files by name at runtime |
| Never | `.maestro/project/`, harness folders (`.claude/`, `.agents/`, `.grok/`), `AGENTS.md`, `CLAUDE.md`, secrets files such as `.env`, a worktree with changes or commits not on `<main>`, a branch `git branch -d` refuses, `<common>/tickets.db*`, `<common>/backups/`, `<common>/runs/`, `<common>/STOP`, and anything tied to a ticket whose `get --json` shows a live `claim` |

## Phase C: Show the list

Show every candidate by class and sort, with its proof (the grep output, the merge check, the ticket status), its size and its last change date. With `--dry-run`, stop here.

## Phase D: Remove

1. Make a worktree for tracked removals as **maestro-working-principles** says, named `prune`.
2. For each safe class: `git rm <paths>` for files, an edit for dead code, then one commit. The message says what went and how you proved it unused.
3. After each commit, run every command under `## Checks` in `project-setup.md`, or the repo's build and tests if that file is missing. If one fails, `git revert --no-edit <sha>` and move that class to the kept list.
4. Remove ask-first items only after the user says yes, by explicit path, from the main checkout: `git worktree remove <path>` (no `--force`), `git worktree prune`, `git branch -d <branch>`, `rm -r <path>`, `rmdir <path>`. Never a glob.
5. Open the PR as **maestro-working-principles** says.

```
Removed:  <class>: <paths> (<commit sha>, or "after yes")
Kept:     <path>: <reason>
Saved:    <n> files, <bytes>
Checks:   <command>: exit <code>
PR:       <url>
Waiting:  <ask-first items still waiting on the user>
```

**Reply:** the report above, filled in.
