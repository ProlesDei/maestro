# Code pruning

Read this only when no cleanup candidate sorts Safe or Ask first, or the user passed `--code`. Everything here is tracked, so it leaves through a reviewed PR that a person merges, since it has no ticket, never straight from the main checkout. `[path]` after `--code` limits the search; it defaults to the repo root. `<main>`, `<common>` and the keep list are as in [SKILL.md](../SKILL.md).

## Find (read only)

Search `origin/<main>`, not the main checkout, so the proof matches main as it is now.

| Class | How to find it |
|---|---|
| Unreferenced files | `git grep -n <basename without extension> origin/<main> -- <path>` finds nothing outside the file itself; also check dynamic loads (globs, string-built paths, plugin registries) |
| Dead code | exports with no importers, functions with no callers, flags read nowhere, tests for deleted code |
| Orphaned docs | docs for removed features, and links to them |
| Duplicates | blob shas that repeat in `git ls-tree -r origin/<main> -- <path>`, except the empty blob `e69de29` |

## Sort

| Sort | What goes there |
|---|---|
| Safe | proven unused, in code that never loads files by name at runtime |
| Ask first | duplicates (which copy stays is a call), files over 1 MB, and anything whose only proof is "no grep hits" in code that loads files by name |
| Keep | everything on the keep list in SKILL.md |

Phase C in SKILL.md shows them with the rest, and `--dry-run` stops there.

## Remove

1. Make your own worktree outside the main checkout, as **maestro-working-principles** says, on a new branch with the fixed name `maestro/prune` from `origin/<main>`. The fixed name lets only one code prune run at a time. If the branch exists, another prune holds it, or its PR hasn't merged: change nothing and report `Code: skipped: maestro/prune exists`.
2. Run every check the repo's agent notes (AGENTS.md or CLAUDE.md) name, else the build and lint its README and build files show, before any change, and note each one already red.
3. For each class: `git rm <paths>` for files, an edit for dead code, then one commit. Ask-first items go in only after a yes, in their own commit. The message says what went and how you proved it unused.
4. After each commit, run the checks. If one that passed in step 2 fails, `git revert --no-edit <sha>` and move that class to `Kept`.
5. If any class stays, open the PR as **maestro-working-principles** says, with the step 2 results in its body. If none stays, open no PR and report the branch.
