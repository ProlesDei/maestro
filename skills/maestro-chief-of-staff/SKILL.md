---
name: maestro-chief-of-staff
description: "Settles a blocked Maestro ticket: checks the facts behind its Escalation: note, then answers it and puts the ticket back, or writes up the one decision the owner must make. Use for /maestro-chief-of-staff, 'unblock T-12', 'answer the escalation on T-12', or when a work run stops blocked."
argument-hint: "[ticket id]"
disable-model-invocation: true
---

# Settle a blocked ticket

You decide at the level of outcomes, cost and risk, and hand back either an answer on the ticket or the owner's call. You may read code and run things to learn a fact; you never change code or start a run. This skill also owns [Unblock](#unblock), the steps anyone follows to answer a block.

Follow the **maestro-working-principles** skill.

## Unblock

Whoever owns the call (chief of staff, engineer, or a person) answers on the ticket, moves it back, and runs it again. A blocked run is over, and `maestro-play resume` won't restart it; the new run starts at plan and reads the `Answer:` note, so post nothing after it.

```sh
maestro-ticket comment <id> --body 'Answer: <the decision>'
maestro-ticket transition <id> --from blocked --to <blocked_from> --role <return role>
maestro-play run <playbook> --ticket <id>
```

Fill in `<blocked_from>` from `maestro-ticket get <id> --json` (`in_progress` after a work run), `<return role>` from the note's last line, `Return-role: <role>` (the block gave the ticket to the chief of staff; `--role` gives it back), and `<playbook>` from its `Stopped at: <step> (<playbook>)` line.

## Start

Open a todo list with one item per phase below.

## Phase A: Find the facts

1. Use the id given, else the first of `maestro-ticket list --status blocked --json`. If there is none, say so and stop.
2. `maestro-ticket get <id> --json` must show `blocked`. Read its latest note that starts `Escalation:` or `Escalation (chief-of-staff):`.
3. For each question in the note, ask whether running or reading something answers it: does the file exist, what does the API return, do the checks pass on the main branch. Then find out; the note's `Where:` line names the run's branch and worktree. A fact is never the owner's to answer; what's left is a call.

## Phase B: Decide whose call it is

1. It's the owner's call when it:
   - changes a goal, scope or priority they set;
   - costs real money;
   - is hard to undo (deleting data, a production deploy);
   - speaks for them outside the team;
   - trades speed for quality on work that matters.
2. Otherwise it's yours: pick the option that stays inside the ticket's goal and is easiest to undo.
3. If the note says the ticket is really two, or a criterion can't be checked as written, answer nothing. Recommend `/maestro-planner <id>` to split or rewrite it, and stop.

## Phase C: Answer or write up

- **Yours.** Write the answer so the next run needs nothing else: the decision, and any corrected criterion in full. Run the first two commands of [Unblock](#unblock). Don't start the run.
- **The owner's.** Change nothing on the ticket. Give two or three options, each with its trade-off, and the one you'd pick with a one-line why.

```text
<id>: <title>
Facts: <one line each, with the command>
Answered: <the Answer: text>. Now: <status>, role <role>. Next: maestro-play run <playbook> --ticket <id>
Owner's call: What (one or two sentences), Why now, Options (two or three, each with its trade-off), Default (with a one-line why), Question (one word if it can be)
```

**Reply:** the ticket id and title, the facts you checked, then the `Answered:` line or the `Owner's call:` line.
