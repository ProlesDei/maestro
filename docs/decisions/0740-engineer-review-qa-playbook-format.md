# 0740 What the engineer, review and QA playbooks need from the engine beyond 0700

- **Context:** Slice 4 of 0700 converted the 12 playbooks for both engineers, the Code Reviewer and QA to YAML while slice 1 builds `maestro-play`. The 0700 contract doesn't say how a template reads a step's result, how a playbook matches a body line, or how a run hands a ticket to another role. These playbooks need all three.
- **Choice:** They use the 0700 keys, plus these. Slice 1 implements them, or the merge renames them in both places:

| Addition | Meaning | Used by |
|---|---|---|
| `intake.body`, `intake.latest` | a body line, or the latest comment, starts with the string: the positive form of `unless_body` and `unless_latest` | refactor, performance, take-escalation |
| `intake` as a list | the playbook takes a ticket that matches any entry (0436: sent-back work resumes the playbook that claimed it) | scout-investigation, verification-skill |
| `.steps.<id>.outcome` | the step's outcome (`ok`, `fail`, `done`, `failed`, …), `""` before it runs | review-change, verify-ticket |
| `.steps.<id>.family` | the model family a prompt step ran on | review-change |
| `.steps.<id>.<field>` on a `run` step | the runner's reply fields, as 0700 already reads `summary` and `output_path`; `install write --commit` gives `commit` | verification-skill |
| `.ticket.trailers` | each `Key: value` line in the ticket's comments, latest comment wins; read with `index`, so a missing key is `""`. `.ticket.tries` is an int | every playbook: `sha`, `author-family`, `review-family`, `tries-at-takeover` |
| `int`, `add` template functions | `int` turns a trailer into a number (`""` is 0) | senior fix-sent-back's Tries gate, review-change's numbered list |
| `mode:`, `vars:` on a prompt step | the template reads them as `.step.mode` and `.step.vars.<k>`; `vars` values are templates | baseline (`repro`, `characterise`, `trace`, `after`), verify (`draft`), draft-setup (`verify`) |
| `session: fresh` on a prompt step | a new harness session for this step only | verification-skill's proof |
| `done.to`, `done.role` | templates; `role` adds `--role` to the move | review-change, verify-ticket, scout-investigation |
| `block.to: <role>` | `owner` (the default) and `chief-of-staff` work as 0700 says. Any other role gets the ticket at `todo` with `--role <role>` and an `Escalation:` note, not `blocked`. `block.to` is a template | junior engineer (to the Senior), review-change (secrets to the Chief of Staff) |

  Step ids are lower-case words joined by `_`, because Go templates can't read `.steps.trace-before`. Each reply field a template reads is in that step's `out:`, so it exists, empty, before the step runs. `on: {x: end}` finishes the run through `done:`. Comments carry facts as trailer lines (`sha:`, `author-family:`) and never put reply text into a `ticket:` or `run:` command line.
- **Alternatives:** A prompt step that reads the handoff and replies the sha (tokens for a lookup, and the model could misread it); parsing each note's prose (breaks when a template changes); separate playbooks for each ending of review and QA (two near-copies each).
- **Why:** Every routing decision in these playbooks then reads a fact the engine already has, so it's deterministic and lint can check it.
