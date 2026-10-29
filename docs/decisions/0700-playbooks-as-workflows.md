# 0700 Playbooks are workflows run by a small engine; Maestro ships no skills

- **Context:** Paul: playbooks read like skills, and should be deterministic, like scripts with templated prompts. Skills should be kept to a minimum, because `skills/` must be an empty slot for his own. Today there are 28 Markdown playbooks (61 harness, 60 runner and 15 bot steps), and the bot follows them by hand. There are 27 skills (23 harness, 4 bot), plus 2 templates and 24 `agents/openai.yaml` files.
- **Choice:** Playbooks become YAML files run by `maestro-play`, a small Go engine. Prompt steps render Go `text/template` files and send them to the harness. Maestro ships **0 skills**. Each skill's procedure becomes a prompt template, an engine step or a ticket-tool flag. Playbooks never name a skill; Paul's skills plug in through an optional list (B3).
- **Alternatives:** Plain shell scripts, Go code per playbook, or keeping Markdown with a stricter check (A1).
- **Why:** The runner can follow a script exactly, skips get logged without anyone remembering to, and the format check can lint every step. It also leaves Paul a clean `skills/` folder.
- **Status: needs Paul** (D2, questions 1 and 2).

## A. The playbook format

### A1. YAML plus an engine, not shell or Go scripts

| | YAML + `maestro-play` (chosen) | Shell scripts | Go per playbook |
|---|---|---|---|
| Trust and allowlist | every step is a typed runner request, checked against `docs/runner.md` | any line can run anything, so it bypasses the allowlist | safe, but every change is a code change |
| Lint before it runs | `maestro-play lint` checks step types, template variables, outcome targets and runner names | can't check prompts or replies | the compiler checks it |
| Who can change it | Ops' improvement loop edits data and trials it on evals | needs shell quoting expertise | needs a Go build and a release |
| Dependencies | `gopkg.in/yaml.v3`: pure Go, no transitive deps (question 1). Fallback: the same schema in JSON with `encoding/json` | none | none |

Templates and conditions both use Go `text/template` with `missingkey=error`, so there is one expression language and it's in the standard library. Each step id has an empty record from the start, so a template can refer to a step that hasn't run yet, and a misspelled id fails lint.

### A2. The engine (`tools/maestro-play/`, Go 1.26, no cgo)

`maestro-play run <role>/<job> (--ticket <id> | --next) --bot <bot-id>`, `resume <run-id> [--choice <c> --text <t>]`, `render <role>/<job> --step <id> --ticket <id>` (dry run, for evals), `lint <dir>`. It's the runner's new `play` request. Every step it runs is checked against the role's allowlist and trust level, like any other request (0011, 0403).

A run goes: `preflight`; intake (`--next` picks the first playbook whose `intake` matches, in category order request, sent-back, new-work, scheduled); `claim` (the token, the holder and every output go into the run state at `<common>/runs/<run-id>.json`, which replaces the scratchpad); the steps; `done` or `block`; clean-up and `session close`. After a crash, `resume` starts again at the first unfinished step, with the same idempotency keys.

| Step key | Does | Outcomes for `on:` |
|---|---|---|
| `ticket: <subcommand args>` | `maestro-ticket`. The engine adds `--json`, `--holder`, `--token`, `--actor-role` and `--idempotency-key <run>:<step>`, and re-reads the version. | `ok`, `fail`, `exit:N`. Exit 4 because the lease was lost always stops the run, with no more writes. |
| `run: <runner request>` | any request from `docs/runner.md` (`worktree add`, `check all`, `trust`, `open pr`, …) | `ok`, `fail`, `exit:N` |
| `prompt: <template>` | Steps in order: `usage check` (exit 2 means block); render `_contract.md`, then the configured skills, then the template; `session start` or `resume` through `maestro-usage run` (`model:` tier, `family: not-author` for review); check the reply against the base schema plus `out:` | `done`, `partial`, `blocked`, `failed` |
| `gate: <template that renders true/false>` | a deterministic check: a trust level, a sha match or a reply field | `ok`, `fail` |
| `wait: {step: <id>, until: <tmpl>, every: 1m, timeout: 30m}` | re-runs a `run` or `ticket` step until `until` is true | `ok`, `timeout` |
| `ask: {question: <tmpl>, choices: [a, b]}` | pauses the run (`status: waiting`) and hands the bot the question, with pointers to the evidence. The bot answers with `resume --choice` | each choice |

Every step can also take these keys:
- `when:` skips the step when it renders false, and the engine records `Skipped: <id> — <rendered reason>` itself.
- `on: {outcome: <step id> | next | block | end}`. By default `ok` and `done` go to `next`, and anything else goes to `block`.
- `retry: {max, tier_up}`: same session, with `.retry.reason`. The reason is a schema miss or missing evidence, so a "sharper prompt" is just the template re-rendered.
- `out: [fields]`: the reply fields the step needs.
- `.limits.step_visits` from preflight caps loops, and passing it means `block`.

Wrap-up and clean-up are built in, so no playbook repeats them:
- **`done`** writes the reply's optional `memory` field to the memory inbox, posts the playbook's `note` template as a `Handoff:` comment, moves the ticket to `to` with `--release`, and appends the log line using the reply's `retro`.
- **`block`** renders `_escalate.md` in the same session, then posts `Escalation:` (or `Escalation (chief-of-staff):` with `block: {to: chief-of-staff}`), moves the ticket to blocked for the owner, and releases it.
- **Clean-up** kills the PIDs that replies listed under `processes`, but only while their command still matches. It removes the worktree if it's clean, and deletes the run state after a success.

### A3. Worked example: `senior-software-engineer/build-ticket`

`playbooks/senior-software-engineer/build-ticket.yaml`:
```yaml
name: build-ticket
role: senior-software-engineer
description: Build a complex or risky ticket on its own maestro/ branch, with tests for each criterion, and submit it for review.
intake: {category: new-work, status: todo, kinds: [feature, chore, docs],
         unless_latest: "Escalation:", unless_body: "Playbook: "}
claim: true                       # engine claims, keeps the token, heartbeat starts
session: ticket                   # one harness session for all prompt steps; fresh per ticket
steps:
  - id: start
    ticket: transition {{.ticket.id}} --to in_progress --from todo
  - id: worktree
    run: worktree add {{.ticket.id}} --from {{.project.main_branch}}
  - id: plan
    prompt: plan
    model: frontier
    out: [plan, risks]
  - id: build
    prompt: build
    model: frontier
    retry: {max: 1, tier_up: false}
    out: [commit, files, tests]
  - id: checks
    run: check all
    on: {fail: build}             # failing checks aren't a try; step_visits caps the loop
  - id: verify
    prompt: verify
    model: mid
    out: [criteria, failures]
    on: {failed: build}
done:
  to: in_review
  note: |
    Handoff: built on {{.run.branch}} at {{.steps.build.reply.commit}}.
    Checks: {{.steps.checks.summary}}
    Criteria:{{range .steps.verify.reply.criteria}}
    - {{.}}{{end}}
    Risks: {{.steps.plan.reply.risks}}
    {{.run.skipped}}Tries: {{.ticket.tries}}
    author-family: {{.env.family}}
```

`playbooks/prompts/_contract.md` comes first in every prompt and never changes, so it gets cached:
```
You are one step of a Maestro playbook in a headless session. Your rules are in the launch flags.
- Do only this step. Read files, tickets and branches yourself; nothing is pasted for you.
- Evidence is something you ran or read: command → exit code, test counts, paths. Never claim a check you didn't run.
- If you can't go on, reply blocked with the exact question. Don't guess.
- List any process you start under processes, with its PID and command.
- Reply with the JSON the schema asks for. retro: one line on what slowed you down.
  memory: only a lasting fact, with the files it's about; otherwise leave it out.
```

`playbooks/prompts/plan.md`:
```
# Plan
Read; edit nothing. Decide how the ticket gets done, so the build step only has to follow it.
1. Read the ticket (`maestro-ticket get <id> --json`) and the `.maestro/project/memory/` entries for the areas it names.
2. Find the closest thing the code already does the same way, by path.
3. Write the plan to the scratch file under `## Plan`: the files and the approach; each acceptance criterion
   with the test or check that proves it; each risk (callers, data, security, performance) with how it's covered.
4. If nobody can prove a criterion, or the plan needs a decision the ticket doesn't make: blocked.
Reply fields: plan (the path), risks (one line each).
## This step
Ticket {{.ticket.id}}, worktree {{.run.worktree}}, scratch {{.run.scratch}}.{{if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
```

`playbooks/prompts/build.md`:
```
# Build
Make the change the plan asks for, prove it with tests, and leave the worktree clean.
1. `git rev-parse --abbrev-ref HEAD` in the worktree must print the branch below. If not: failed.
2. Change what the plan says, the way its example does.
3. Give each acceptance criterion a test that fails without the change. Run the test command from
   "Automated checks" in `.maestro/project/project-setup.md` until it passes.
4. `git add` only your files, then commit, saying what changed and why. `git status --porcelain` prints nothing.
5. Touch only what the ticket needs. Never skip or weaken a test. If it needs a dependency the ticket doesn't ask for: blocked.
Reply fields: commit, files, tests.
## This step
Ticket {{.ticket.id}}, branch {{.run.branch}}, worktree {{.run.worktree}}, plan in {{.run.scratch}}.
{{- with .steps.checks.output_path}} Checks failed; start from {{.}}.{{end}}
{{- with .steps.verify.reply.failures}} Verify failed: {{json .}}.{{end}}
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
```

`playbooks/prompts/verify.md`:
```
# Verify
Show from the running app whether the ticket does what it says. Reading code is not proof. Fix nothing.
1. Read the acceptance criteria and the definition-of-done rows for this kind in `.maestro/project/project-setup.md`.
   If `.maestro/project/verify.md` exists, follow it.
2. Check each one the way it says. Command: the exit code and test counts. Evidence: run, call or click the real app,
   and save excerpts to the evidence dir as <n>-<slug>.<ext>. Judgment: what you looked at and what you concluded.
   Rows that review, QA or the merge queue prove are `later`.
3. Try empty and wrong input, limits, error paths, and one nearby feature.
4. Anything you couldn't check is `not checked`, never a pass. Stop what you started.
Reply fields: criteria (one line each: id, pass | fail | later | not checked, evidence path), failures
(steps, expected, actual, path). Status is failed if any criterion failed.
## This step
Ticket {{.ticket.id}} ({{.ticket.kind}}), worktree {{.run.worktree}}, evidence dir {{.run.evidence_dir}}.{{if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
```

`playbooks/prompts/_escalate.md`, which the built-in `block` uses:
```
# Escalate
This playbook can't go on. Commit work in progress on {{.run.branch}}; never discard it.
Reply fields: happened (one or two sentences), tried (each attempt and its failing output line),
options (two or three, with trade-offs), recommendation, question (one word to answer, if you can).
## This step
Stopped at {{.run.stopped_at}}: {{.run.stop_reason}}
```

Fixed text goes first and the `## This step` variables go last, so each prompt's stable prefix gets cached. Model IDs never appear: the runner maps tiers to models.

## B. The skills cut: 27 to 0

| Skill | Becomes | Why |
|---|---|---|
| `runner` (bot) | delete; 5 lines in `team-rules.md`: start with `play`, answer `ask`, treat `refused` and `stopped` as stops | the engine now sends the requests |
| `prompt-contract` (bot) | `prompts/_contract.md` + the reply schema in the engine | the engine composes the prompt and judges the reply |
| `model-selection` (bot) | the engine: `model:`, `retry.tier_up`, `family: not-author` | tier choice is a fixed rule |
| `talk-to-user` (bot) | merge into `chief-of-staff/persona.md` | only one bot uses it, and it's about character, not a procedure |
| `maestro-check-intake` | the engine (`--next`), playbook `intake:` headers, and `maestro-ticket list --latest-prefix` | queue order and prefix matching are deterministic |
| `maestro-claim` | the engine (`claim: true`); the run state replaces the scratchpad | one CLI call plus bookkeeping |
| `maestro-handoff-note` | the engine's `done:` + its `note` template | comment, transition, release |
| `maestro-escalate` | the engine's `block` + `_escalate.md` | only the note's content needs judgment |
| `maestro-log-entry` | the engine writes the line; `retro` is a reply field | appending a JSON line |
| `maestro-clean-up` | the engine's clean-up | it already knows everything it created |
| `maestro-propose-memory` | the reply's `memory` field; the engine writes the inbox | the content is judgment, the write isn't |
| `maestro-plan-change` | `prompts/plan.md` (bug root cause and infrastructure as `{{if}}` blocks) | it's one step's procedure |
| `maestro-capture-baseline` | `prompts/baseline.md` (`mode:` repro, characterise or trace) | step procedure |
| `maestro-build-change` | `prompts/build.md` | step procedure |
| `maestro-verify` | `prompts/verify.md` | step procedure |
| `maestro-read-feedback` | `prompts/feedback.md` | step procedure |
| `maestro-review-change` | `prompts/review.md`; pass or send back becomes a `gate` on `must_fix` being empty | the procedure is judgment, the decision isn't |
| `maestro-investigate` | `prompts/investigate.md` | step procedure |
| `maestro-write-report` | delete: the reply fields of `investigate` or `plan` go into the engine's `Report:` note template | posting a comment is deterministic |
| `maestro-write-tickets` | `prompts/tickets.md` drafts the tickets as reply fields; the engine runs `maestro-ticket create` with keys | drafting is judgment, the writes aren't |
| `maestro-groom-board` | `prompts/groom.md` | step procedure |
| `maestro-check-release` | `gate` steps: latest note `Handoff: QA passed`, head sha equals QA's sha, a review on another family, rollback set in setup | every check is a comparison |
| `maestro-analyse-logs` | numbers from runner `logs` and `maestro-usage report`; `prompts/patterns.md` for the one pattern | split the counting from the judgment |
| `maestro-propose-change` | `prompts/propose.md`; the engine creates the proposal ticket | step procedure |
| `maestro-curate-memory` | `prompts/curate.md` after runner `memory scan` | step procedure |
| `maestro-housekeeping` | delete: runner `housekeep` checks the paths and ticket states itself | path and status rules are deterministic |
| `maestro-draft-setup` | `prompts/draft-setup.md` (`mode:` setup, dod or verify) | step procedure |

The two `_template/` folders and the 24 `agents/openai.yaml` files are deleted. `skills/harness/rules.md` moves to `playbooks/rules.md`, and the engine passes it in the launch flags from there, so it's never installed. That leaves 13 step templates plus 2 partials in `playbooks/prompts/`. **Final count: 0 Maestro skills.** Nothing the playbooks or the ticket tool do needs one.

### B3. The plug-in point for Paul's skills

- `skills/<name>/SKILL.md`, flat, in any format the harnesses read. `skills/README.md` explains the slot and nothing else.
- `skills/use.yaml` is optional. If it's missing or empty, nothing gets named:

```yaml
prompts:                  # harness skills, named at the top of a rendered prompt, the harness's way (docs/runner.md)
  all: []                 # every prompt step
  build: [tdd]            # by template name, never by playbook
  review: [deep-review]
bots:                     # bot skills, added to a bot's instructions at bot setup
  all: []
  senior-software-engineer: [architecture]
```

- The engine looks up `prompts.all` plus `prompts.<template>` and names those skills after `_contract.md`. The installer copies only the skills listed under `prompts` into each harness's skill folder (0143), and bot setup adds the ones under `bots`. Playbooks and personas never name a skill, so Paul can add, rename or remove skills without editing either. `maestro-play lint` fails on any listed skill that has no folder.

## C. Migration: 6 parallel slices, 20 minutes each

All slices start from `int/p2a-p45` (D1). This file is the shared contract, so no slice waits on another until the merge.

| # | Slice | Owns |
|---|---|---|
| 1 | Engine: the `run`, `resume`, `render` and `lint` commands, run state and the six step types; tests use fake `maestro-ticket`, runner and harness binaries; a Go test lints `../../playbooks` | `tools/maestro-play/**` |
| 2 | Ticket tool: `list --latest-prefix P` and a documented order (priority, then id); add `play` to the runner contract and replace the skill-naming row with `use.yaml` | `tools/maestro-ticket/{cli,sqlitestore}/**`, `tools/maestro-ticket/README.md`, `docs/runner.md` |
| 3 | Templates: write the 15 files in `playbooks/prompts/` by mining the skill bodies; move `rules.md` and drop its skill rules; delete `skills/harness/**` | `playbooks/prompts/**`, `playbooks/rules.md`, `skills/harness/**` |
| 4 | Convert the 12 playbooks for both engineers, the reviewer and QA to `.yaml`; their golden tasks name step ids | `playbooks/{senior,junior}-software-engineer/**`, `playbooks/{code-reviewer,qa-lead}/**`, `evals/` for the same four roles |
| 5 | Convert the 16 playbooks for the Planner, Chief of Staff and Ops (`ask` for judgment, `gate` for trust levels, `wait` for PR status) | `playbooks/{planner,chief-of-staff,operations-manager}/**`, `evals/` for the same three roles |
| 6 | Docs, check, bots: `skills/README.md` and `use.yaml`; delete `skills/bot/**`; merge `talk-to-user` into the CoS persona; take skill names out of the personas and `team-rules.md`; rewrite `playbooks/README.md` and `_template/`; update `evals/README.md`, `README.md` and `AGENTS.md` | `skills/README.md`, `skills/use.yaml`, `skills/bot/**`, `bot-profiles/**`, `playbooks/README.md`, `playbooks/_template/**`, `evals/check.py`, `evals/README.md`, `README.md`, `AGENTS.md` |

`evals/check.py` keeps the checks for links, repo paths, README embeds, golden-task frontmatter and coverage, which it finds by globbing `playbooks/*/*.yaml`. Its checks for step format, the index, runner names, skills and ticket writes move to `maestro-play lint`, because Python here has no YAML parser and the engine owns the schema. AGENTS.md adds `go test ./...` in `tools/maestro-play/` as the playbook check. Merge order: 2, 1, 3, then 4 and 5, then 6. Gate: both checks pass, and `maestro-play render` of every prompt step succeeds on the `tiny-cli` fixture.

## D. Conflicts and open questions

### D1. What this changes

- **HANDOFF, "Two kinds of skills"**, the "Missing harness skills" item and "Prompts name harness skills": replaced. Decisions 0401, 0404 and 0421 are superseded, 0500 and 0508 are replaced by A2, and 0427 is narrowed to Markdown.
- **"Bots prompt, harnesses work"**: bots no longer write prompts; templates do. Bots still never touch files. They start runs, answer `ask` steps and handle escalations.
- **0431**: the engine adds `--holder` and `--token` to every ticket write. 0431 rejected that option because skills called `maestro-ticket` directly. No skill does now, so the engine covers every write.
- **The install footprint (Phase 7)** gets smaller: no generated Maestro skills, and `rules.md` is passed from Maestro, not installed. Only skills Paul lists get copied.
- **Base:** this branch predates `int/p2a-p45` (0430 to 0436: ship and scout types, tokens, the store counting tries), so migrate on top of that branch.
- **Unchanged:** the runner allowlist, trust gates, statuses, Tries, the escalation chain and one golden task per playbook.

### D2. Questions only Paul can answer

1. Can we add `gopkg.in/yaml.v3` (pure Go, no dependencies of its own)? If not, playbooks use the same schema in JSON.
2. Should the wake loop start runs for engineers, the reviewer, QA and Ops itself, with zero tokens, and wake a bot only at `ask` and `block`? Recommended: yes.
3. Should `skills/use.yaml` be global only, or should `.maestro/project/skills.yaml` be able to override it per project? Default: global only.
