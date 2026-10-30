---
name: maestro-refactor-code
description: "Change the shape of code without changing what it does: name the problem and what it blocks, pin today's behaviour with tests proven able to fail, then make small named refactorings with the tests green after each. Use for /maestro-refactor-code, 'refactor X', 'restructure this module', 'split this file', or 'clean this up without changing behaviour'. Not for a bug fix or feature (the maestro-tdd skill) or removing unused code across a repo (the maestro-prune skill)."
argument-hint: "<what to refactor and why> [ticket id]"
disable-model-invocation: true
---

# Refactor code

You change the code's shape, never what it does, and hand back small commits with proof that behaviour held. No step mixes in a behaviour change. Follow the **maestro-working-principles** and **maestro-software-engineer** skills.

## Start

Open a todo list with one item per phase below.

## Phase A: Name the problem

1. With a ticket id, read the problem from `maestro-ticket get <id> --json`.
2. Name the problem in concrete terms: duplication at `a:12` and `b:40`, a 600-line function, a module everything imports, names that lie. Name what it blocks: the change, ticket or bug it gets in the way of. "Cleaner" is not a reason. If you can't name both, stop.
3. Sketch the target shape in a few lines, built from patterns the repo already uses.
4. List every caller of what you will move or rename, as rule 2 of **maestro-software-engineer** says. These are the edges Phase B pins.

## Phase B: Pin the behaviour

1. For each edge (exported functions, CLI output, API responses, file formats, error messages callers parse), find the tests that cover it. Where none do, write a characterisation test (a pin) that passes on the unchanged code.
2. List what you can't pin, and keep the refactor away from it.
3. Commit the new pins alone, before any refactoring.
4. Prove each pin, old or new, can fail: break the code it covers by hand, run it, see red, then `git checkout -- <path>`. A pin that stays green pins nothing: fix it before you go on.
5. On a hot path, measure now, the way the **maestro-prove-complete** skill says for speed.

## Phase C: Move in small, named steps

1. Make one named refactoring per commit, and name it in the subject: extract or inline a function or variable, rename, move a function or module, introduce a parameter object, replace a conditional with a lookup table or polymorphism, split a module along a seam, delete code nothing calls (proved the way the **maestro-prune** skill proves it).
2. After each step, run the pins and the tests for the files you touched. Commit only on green. A red pin means the step changed behaviour: fix the step, or undo it and take a smaller one.
3. Never change a pin's inputs or expected output; a move may change its imports. If a pin must change, the work changes behaviour: stop, and give it a ticket of its own with the **maestro-create-ticket** skill.
4. For a step too big for one commit, add the new shape beside the old, move callers over a few per commit, and delete the old once `git grep` finds no caller. Code outside the repo may call a public interface, so leave a shim at its old name, marked deprecated.
5. Make no formatting churn and fix nothing on the side. List those as follow-ups.

## Phase D: Prove it

1. Run the repo's own build, lint and tests, as its README, build files or CI config name them.
2. On a hot path, measure again the same way. If it got slower beyond the spread, find the step that did it and say so.

```
Problem:    <the concrete problem>; blocks: <what>; target: <the shape>
Pinned:     <sha of new pins>; <edge>: red when <the break>   (one line each)
Steps:      <sha> <refactoring>: <what moved>   (one line each)
Checks:     <command>: exit <code>; speed <before> / <after>
Not pinned: <areas the refactor stayed away from>
Follow-ups: <one line each>
```

**Reply:** the report above, filled in, then the branch and the PR link if you opened one.
