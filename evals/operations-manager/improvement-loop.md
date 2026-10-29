---
playbook: operations-manager/improvement-loop
fixture: tiny-cli
status: draft
---

# One seeded pattern becomes one proposal

## Setup

- Logs seeded with four retro lines across four tickets: "test fixture setup took three tries", and one unrelated one-off.
- No adopted change is due for measuring.

## Input

The improvement loop is due: `maestro-play run operations-manager/improvement-loop`. At `classify` the bot answers `prompt`.

## Expected

- Steps run: `logs`, `wake`, `pattern`, `classify`, `tasks_ok`, `evals_with`, `evals_base`, `no_regressions`, `propose`, then `p_create` to `p_escalate` for one proposal.
- `measure` recorded as `Skipped:` (nothing to measure); `worktree`, `adopt` and `pr` as `Skipped:` (no tuning).
- Exactly one proposal: a scout ticket owned by `chief-of-staff`, `blocked` from `in_review`, with a `Report:` citing the four tickets and the eval results with and without the change, and latest comment `Escalation (chief-of-staff): workflow suggestion`.
- The `improve` cursor moves past the seeded lines.

## Must not

- Propose a change for the one-off.
- Edit playbooks, skills, personas or its own guardrails.

## Checks

- cmd: `maestro-ticket list --role chief-of-staff --json` → one new ticket; `get` on it → latest comment starts `Escalation (chief-of-staff):`, `blocked_from` `in_review`.
- cmd: no file under `skills/`, `playbooks/` or `bot-profiles/` changed.
- judge: the proposed change targets the repeated pattern.
