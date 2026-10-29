---
name: maestro-pr-risk
description: Review a branch, PR or piece of work for the risk of merging it, and give it a score from 0 to 10 with the reasons, what would lower it, and how deep its review should go. Read only.
argument-hint: "[branch | PR number | ticket id] [--base <branch>]"
disable-model-invocation: true
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# PR risk

This command scores what could go wrong if the change merged today, and how hard that would be to undo. It does not judge whether the change is good: `/maestro-pr-review` does that. It never edits, comments or writes a ticket.

## Resolve the target

- A ticket id means its branch, `maestro/<id>`. A PR number means its head branch (`gh pr view <n> --json headRefName,baseRefName`). No argument means the current branch.
- The base is `--base`, or else the main branch from `.maestro/project/project-setup.md`.
- Diff from the merge base, never from the moving tip: `B=$(git merge-base <base> <branch>)`, then `git diff --stat $B <branch>` and `git diff $B <branch>`.
- Check that it merges cleanly: `git merge-tree --write-tree <base> <branch>`. A conflict is reported first, before any score.

## Score: four factors, 0 to 10 in total

| Factor | 0 | 1 | 2 | 3 |
|---|---|---|---|---|
| **Blast radius** | docs, tests, a leaf file | one module, few callers | a shared module or public interface, many callers | a path every request takes, or a contract between services |
| **Sensitivity** (the highest one touched) | docs, tests, dev tooling | UI, copy, internal logic | data model, config, dependencies, CI, caching, concurrency, error handling on a hot path | auth, permissions, money, secrets or crypto, migrations, deploy or infrastructure |
| **Reversibility** | a revert undoes it | it needs a flag, a config change or a coordinated deploy | irreversible: it drops or rewrites data, sends external side effects, or publishes an API or version | |
| **Evidence gap** | every changed behaviour has a test that fails without it, and the checks are green on the head | partial: some behaviour untested, or checks unknown | untested behaviour changes, or red checks | |

Measure, don't guess:
- blast radius: callers from `git grep`
- sensitivity: the paths and symbols touched
- reversibility: look for migrations, API schemas and outbound calls in the diff
- evidence gap: run the tests, or read the latest CI on the head sha

**Bands:** 0–2 low, 3–4 medium, 5–7 high, 8–10 critical.

**Raise the band by one** if any of these holds, and say which:
- more than about 500 changed lines
- files outside the ticket's areas
- a dependency or lockfile change
- changes to guardrails (hooks, CI, agent settings)
- author and reviewer on the same model family

## Report

```
PR risk: <branch> @ <sha> vs <base> @ <merge-base sha>
Score: <n>/10 (<band>)   blast <n> · sensitivity <n> · reversibility <n> · evidence <n>   raised: <reason or none>
Merges cleanly: yes | no (<files>)
Top risks:
1. <file:line>: <what could break, for whom>; <the evidence for it>
What would lower it:
- <a test to add, a split, a flag, a rollback step>, and which factor it lowers
Review depth: /maestro-pr-review --effort <low|medium|high|max> --risk <band>
```

Give the top three risks at most, each one pointing at real lines. If the score rests on something you couldn't check, say what, and score that factor as if it were the worst case.
