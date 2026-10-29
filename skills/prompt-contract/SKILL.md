---
name: prompt-contract
description: How to write every prompt you send a harness session and how to judge its reply; use it for each playbook step, including intake, claim, wrap-up and clean-up.
---

# Prompt contract

Every prompt you send has the same five parts, and every reply comes back in the same shape. That keeps prompts short, replies checkable and costs low.

## The five parts

1. **The goal.** One or two sentences: what this step must achieve.
2. **Pointers, not pasted content.** Name files, ticket ids, branches, the harness skill to load (`.maestro/skills/<name>/SKILL.md`). The harness reads them itself. Never paste file contents, diffs or logs into the prompt.
3. **Constraints and permissions.** What the harness may and may not do in this step: paths, branch, commands, the holder and expected version, the idempotency key `<session-id>:<step>`.
4. **What done means.** The check that proves the goal is met: an exit code, a status, a test count.
5. **Reply: short, structured, with evidence.** Ask for the reply block below and nothing else.

Every step prompt also says: add one line to the scratchpad; if the step may outlast the lease, run `maestro-ticket heartbeat <id> --holder <holder>` as you go, and stop if it exits `4`.

## Sessions

- Same session across a playbook's steps. Resume it for each step so the harness keeps its context.
- Fresh session per ticket. Never carry one ticket's session into the next.
- Stable content first, so it gets cached: team rules and role permissions in the launch flags, then the playbook's fixed wording, then the step's goal last. Don't put timestamps or ids in the stable part.

## The structured reply

One fenced block at the end of the reply. Where the harness supports `--json-schema`, pass the same fields as a JSON Schema.

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
3. If in doubt, check one fact yourself with a no-token command (for example `maestro-ticket get <id> --json`).

## Result

- Step done, more to do: next step.
- Not right, within budget: send a sharper prompt in the same session. Say exactly what was missing, narrow the scope, keep the same idempotency key for the same write.
- Stuck or over budget: set Blocked, then wrap up. Retry limits and budgets come from `.maestro/environment.md`.
- Playbook done: wrap up.

## Rules

- Secrets never go in prompts, replies or logs. Refer to them by env var name only. If a reply contains one, don't repeat it, don't log it, and flag it in the handoff note as "secret exposed in session output" without the value.
- One step per prompt, except wrap-up, which is batched.
- Never ask the harness to skip a check to meet the budget.
