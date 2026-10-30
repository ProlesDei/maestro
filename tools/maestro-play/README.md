# maestro-play

Runs a playbook (`playbooks/<role>/<job>.yaml`) step by step: it claims the ticket, runs each step as a `maestro-ticket` call, a built-in request or a harness prompt, saves every output in the run state, and writes the handoff or escalation note itself. Sessions it starts never write tickets. Go, no cgo; build with `go build -o bin/maestro-play ./cmd/maestro-play`. `go test -race ./...` here also lints and renders `../../playbooks`.

```sh
maestro-play run engineer/work --ticket <id> --bot <bot-id>   # also: run <role> --next, run <role>/<job> with no ticket
maestro-play resume <run-id> [--choice <c> [--text <t>]]      # after a crash, a stop or an ask step
maestro-play render <role>/<job> --step <id|done|escalate> [--ticket <id> | --ticket-file <f>] [--env <f>]
maestro-play lint [<playbooks-dir>]
```

`run` and `resume` print one JSON object: `id`, `state`, `playbook`, `ticket`, `step`, `reason` (why it failed, blocked or was refused), and for `waiting` also `question`, `choices` and `evidence`. Exit codes: 0 done, 1 failed, 2 usage, 3 blocked, 4 lease lost, 5 idle, 6 waiting, 7 stopped (`<common>/STOP` exists). `render` prints what a step would send and writes nothing. `lint` checks the schema, step types, requests and their flags, ticket subcommands, `on:` targets, and every template against the data below.

Flags: `--playbooks` (`$MAESTRO_PLAYBOOKS`, else `./playbooks`), `--skills` (`$MAESTRO_SKILLS`), `--common` (`$MAESTRO_COMMON`, else `<git common dir>/maestro`), `--ticket-bin` (`$MAESTRO_TICKET_BIN`), `--harness claude|codex` (`$MAESTRO_HARNESS`, default claude). `MAESTRO_GIT_BIN`, `MAESTRO_GH_BIN`, `MAESTRO_CLAUDE_BIN` and `MAESTRO_CODEX_BIN` pick the binaries; `MAESTRO_MODEL_MID` and `MAESTRO_MODEL_FRONTIER` the models (claude default: sonnet, opus); `MAESTRO_CLAUDE_ARGS` and `MAESTRO_CODEX_ARGS` add session flags, such as `--permission-mode acceptEdits`. Without them, your own harness settings decide what a session may do.

## Built-in requests

A `run:` step names one of these, or a list of them run in order until one fails. Every reply has `status` (`ok`, `failed` or `refused`), `exit_code` and `output`, plus:

| Request | Runs | Reply fields |
|---|---|---|
| `worktree add <ticket> --from <branch>` | `git worktree add -b maestro/<ticket> .maestro/worktrees/<ticket> <branch>`; reuses the branch, or the worktree, if it exists | `path`, `branch` |
| `check all`, `check <name>` | each `` - <name>: `<command>` `` under `## Checks` in `.maestro/project/project-setup.md`, with `sh -c` in the worktree; all run, so every failure shows | `summary`, `checks` (name, command, exit_code, status), `output_path` |
| `head` | `git rev-parse HEAD` in the worktree | `sha` |
| `open pr [--title <t>] [--body <b>]` | `git push -u origin <branch>`, then `gh pr create --base <main branch>`, or returns the PR already open from this repo (a fork's PR from a branch of the same name doesn't count); fails if the branch has no commits beyond the main branch | `url`, `number` |

`check`, `head` and `open pr` fail while `git status --porcelain` in the worktree prints anything, and the output lists it: a check or sha must match what the branch holds.

A bad request (an unknown flag, a ticket that isn't a plain id) is `refused` and blocks the step. A failed one is outcome `exit:<n>`, which `on: {fail: ...}` catches; `.steps.<id>.status` is `failed`. `## Main branch` in the same file is `.project.main_branch` (default `main`).

Engine-only: `session start` runs `claude -p --output-format json --session-id <id> --json-schema <schema> --model <tier's model> --append-system-prompt <playbooks/rules.md>` with the prompt on stdin, in the worktree; `session resume` is the same with `--resume <id>`. The prompt is `_contract.md`, the skills `skills/use.yaml` names, then the step's template; the schema asks for `status` and `summary`, the `_contract.md` fields and the step's `out:` fields, and the reply is the result's `structured_output`. With `--harness codex`, or for a `family: not-author` step whose author's family is the harness's own, it is `codex exec --json -o <file>` (and `codex exec resume <thread>`), with the rules ahead of the first prompt and the schema at the end of each; the reply is the JSON in the last message. Codex's `--output-schema` takes only strict schemas, which typeless `out:` fields can't be. Every command and session gets `MAESTRO_RUN_ID`, and `run` refuses to start inside one.

## Template data and run state

Templates see `.ticket` (the `get --json` ticket plus `kind`, `latest` and `trailers`), `.project` (`name`, `summary`, `main_branch`, `setup`, `checks`), `.env` (`harness`, `family`), `.limits` (`step_visits` 5, `retries` 1), `.run` (`id`, `branch`, `worktree`, `scratch`, `evidence_dir`, `skipped`, ...), `.steps.<id>` (`status`, `outcome`, `output`, the reply fields above, `reply` for a prompt step), `.step.with`, `.item`/`.index` in an `each:` step, `.input.text`, `.cursor` and `.retry`. Functions: `json`, `join`, `int`, `add`, `index`, `quote`, `latest`, `line`, `latestLine`, `hasPrefix`, `contains`, `trim`, `lower`, `default`.

`<common>/runs/<run-id>.json` holds the lease, the cursor and every step's record; prompts, schemas and check output go in `<run-id>.d/`. `resume` gets the lease back and starts at the step that was running; writes reuse their idempotency keys, so a replay is safe. A done run deletes both and removes a clean worktree; the branch stays.
