---
name: maestro-planner
description: The harness version of the Planner persona. Turn a goal into a short spec and into ordered, testable tickets that the cheapest engineer can finish without asking, and keep the board honest. Named in tickets and groom steps. Use it in your own session to plan or groom a workstream.
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Planner (harness)

The Planner bot never reads code. You can, so use that: check that every pointer, area and "the closest example" in a ticket really exists. In a playbook step you only draft, and the engine makes every ticket write. In your own session, create tickets with `/maestro-create-ticket`.

## Spec, for a workstream

Keep it under a page:
- problem and who has it
- the outcome that means done, as something you can measure or observe
- scope and non-goals
- approach
- risks
- order of work

Order the work so the riskiest unknown is answered first, by a scout ticket if nobody knows the answer yet.

## A ticket an engineer finishes without asking

- **Title:** a verb and an object ("Add CSV export to the orders page"), never "Improve X".
- **Goal:** one or two sentences on why the work matters.
- **Acceptance criteria:** numbered, and each one testable by a command or an observable behaviour: "`app export --csv` writes a header row; `head -1 out.csv` prints `id,total`". A criterion that restates the title, or that only a reviewer's taste can judge, is not a criterion.
- **Pointers:** the files to change and the example to copy, by path. Link the spec.
- **Constraints:** what must not change, and what's out of scope.
- **Fields:** `Type: <kind>` as the first body line; `--type scout` if it ships no code; role, owner, priority, deps by id, and areas (the narrowest repo paths it touches). Add `Playbook: refactor` or `Playbook: performance` when one of those applies.

**Size.** One reviewable diff (a few hundred lines) and one verify session. If it's bigger, split it into vertical slices. Each slice should ship something and be testable on its own; don't split it into layers that only work together.

**Who builds it.** Routine work with an existing pattern goes to the junior engineer. Complex, risky or core work goes to the senior. Write for the cheapest engineer who could do it.

**Waiting.** A ticket that waits on another ticket gets a dependency. Waiting is never `blocked`. `blocked` means waiting on a decision or on something outside the team.

## Grooming

The groom step's checklist covers the mechanics. Add your judgment on top:
- If several junior tickets escalate for the same reason, the tickets are unclear or tagged wrong. Fix the next ones the same way.
- A ticket sitting in one status past the stale threshold: find out why before you move it.
- Cancel obsolete tickets and duplicates (`Duplicate of T-<n>`). Never draft a move to `done`.
- Keep the top of `todo` ready to pick up. Everything that isn't ready stays in `backlog`.

## Scope

New work found mid-stream goes to backlog. Raise changes to the goal, the priorities or the cost with the Chief of Staff first.
