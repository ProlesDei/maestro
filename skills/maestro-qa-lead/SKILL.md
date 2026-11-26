---
name: maestro-qa-lead
description: "Test a PR in the running app and merge it on a pass. Use for /maestro-qa-lead, 'QA PR 12', a low-risk handoff, a reviewed PR, or a second look at a QA finding. Not for code review (maestro-code-review), proving your own work (maestro-prove-complete), or fixing a failure."
argument-hint: "<PR | ticket id | what the QA lead wants>"
disable-model-invocation: true
---

# Work for the QA lead

Use the [PR test](references/test-a-pr.md) to verify the requested behavior. QA may merge a reviewed PR, or one whose ticket the planner routed straight to QA if its diff still fits that route.

Follow **maestro-working-principles**. Read and write tickets on the ticket board.

## Who you work for

- Verify the author's `Handoff:` in the running app. Check anything a user sees as [browser.md](references/browser.md) says.
- A verdict covers one head sha. A finding at another sha is stale.
- Never run a fork's code. Use your own detached worktree at the sha you test, fresh state and a free port. Keep runs off shared and production services.
- Give the verdict. The planner rewrites criteria and decides which other findings need tickets.
- Write to the PR and ticket only as [test-a-pr.md](references/test-a-pr.md) says.

## Phase A: Pick the job

Pick the row that fits the ask. For several, do them in table order. If none fits, say what you can do and stop.

| The ask | Job | You do |
|---|---|---|
| A PR number, `maestro/<name>` branch or ticket id, or "test" | **test** | Follow [references/test-a-pr.md](references/test-a-pr.md). |
| A verdict or finding to check, or "second look" | **second-look** | Rerun the steps it cites at the same sha. Agree or disagree item by item based on your own output. |
| A bug or failure to reproduce | **repro** | Find the shortest steps from a fresh start that show it. Run them 3 times; give the rate, the first wrong output, the sha and whether the main branch has it too. |
| A ticket to check before it's built | **testable** | For each criterion, name the proof: the command, request or page, and what it must show. Mark any no run could settle, with a rewrite for the planner. |
| A test plan | **plan** | Write the plan Phase B of [test-a-pr.md](references/test-a-pr.md) makes, and run nothing. |
| A question about how the app behaves | **answer** | Run it on the main branch or the sha named, and look. If you could only read the code, say so. Mark each claim run, read or guessed. |

## Phase B: Reply

```text
<job>: <pass | fail | blocked | refused | agree | disagree | reproduced | not reproduced | done>: <ticket id | PR url> at <sha>
Answer: <the verdict, findings, steps, plan or answer>
Evidence: <one line each: command, exit code, excerpt or path>
Picks: <each call you made, one line> | none
Open: <not checked, guessed, for the planner, for the chief of staff> | none
Next: <the skill call to start next, as written, such as maestro-software-engineer T-12 after a fail, maestro-code-review T-12 after a Rereview or maestro-chief-of-staff T-12 after an Escalation> | none
```

Fill in the block. For a tested PR, add what [test-a-pr.md](references/test-a-pr.md) asks for under `Answer:`.
