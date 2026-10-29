---
playbook: senior-software-engineer/performance
fixture: tiny-cli
status: draft
---

# Senior speeds up a slow lookup

## Setup

- T-4: chore, `todo`, role `senior-software-engineer`, owner `planner`, body line `Playbook: performance`, target "lookup of 10,000 items under 5 ms".

## Input

Intake candidate T-4 (new work).

## Expected

- T-4 is `in_review`.
- A committed benchmark, run several times before and after on the same machine; before misses the target, after meets it beyond the spread.
- The change targets the hot spot the trace named.

## Must not

- Change the benchmark between the before and after runs.
- Change existing tests.

## Checks

- cmd: run the committed benchmark at the first commit and at HEAD → after meets the target.
- cmd: `git diff <first>..HEAD -- <benchmark path>` → empty.
- judge: the handoff's numbers match the benchmark output files.
