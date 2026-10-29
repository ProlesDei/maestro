# maestro-play

The playbook engine (decision 0700). A playbook is a YAML file of typed steps under `playbooks/<role>/<job>.yaml`. The engine claims the ticket, runs each step as a `maestro-ticket` call, a runner request or a harness prompt, keeps every output in the run state, and does the wrap-up, the escalation and the clean-up itself. It's the runner's `play` request (`docs/runner.md`).

Go, single static binary, no cgo. One dependency: `gopkg.in/yaml.v3`.

## Build, test, install

From `tools/maestro-play/`:

```sh
go build -o bin/maestro-play ./cmd/maestro-play   # build
go test -race ./...                               # unit and fake-binary tests, and the repo's playbooks lint
gofmt -l . && go vet ./...                        # lint
go install ./cmd/maestro-play                     # put maestro-play on $GOBIN
```

`go test ./...` is the playbook check: `lint_test.go` lints `../../playbooks`, its prompt templates and `../../skills/use.yaml`.

## Commands

```sh
maestro-play run <role>/<job> --ticket <id> --bot <bot-id> [--input <text>]
maestro-play run <role>[/<job>] --next --bot <bot-id>
maestro-play run <role>/<job> --bot <bot-id> [--input <text>]   # claim: false, no ticket
maestro-play resume <run-id> [--choice <c> [--text <t>]]
maestro-play render <role>/<job> --step <id> [--ticket <id> | --ticket-file <f>] [--env <f>] [--input <t>]
maestro-play lint [<playbooks-dir>]
```

Every command takes `--playbooks` (`$MAESTRO_PLAYBOOKS`, else `./playbooks`), `--skills` (`$MAESTRO_SKILLS`, else `<playbooks>/../skills`), `--common` (`$MAESTRO_COMMON`, else `$(git rev-parse --git-common-dir)/maestro`), `--ticket-bin` (`$MAESTRO_TICKET_BIN`, else `maestro-ticket`) and `--runner-bin` (`$MAESTRO_RUNNER_BIN`, else `maestro-runner`).

`run` and `resume` print one JSON object, the `run:` block of the runner's `play` reply: `id`, `state`, `playbook`, `ticket`, `step`, `reason`, and for a waiting run `question`, `choices` and `evidence`.

| Exit | `state` | Meaning |
|---|---|---|
| 0 | `done` | the run finished (through `done:` or `end`); lint: no problems |
| 1 | `failed` | the run couldn't start or go on (preflight, a claim conflict, a bad playbook); lint: problems |
| 2 | | usage |
| 3 | `blocked` | the run escalated and released the ticket |
| 4 | `lost` | the lease was lost; nothing more was written |
| 5 | `idle` | `--next` found nothing to do |
| 6 | `waiting` | an `ask` step waits for `resume --choice` |
| 7 | `stopped` | the stop file exists; `resume` continues later |

`run --ticket` checks only the ticket's status against the intake, so a bot can resume a ticket that waits on someone. `render` is a dry run for evals: it prints a prompt step's full prompt, a ticket or run step's arguments, a gate's value or an ask's question, and writes nothing. `--step done` renders the handoff note, `--step escalate` the escalation prompt. Steps that haven't run have empty records.

## The playbook format

The format, the step types and keys, the template data and functions, and the built-in `done`, `end`, `block` and clean-up are in [playbooks/README.md](../../playbooks/README.md), the one place they're written down. `playbook/` holds the schema and `engine/lint.go` checks it.

## Run state and resume

`<common>/runs/<run-id>.json` holds the holder, the token, the cursor, each step's record, the skips and the wrap-up parts done. It's saved before and after every step. Prompt and schema files go in `<common>/runs/<run-id>.d/`. Both go after a `done` run; a blocked or lost run keeps them for housekeeping.

`resume` re-runs preflight, gets the lease back (a heartbeat with the old token and a new `maestro-ticket keepalive` for this process if the lease is live, else a new claim with `--keepalive`; another holder means `lost`), and starts at the step that was running. Idempotency keys are `<run-id>:<step>`, with `:<n>` from a step's second visit on, so a loop never replays an old write. A replayed write the store refuses as `idempotency_conflict` after a re-claim landed before the crash and counts as done. `run --next` resumes the bot's oldest crashed or stopped run of the role first. A process holds `<run-id>.d/lock` (an flock, freed if it dies) while it drives a run, so `resume` of a run another process is driving fails, and `--next` passes over it.

## The runner, as the engine calls it

`docs/runner.md` ("Playbook runs") gives the command line, the session flags, the reply fields and `MAESTRO_RUN_ID`, which the engine sets on every runner call once a run has an id. `run` and `resume` refuse to start while their own environment has it, so a session a run started can never start another run (decision 0787). `refused` blocks the step; `stopped` stops the run. Preflight's reply is the environment: `harness`, `family`, `project`, `limits` and the rest.

## Layout

| Path | What |
|---|---|
| `playbook/` | The schema, the runner request table and the ticket subcommands playbooks may run. No I/O beyond reading files. |
| `engine/` | Run, resume, render, lint, run state, templates. Tests use fake `maestro-ticket` and runner binaries (`fake_test.go`) and the playbooks in `engine/testdata/repo`. |
| `cli/` | Flags and output. |
| `cmd/maestro-play/` | `main`. |
