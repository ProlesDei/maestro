---
name: prompt-contract
description: How to write every prompt you send a harness session and how to judge its reply; use it for each playbook step, including intake, claim, wrap-up and clean-up.
metadata:
  internal: true
---

# Prompt contract

Every prompt you send has the same five parts, and every reply comes back in the same shape. That keeps prompts short, replies checkable and costs low.

## The five parts

1. **The goal.** One or two sentences: what this step must achieve.
2. **Pointers, not pasted content.** Name files, ticket ids, branches and the harness skill for the step. Send the skill name with the `session` request; the runner names it the way the harness expects. The harness reads everything itself. Never paste file contents, diffs or logs into the prompt.
3. **Constraints and permissions.** What the harness may and may not do in this step: paths, branch, the holder and expected version, the idempotency key `<session-id>:<step>`.
4. **What done means.** The check that proves the goal is met: an exit code, a status, a test count.
5. **Reply: short, structured, with evidence.** Ask for the reply block below and nothing else.

Every step prompt also says: add one line to the scratchpad. Never ask a session to run `maestro-ticket heartbeat`: the claim keeps the lease alive in the background. If a ticket write exits `4` because the lease was lost, the session stops and replies `failed`.

## Sessions

- Same session across a playbook's steps. Resume it for each step so the harness keeps its context.
- Fresh session per ticket. Never carry one ticket's session into the next.
- Stable content first, so it gets cached: the rules and permissions are in the launch flags, then the playbook's fixed wording, then the step's goal last. Don't put timestamps or ids in the stable part.

## The structured reply

The runner passes the reply schema to harnesses that support one. Otherwise ask for one fenced block at the end of the reply.

```yaml
status: done | partial | blocked | failed
summary: one or two sentences
evidence:
  - command run → exit code (and test counts)
  - file paths changed or read
  - ticket id and version after the write
next: what should happen next
questions: []   # only what a person or another role must answer
```

- `done`: goal met, evidence attached.
- `partial`: progress made, more to do; `next` says what.
- `blocked`: can't go on without something outside this session.
- `failed`: tried and it didn't work; evidence shows the error.

Skills may add fields (for example `candidates`, `claimed`, `kept`).

## Judge the reply

1. **Goal met?** Compare against what done means, not against the summary.
2. **Evidence included?** Each claim needs proof: "tests pass" without a command and exit code is not done. Missing evidence means not done.
3. If in doubt, check one fact through the runner (for example `ticket get <id>`).

## Result

- Step done, more to do: next step.
- Not right, within budget: send a sharper prompt in the same session. Say exactly what was missing, narrow the scope, keep the same idempotency key for the same write.
- Stuck or over budget: wrap up with `maestro-escalate`.
- Playbook done: wrap up.

## Wrap-up

One batched prompt, naming these skills in order:

1. `maestro-propose-memory`, only when there's something durable to keep.
2. `maestro-handoff-note` to hand the ticket on, or `maestro-escalate` when someone above you must act.
3. `maestro-log-entry`: the retro and the log line.

Then clean up: the harness's cleanup hook if it has one, else `maestro-clean-up`, then `session close`.

## Rules

- One step per prompt, except wrap-up.
- Never ask the harness to skip a check to meet the budget.
