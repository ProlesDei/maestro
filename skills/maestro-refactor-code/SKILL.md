---
name: maestro-refactor-code
description: "Pin current behaviour with tests proven able to fail, then make small named refactorings with the tests green after each. Use for /maestro-refactor-code, 'refactor X', 'restructure this module', or 'clean this up without changing behaviour'."
argument-hint: "<what to refactor and why> [ticket id]"
disable-model-invocation: true
---

# Refactor code

You change the code's structure, never what it does. The tests stay green after every step, and no step mixes in a behaviour change. Follow the **maestro-working-principles** skill.

## Start

Open a todo list with one item per phase below.

## Phase A: Name the problem

1. With a ticket id, read the problem from `maestro-ticket get <id> --json`.
2. Name the problem in concrete terms: duplication at `a:12` and `b:40`, a 600-line function, a module everything imports, names that lie. "Cleaner" is not a reason. If you can't name a problem, stop.
3. Sketch the target shape in a few lines.

## Phase B: Pin the behaviour

1. Write characterisation tests at the public edges the refactor could touch: exported functions, CLI output, API responses, file formats, error messages callers parse. They must pass on the unchanged code.
2. List what you can't test, and keep the refactor away from it.
3. Prove the tests can fail: break the code on purpose (not a test), watch a test go red, and undo the break until `git status --porcelain` prints nothing.
4. Commit the tests alone: `test: characterise <area>`.

## Phase C: Move in small, named steps

1. Make one known refactoring per commit, with the tests green after it: extract or inline a function or variable, rename, move a function or module, introduce a parameter object, replace a conditional with polymorphism or a lookup table, split a module along a seam, replace a temp with a query, remove dead code the tests prove unused.
2. Never change a characterisation test. If one must change, that step changes behaviour: stop, and give it a ticket of its own.
3. Keep public interfaces. If one must move, leave a shim at the old name and mark it deprecated.
4. Make no formatting churn, add no dependencies, fix nothing "while you're here". List those as follow-ups for the **maestro-create-ticket** skill.

## Phase D: Prove it

1. Run every command under `## Checks` in `.maestro/project/project-setup.md`, or the repo's build and tests if that file is missing.
2. On a hot path, run the same benchmark before and after, under the same conditions, and give both numbers.

```
Problem:    <the concrete problem>; target: <the shape>
Pinned:     <test commit sha>; went red when <the break>
Steps:      <sha> <refactoring>: <what moved>   (one line each)
Checks:     <command>: exit <code>; benchmark <before> / <after>
Not tested: <areas the refactor stayed away from>
Follow-ups: <one line each>
```

**Reply:** the report above, filled in.
