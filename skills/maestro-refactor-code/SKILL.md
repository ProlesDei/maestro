---
name: maestro-refactor-code
description: "Change the shape of code without changing what it does, in small named steps checked against tests that pin today's behaviour. Use for /maestro-refactor-code, 'refactor X', 'restructure this module', 'split this file', or before a change the current shape blocks. Not for a bug fix or feature (the maestro-tdd skill) or removing unused code across a repo (the maestro-prune skill)."
argument-hint: "<what to refactor and why> [ticket id]"
disable-model-invocation: true
---

# Refactor code

You change the code's shape, never what it does, and hand back small commits with proof that behaviour held. Follow the **maestro-working-principles**, **maestro-software-engineer** and **maestro-tdd** skills; this skill's report and reply replace the engineer's plan and reply.

## Start

Open a todo list with one item per phase below.

## Phase A: Name the problem

1. With a ticket id, read the problem from `maestro-ticket get <id> --json`, or the team's ticket tool.
2. Name the problem in concrete terms: duplication at `a:12` and `b:40`, a 600-line function, a module everything imports, misleading names. Name what it blocks: the change, ticket or bug it gets in the way of. "Cleaner" is not a reason. If you can't name both, stop.
3. Sketch the target shape from patterns the repo already uses, then the named steps that reach it, in order, with deleting what nothing calls first. Add each step to the todo list. Stop when the target holds.
4. List every caller of what you will move or rename, as the **Know the blast radius** rule of **maestro-software-engineer** says. Callers show which behaviours are in use; Phase B pins each once, at its edge.

## Phase B: Pin the behaviour

1. Edges are exported functions, CLI output, API responses, file formats, error text callers match, and names that leak out: serialized field names, log keys, metric names, output order. Find the tests that cover each; where none do, write a characterisation test (a pin).
2. A pin calls the edge as a caller does and checks what comes out: return values, output, files written, errors. It passes on the unchanged code. Pin a result that looks wrong as it is, and list it as a follow-up. With no test runner, pin with a script that diffs output at the nearest outer edge, such as the CLI.
3. List what you can't pin, and keep the refactor away from it. If the target itself can't be pinned, stop and say why.
4. Commit the new pins alone, before any refactoring.
5. Prove each new pin, and each edge's old tests, can fail: break the code by hand, run them, see red, then `git checkout -- <path>`. One break per edge is enough for old tests. Fix a pin that stays green in a new commit.
6. Run the build, lint and tests on the unchanged code and note each exit code. Where speed matters (code that runs per request or in a loop), measure as the **maestro-prove-complete** skill says.

## Phase C: Move in small, named steps

1. Make one named refactoring per commit, and name it in the subject: extract or inline a function or variable, rename, move a function or module, introduce a parameter object, replace a conditional with a lookup table, split a module along a seam, delete code nothing calls (proved the way **maestro-prune** proves it). Move code by copying its lines (`git mv`, a script), never by retyping it.
2. After each step, build or typecheck the whole repo and run the pins. Commit only on green. A red pin means the step changed behaviour: fix the step, or undo it with `git checkout -- <paths>`, delete the files it added by path, and take a smaller step.
3. From here on, a pin's inputs and expected results never change. A step may change its imports, the names it calls, or how its arguments are grouped. A test of an internal you inline may go once an edge pin covers it; say so in the commit. If a pin's values must change, the work changes behaviour: stop, and give it a ticket of its own with the **maestro-create-ticket** skill.
4. Move in-repo callers over a few per commit, and delete the old name before the branch is done. For what outside code can call (a package's exports, a public API, CLI flags), leave a shim at the old name and mark it deprecated.
5. After a rename, `git grep -n <old name>` finds only shims and text the program prints. Fix docs and comments that still use it. Reformat only lines you moved.

## Phase D: Prove it

1. Run the build, lint and tests again. A check green in Phase B and red now: find the step that broke it, then fix or revert it. Red in Phase B too: a follow-up.
2. Run the changed path for real on `<main branch>` and on HEAD, the way **maestro-prove-complete** says, and diff the outputs.
3. Read `git diff --color-moved <main branch>...HEAD`. Each line that isn't a move belongs to a named step; undo any other edit.
4. Where speed matters, measure again the same way. If it got slower beyond the spread, name the step and say so.

```
Problem:    <the concrete problem>; blocks: <what>; target: <the shape>
Pinned:     <pin commit shas>; <edge>: red when <the break>   (one line each)
Pin diff:   git diff <first pin sha> HEAD -- <pin files>: <imports, call names, inlined-internal tests only>
Steps:      <sha> <refactoring>: <what moved>   (one line each)
Checks:     <command>: exit <before> / <after>; real run: <outputs match | the diff>; speed <median±spread> / <median±spread>
Not pinned: <areas the refactor stayed away from>
Follow-ups: <one line each>
```

**Reply:** the report above, filled in, then the branch and the PR link if you opened one.
