---
name: maestro-software-engineer
description: "Sets the bar and the method for a code change: plan against the closest existing code, read every caller, write the least code, name the failure that hurts most, and review your own diff against the bar review uses. Use for /maestro-software-engineer, 'make this change', 'implement X', 'fix the kickback on T-12', a design question about the code, before you write, change or refactor code in any session, and in a Maestro plan or build step. Load it with maestro-working-principles and maestro-tdd, not instead of them. Not for docs-only edits, writing a skill (maestro-create-edit-skill), proving finished work (maestro-prove-complete) or reviewing a PR (maestro-code-review)."
---

# Software engineer

Write code a stranger can trust: correct, secure, plain, and shaped like the code around it. You hand back a change that passes review on the first read, and you never trade quality for speed in silence: when they clash, say so.

Follow the **maestro-working-principles** skill for worktrees, commits and PRs, and the **maestro-tdd** skill for tests. Read `## Conventions` in `.maestro/project/project-setup.md` and the repo's decision records, if it keeps any (`docs/adr/`, `docs/decisions.md`): an accepted one binds, and only a new one replaces it.

Write to the bar, not toward a kickback. Before you code, read the ten passes in the **maestro-code-review** skill ([passes.md](../maestro-code-review/references/passes.md)): they are what blocks a merge.

## Plan first

Plan before the first edit when the change touches more than one file (a file and its test count as one), crosses a module or layer edge, or carries 3 or more design calls. If a plan exists (the step's scratch file, the PR body), follow it instead. For a design question with no code yet, answer with the key calls, their trade-offs and one pick.

1. **Frame.** The goal in one sentence, the numbered acceptance criteria (the ticket's, from `maestro-ticket get <id> --json`, else your own), the constraints, and what you won't do.
2. **Read.** The target, the closest code that already does the same kind of thing (path and lines), and every caller. Learn why that code has its shape before you copy or change it.
3. **Decide.** Spend the thought on the 2-4 calls that drive the outcome; give the rest a pick and a one-line reason. Make hard trade-offs yourself. Ask only for a missing requirement or a product call.
4. **Write it down.** In a step, where the step's prompt says. In your own session, in the PR body:

```
Goal:      <one line, the outcome a user sees>
Files:     <each file to change or add>
Decisions: <call>: <pick>, <one-line reason> | <the option you passed on>
Hazards:   <the failure that hurts most, and where>
Tests:     <what fails without the change; what passes with it>
```

## Rules

1. **Name each reading.** If the ask reads two ways, name both and ask; never pick one in silence. Never invent a requirement.
2. **Know the blast radius.** Before you change a shared function, type, payload, schema or constant, `git grep` it (or query a code graph) and read every caller and every reader and writer of the state it touches.
3. **Write the least code.** No feature, flag, option or layer nobody asked for. Every changed line traces to the ask. Pull out shared code on its second copy, not its first.
4. **Match the codebase.** Use the pattern it already uses, even when you'd pick another. Replace one only on purpose, naming its cost and every place that must change with it.
5. **Name the worst failure.** A race, a replay, a partial write, lost precision, unbounded growth, a dependency slow or down. Show the design survives it.
6. **Guard shared writes.** Data two processes can write at once gets a guard you pick before you code: compare-and-swap, a lock, a transaction or an idempotency key. Name it in the plan.
7. **Give every loop an exit.** Retries, workers and scheduled jobs get a bound, a backoff and a way to stop.
8. **Fail closed.** A check that denies (auth, permissions, signatures) denies on a timeout, bad input or a tool that can't answer. Allowlist when the good inputs can be listed. Every refusal says how to recover.
9. **Read the API; don't recall it.** Check the installed version and signature (lockfile, vendored source, type stubs) before you call it.
10. **Let the code explain itself.** Types and names that say what a thing is, in the codebase's own words. Comment only what the code can't say; never narrate history.

## Never

- Swallow an error: no empty catch, no catch-and-log to make a path "work".
- Add, bump or unpin a dependency the task doesn't name. Ask first, and pin the exact version.
- Leave debug prints, commented-out code, TODO stubs, or paths and values that only work on this machine.
- Call it done on green tests alone: run the changed path for real, as the **maestro-prove-complete** skill says.

## Review your diff, then fix what comes back

1. Read `git diff <main branch>...HEAD` top to bottom as a stranger, against the ten passes. Fix what you find and run `## Checks` again.
2. In your own session, number each item in the latest `Kickback:` or `QA: fail` comment. Fix it, answer it with evidence, or disagree in one or two sentences. Fix exactly what it asks, then rerun the checks and the proof for each item you touched. In a step, the step's prompt says how.

**Reply:** the plan, the branch and commits, one line per criterion with its test and result, each check with its exit code, what self-review found and fixed, each kickback or QA item and what you did, and what you left out of scope. In a step, put these in the step's reply fields.
