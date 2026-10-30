---
name: maestro-refactor-code
description: "Use when the user types /maestro-refactor-code or says 'refactor X', 'restructure this module', or 'clean this up without changing behaviour'. Pins current behaviour with tests proven able to fail, then makes small named refactorings with the tests green after each. Never mixes in a behaviour change."
argument-hint: "<what to refactor and why> [ticket id]"
disable-model-invocation: true
---

# Refactor code

Change the code's structure, never what it does. The tests stay green after every step, and no step changes behaviour.

## 1. Name the problem and the target

With a ticket id, read the problem from `maestro-ticket get <id> --json`. Name it in concrete terms: duplication at `a:12` and `b:40`, a 600-line function, a module everything imports, names that lie. Sketch the shape you are aiming for in a few lines. "Cleaner" is not a reason. If you can't name a problem, stop.

## 2. Pin the behaviour

1. Write characterisation tests at the public edges the refactor could touch: exported functions, CLI output, API responses, file formats, error messages callers parse. They must pass on the unchanged code.
2. List what you can't test, and keep the refactor away from it.
3. Prove the tests can fail: break the code on purpose (not a test), watch a test go red, and undo the break until `git status --porcelain` prints nothing.
4. Commit the tests alone: `test: characterise <area>`.

## 3. Move in small, named steps

Each step is one known refactoring, in its own commit, with the tests green after it:

- extract or inline a function or variable
- rename
- move a function or module
- introduce a parameter object
- replace a conditional with polymorphism or a lookup table
- split a module along a seam
- replace a temp with a query
- remove dead code the tests prove unused

## 4. Hard rules

- The characterisation tests never change. If one must, that step changes behaviour: stop, and give it a ticket of its own.
- Keep public interfaces. If one must move, leave a shim at the old name and mark it deprecated.
- No formatting churn, no new dependencies, no "while I'm here" fixes. List those as follow-ups for `/maestro-create-ticket`.
- On a hot path, run the same benchmark before and after, under the same conditions, and give both numbers.

Reply: the problem and target shape; the characterisation test commit and the break that turned it red; one line per refactoring commit; the check results; what you couldn't test; and the follow-ups.
