---
name: maestro-prune
description: "Use for /maestro-prune, 'prune this repo', 'remove dead code', 'delete unused files', or 'clear out stale branches and worktrees'. Finds what nothing uses, proves it, lists it by class, then removes it so a revert brings it back. Asks before anything that can't be recovered."
argument-hint: "[path] [--dry-run]"
disable-model-invocation: true
---

# Prune

Find what nothing uses, prove that nothing uses it, and remove it so a revert brings it back. Start at `[path]` if given, else the repo root.

## 1. Inventory (read only)

| Class | How to find it |
|---|---|
| Unreferenced files | for each one, `git grep -n <basename without extension>` across code, configs, CI, docs, build files and package manifests; also check dynamic loads (globs, string-built paths, plugin registries) |
| Dead code | exports with no importers, functions with no callers, flags read nowhere, tests for deleted code |
| Orphaned docs | docs for removed features, and links to them |
| Generated output | untracked files `.gitignore` covers: `git status --ignored --porcelain` |
| Stale worktrees | in `git worktree list`, a worktree whose branch is merged into main and whose `git status --porcelain` prints nothing |
| Merged branches | `git branch --merged <main>`, local only |
| Maestro leftovers | `.maestro/worktrees/<ticket>`, and `evidence/<ticket>` and `scratch/` files under `$(git rev-parse --git-common-dir)/maestro/`, for tickets `maestro-ticket get <id> --json` shows `done` or `cancelled`; ask the user how old they must be |
| Empty folders, duplicates | `find -type d -empty`; identical files by `git hash-object` |

## 2. Sort each candidate

- **Safe:** tracked and proven unused. A `git rm` in its own commit comes back on revert.
- **Ask first:** anything that can't be recovered (untracked or ignored files, worktrees, branches, Maestro leftovers), anything large, and anything whose only proof is "no grep hits" in code that loads files by name at runtime.
- **Never:** `.maestro/project/`, harness settings (`.claude/`, `.agents/`, `.grok/`), `AGENTS.md`, `CLAUDE.md`, secrets files, a worktree with changes, a branch with unpushed or unmerged commits, the ticket store (`tickets.db` and `backups/`), the `STOP` file, and anything another session holds.

## 3. Show the list

Show every candidate by class, with its proof (the empty grep, the merge check) and its size. With `--dry-run`, stop here.

## 4. Remove

1. Remove the safe class with `git rm <paths>`, one commit per class. Each message says what went and how you proved it unused.
2. Remove ask-first items only after the user says yes, one item or class at a time, by explicit path. Use `git worktree remove <path>` (no `--force`) and `git branch -d`. Never `git clean`, `rm -rf` on a pattern, or `git branch -D`.
3. Run every command under `## Checks` in `.maestro/project/project-setup.md`, or the repo's build and tests if that file is missing. If one fails, revert that class's commit and move its items to the kept list.

Reply: the removed items by class with their commits; the kept items, each with its reason; files and bytes saved; the check results; then the ask-first items still waiting on the user.
