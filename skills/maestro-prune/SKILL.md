---
name: maestro-prune
description: "Clean up a repo's dead weight: unused files and folders, dead code, stale worktrees and branches, orphaned docs, and leftover build and evidence output. Every removal is proven unused, reversible and grouped by class. It lists before it deletes and asks before anything that can't be recovered."
argument-hint: "[path] [--dry-run]"
disable-model-invocation: true
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Prune

Find what nothing uses, prove that nothing uses it, and remove it so that a revert brings it back. Start with `[path]` if one is given, otherwise the repo root. With `--dry-run`, stop after the inventory.

## 1. Inventory (read only)

| Class | How to find it |
|---|---|
| Unreferenced files | for each candidate, `git grep -n <basename without extension>` across code, configs, CI, docs, build files and package manifests; also check dynamic loads (glob patterns, string-built paths, plugin registries) |
| Dead code | exports with no importers, functions with no callers, flags read nowhere, tests for deleted code |
| Orphaned docs | docs for removed features; broken links to them |
| Generated output | untracked files that `.gitignore` covers: list them with `git status --ignored --porcelain` |
| Stale worktrees | `git worktree list`: a worktree whose branch is merged into main and whose `git status --porcelain` prints nothing |
| Merged branches | `git branch --merged <main>`, local only |
| Maestro leftovers | old evidence, scratch and log files under `$(git rev-parse --git-common-dir)/maestro/` for tickets that are `done` or `cancelled` (older than the project's retention period, if `.maestro/project/budgets.json` sets one; otherwise ask) |
| Empty folders, duplicates | `find -type d -empty`; identical files with `git hash-object` |

## 2. Sort each candidate

- **Safe:** tracked and proven unused. A `git rm` in a commit brings it back on revert.
- **Ask first:** anything that can't be recovered (untracked or ignored files, worktrees, branches, Maestro leftovers), plus anything large, and anything whose only proof is "no grep hits" in a codebase that loads files dynamically.
- **Never:** `.maestro/local/`, harness settings (`.claude/`, `.agents/`, `.grok/`), `AGENTS.md`, `CLAUDE.md`, secrets files, a worktree with changes, a branch with unpushed or unmerged commits, the ticket store, anything another session holds.

## 3. Remove

1. Show the full list by class, with the proof for each item (the grep that came back empty, the merge check), and the size.
2. Remove the safe class with `git rm <paths>`, one commit per class, each saying what went and how you proved it unused.
3. Remove the ask-first items only after the user says yes, one item or class at a time, by explicit path. Never `git clean`, `rm -rf` on a pattern, or `branch -D`. Use `git worktree remove <path>` (no `--force`) and `git branch -d`.
4. Run the build and the tests listed under "Automated checks" in `project-setup.md`. If anything fails, revert that class's commit and move its items to the "kept" list.

## 4. Reply

The removed items by class, with their commits. The kept items, each with its reason. The bytes and files saved. The check results. Then the ask-first items still waiting on the user.
