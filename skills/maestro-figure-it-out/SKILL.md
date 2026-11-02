---
name: maestro-figure-it-out
description: "Works out and does a one-off task that no skill covers and that sits outside the ticket flow: frames it, plans small checked steps, gets a yes before anything that can't be undone, and hands back the result with proof and whether it should become a ticket or a skill. Use for /maestro-figure-it-out, 'no skill fits this', 'how should we even approach X', 'a one-off job', or work that isn't a ticket. Not for a code change that ships through review (maestro-create-ticket, maestro-software-engineer) or a task an installed skill already covers."
argument-hint: "<the task>"
disable-model-invocation: true
---

# Figure it out

You take a task that fits no skill and no ticket, such as a migration of the team's tooling, a mess in the repo's state, or a question no one has asked before, and get it done in small checked steps. You hand back the result, its proof, and whether it should repeat as a ticket or a skill. You never take a step that can't be undone without the user's yes.

Follow the **maestro-working-principles** skill for the session rules, worktrees, commits and getting unstuck.

## Start

Open a todo list with one item per phase below.

## Phase A: Frame

1. Write the goal in one sentence, and what done looks like as checks someone can run.
2. List the inputs, the outputs and where they land, each step that can't be undone (a delete, a push, a deploy, a call outside the repo), and each call only a person should make.
3. If the task reads two ways, name both and ask. If an installed skill or the ticket board covers it after all, stop and name that instead.

## Phase B: Plan

1. Break the work into steps of a few minutes each. Each step ends in a check that shows it worked.
2. Mark each step `undoable` or `final`. Put every `final` step as late as you can, after the steps that prove it is safe.
3. Pick where the work happens: a worktree of your own if it changes tracked files, else a fresh temp folder.
4. Show the plan, and get the user's yes for each `final` step. With no one to answer (`claude -p`), stop before the first `final` step and list them under `Waiting`.

## Phase C: Do

1. Run the steps in order. After each, run its check and keep the command, its exit code and a short excerpt of output.
2. When a check fails, fix the step before you go on. Two fixes on one idea failed: test the idea on its own, then stop and ask if it still fails.
3. Make each reversible call yourself and note it. Ask only on taste, risk, or a step marked `final`.
4. Stop what you started and remove your temp folder.

## Phase D: Hand back

1. Check each done criterion from Phase A on the real result, as the **maestro-prove-complete** skill says. A criterion you couldn't check is `not checked`, with the reason.
2. Say whether the task will come back. If it will, propose a ticket (**maestro-create-ticket**) for the work left, or a skill (**maestro-create-edit-skill**) for the method.

**Reply:** the goal, each step with its check and exit code, each done criterion (pass | fail | not checked, evidence), each call you made, what is `Waiting`, and the ticket or skill you propose, if any.
