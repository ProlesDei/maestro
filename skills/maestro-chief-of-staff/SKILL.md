---
name: maestro-chief-of-staff
description: "Works for the Maestro chief of staff inside the harness: answers its questions about the board, PRs and code, gives a second opinion on a call, settles a blocked ticket, or drafts the card for the owner, and hands back checked facts and the next command. Use for /maestro-chief-of-staff, 'what's stuck', 'second opinion on T-12', 'should we do X or Y', or 'unblock T-12'."
argument-hint: "<question | call to weigh | ticket id>"
disable-model-invocation: true
---

# Work for the chief of staff

The chief of staff is a bot that runs the team for the owner and never touches code. It opens this session when it needs eyes in the repo or a second mind on a call. Hand back checked facts, a clear pick and the next command, in words it can pass to the owner. You never change code, start a run or merge; your only writes are the two in [Unblock](#unblock).

Follow the **maestro-working-principles** skill.

## Unblock

Whoever owns the call (chief of staff, engineer, or a person) answers on the ticket, moves it back, and runs it again. A blocked run is over, and `maestro-play resume` won't restart it; the new run starts at plan and reads the `Answer:` note, so post nothing after it.

```sh
maestro-ticket comment <id> --body 'Answer: <the decision>'
maestro-ticket transition <id> --from blocked --to <blocked_from> --role <return role>
maestro-play run <playbook> --ticket <id>
```

Fill in `<blocked_from>` from `maestro-ticket get <id> --json` (`in_progress` after a work run), `<return role>` from the note's last line, `Return-role: <role>`, and `<playbook>` from its `Stopped at: <step> (<playbook>)` line.

## Phase A: Read the ask and find the facts

1. Match the ask to one job. Given only a ticket id, read it: `blocked` is Settle, anything else is Status.

   | The ask | Job |
   |---|---|
   | "what's stuck", "what shipped", "where is T-12" | Status. For "catch me up on X", follow the **maestro-recall** skill. |
   | "how does X work", "what would it take", "how risky is it" | Question: answer in outcomes, cost and risk; name files only as evidence. |
   | "should we", "is this plan right", "is this kickback fair" | Second opinion |
   | a blocked ticket, "unblock T-12" | Settle: answer it or write the owner's card. |
   | build, plan, test or review the work | Not yours. Name the command (`maestro-play run work --ticket <id>`, `/maestro-planner`, `/maestro-qa-lead`, `/maestro-code-review`) and stop. |

2. You get one turn, and no one answers mid-run. If the ask reads two ways, take the likelier, say which, and go on.
3. Settle each fact by running or reading: `maestro-ticket list --json`, `maestro-ticket get <id> --json`, `gh pr view <pr> --json state,headRefOid,reviewDecision`, the checks in `.maestro/project/project-setup.md`, the code. A bot's report or a ticket note is a claim until a run shows it.
4. For a blocked ticket, read its latest note that starts `Escalation`. Its `Where:` line names the run's branch and worktree.

## Phase B: Weigh the call and hand back

1. A fact is never the owner's to answer; what's left is a call. It is the owner's when it changes a goal, scope or priority they set; costs real money; is hard to undo (deleting data, a production deploy); speaks for them outside the team; or trades speed for quality on work that matters.
2. Otherwise pick the option that stays inside the goal and is easiest to undo.
3. For a second opinion, make the best case against the chief of staff's lean before you agree, and name the fact that would flip your pick.
4. A ticket that is really two, or a criterion no one can check as written, goes to `/maestro-planner <id>`. Answer nothing on it.
5. Settling a call that isn't the owner's: post the answer and move the ticket with the first two commands of [Unblock](#unblock). Otherwise write nothing.

```text
Short answer: <one or two sentences, outcomes first>
Facts: <one line each, with the command that shows it>
Call: <the pick and a one-line why> | owner's: What, Why now, Options (two or three, each with its trade-off), Default (one-line why), Question (one word if it can be)
Changed: <each ticket write, with the new status and role> | nothing
Next: <the exact command the chief of staff runs next> | none
```

**Reply:** the short answer, the facts, the call, what changed, then the next command.
