---
name: maestro-refactor-code
description: Refactor code. Change its structure without changing what it does. Pin current behaviour with tests that pass today and are proven able to fail, then make small named refactorings with the tests green after each, and never mix in a behaviour change.
argument-hint: "<what to refactor and why> [ticket id]"
disable-model-invocation: true
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Refactor code

If this is a ticket, give it the body line `Playbook: refactor` and run `/maestro-work <id>`. The senior engineer's refactor playbook does the same as below, with a review and QA after it. Use the steps here when you're working by hand.

## 1. Say why, and what the target shape is

Name the problem in concrete terms: duplication at `a:12` and `b:40`, a 600-line function, a module everything imports, names that lie. Sketch the shape you're aiming for in a few lines. "Cleaner" is not a reason. If you can't name a problem, stop.

## 2. Pin the behaviour

- Write characterisation tests at the public edges the refactor could touch: exported functions, CLI output, API responses, file formats, error messages that callers parse. They must pass on the unchanged code.
- List what you can't test, and keep the refactor away from it.
- Prove the tests can fail: make one deliberate break in the code (not in a test), see a test go red, and undo the break until `git status --porcelain` prints nothing.
- Commit the tests alone (`test: characterise <area>`).

## 3. Move in small, named steps

Each step is one known refactoring, with the tests green after it, in its own commit:
- extract or inline a function or variable
- rename
- move a function or module
- introduce a parameter object
- replace a conditional with polymorphism or a lookup table
- split a module along a seam
- replace a temp with a query
- remove dead code that the tests prove unused

## 4. Rules

- The characterisation tests never change. If one has to change, that step is a behaviour change: stop, and make it a ticket of its own.
- Keep public interfaces. If one must move, leave a shim at the old name and record that it's deprecated.
- No formatting churn, no new dependencies, and no "while I'm here" fixes. Write those down as tickets.
- On a hot path, run the same benchmark before and after (`/maestro-prove-complete`).

## 5. Reply

The problem and the target shape, the characterisation test commit and its proof that it fails, one line per refactoring commit, the check results, what you couldn't test, and the follow-up tickets.
