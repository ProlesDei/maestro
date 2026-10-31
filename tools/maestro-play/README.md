# maestro-play

Runs a playbook (`playbooks/<name>/playbook.yaml`, its step templates beside it) step by step: it claims the ticket, runs each step as a `maestro-ticket` call, a built-in request or a harness prompt, saves every output in the run state, and writes the handoff or escalation note itself. Sessions it starts never write tickets. Go, no cgo; build with `go build -o bin/maestro-play ./cmd/maestro-play`. `go test -race ./...` here also lints and renders `../../playbooks`.

```sh
maestro-play run work [--ticket <id>] [--bot <bot-id>]   # no --ticket: the next ticket intake matches
maestro-play resume <run-id>                             # after a crash or a stop
maestro-play render work --step <id|done|escalate> [--ticket <id> | --ticket-file <f>] [--env <f>]
maestro-play lint [<playbooks-dir>]
```

`run` and `resume` print one JSON object: `id`, `state`, `playbook`, `ticket`, `step`, `reason` (why it failed, blocked or was refused). Exit codes: 0 done, 1 failed, 2 usage, 3 blocked, 4 lease lost, 7 stopped (`<common>/STOP` exists; a step that would block while it does stops the run instead, and `resume` reruns that step). `render` prints what a step would send and writes nothing. `lint` checks the schema, step types, requests and their flags, ticket subcommands, `on:` targets, and every template against the data below.

`--bot` defaults to `$MAESTRO_BOT`, else `user`. Without `--ticket`, a playbook that claims takes the first unclaimed ticket its `intake:` entries match, entry by entry, skipping one it can't claim; it runs once and never waits for work.

Flags: `--playbooks` (`$MAESTRO_PLAYBOOKS`, else `./playbooks`), `--skills` (`$MAESTRO_SKILLS`), `--common` (`$MAESTRO_COMMON`, else `<git common dir>/maestro`), `--ticket-bin` (`$MAESTRO_TICKET_BIN`), `--harness claude|codex` (`$MAESTRO_HARNESS`, default claude). `MAESTRO_GIT_BIN`, `MAESTRO_GH_BIN`, `MAESTRO_CLAUDE_BIN` and `MAESTRO_CODEX_BIN` pick the binaries; `MAESTRO_MODEL_MID` and `MAESTRO_MODEL_FRONTIER` the models (claude default: sonnet, opus); `MAESTRO_CLAUDE_ARGS` and `MAESTRO_CODEX_ARGS` add session flags, split on spaces.

Sessions run headless, with no one to answer a permission prompt, so by default they may do anything: claude gets `--permission-mode bypassPermissions`, codex `--dangerously-bypass-approvals-and-sandbox`. To set limits, put your own permission flag in the args; then the engine adds none. Claude's: `--permission-mode`, `--dangerously-skip-permissions`, `--allowedTools`, `--disallowedTools`, `--restricted`, `--settings`. Codex's: `-s`/`--sandbox`, `--approve-for-me`, `--dangerously-bypass-approvals-and-sandbox`, `-p`/`--profile`, `-c sandbox_mode=...`, `-c approval_policy=...`. A settings file or profile counts because it can set permissions. For example `MAESTRO_CLAUDE_ARGS="--permission-mode acceptEdits"`. `codex exec resume` takes no `-s`, so limit codex with `-c`.

Timeouts, as Go durations (`90s`, `1h`; `0` is no limit): `MAESTRO_SESSION_TIMEOUT` (default `30m`) for one session call, `MAESTRO_CHECK_TIMEOUT` (`15m`) for each check, `MAESTRO_GIT_TIMEOUT` (`5m`) for each git or gh call. Each command runs in its own process group; on timeout the engine kills the group, the call exits 124 with output ending `timed out after <d>`, and the step fails as any other would: `on:`, retries, then escalation. When a command exits, the engine kills what it left in its group, so a check like `sleep 30 & echo ok` passes at once. maestro-ticket calls have no limit.

A command in its own process group can't read the terminal, so git and gh calls get `GIT_TERMINAL_PROMPT=0`, `GH_PROMPT_DISABLED=1` and `GIT_SSH_COMMAND=ssh -o BatchMode=yes`: a credential or ssh prompt fails the call at once instead of hanging until the timeout. An ssh command of your own (`GIT_SSH_COMMAND`, `GIT_SSH` or `core.sshCommand`) stays; add `-o BatchMode=yes` to it yourself. Set up credentials (a helper, an agent-loaded key, `gh auth login`) before a run.

## Built-in requests

A `run:` step names one of these. Every reply has `status` (`ok`, `failed` or `refused`), `exit_code` and `output`, plus:

| Request | Runs | Reply fields |
|---|---|---|
| `worktree add <ticket> --from <branch>` | `git worktree add -b maestro/<ticket> .maestro/worktrees/<ticket> <branch>`; reuses the branch, or the worktree, if it exists | `path`, `branch` |
| `check all`, `check <name>` | each `` - <name>: `<command>` `` under `## Checks` in `.maestro/project/project-setup.md`, with `sh -c` in the worktree; all run, so every failure shows. Any other bullet there fails every check request, naming the line; prose (`None yet.`) is skipped. `check all` with no checks listed (or no file) passes, with summary `no checks listed` | `summary`, `checks` (name, command, exit_code, status), `output_path` |
| `head` | `git rev-parse HEAD` in the worktree | `sha` |
| `open pr [--title <t>] [--body <b>]` | `git push -u origin <branch>`, then `gh pr create --base <main branch>`, or returns the PR already open from this repo (a fork's PR from a branch of the same name doesn't count); fails if the branch has no commits beyond the main branch | `url`, `number` |

`check`, `head` and `open pr` fail while `git status --porcelain` in the worktree prints anything, and the output lists it: a check or sha must match what the branch holds.

A bad request (an unknown flag, a ticket that isn't a plain id) is `refused` and blocks the step. A failed one is outcome `exit:<n>`, which `on: {fail: ...}` catches; `.steps.<id>.status` is `failed`. `## Main branch` in the same file is `.project.main_branch` (default `main`).

Engine-only: `session start` runs `claude -p --output-format json --session-id <id> --json-schema <schema> --model <tier's model> --append-system-prompt <playbooks/rules.md>` with the prompt on stdin, in the worktree; `session resume` is the same with `--resume <id>`. The prompt is `_contract.md`, the skills `skills/use.yaml` names, then the step's template; the schema asks for `status` and `summary`, the `_contract.md` fields and the step's `out:` fields, and the reply is the result's `structured_output`. With `--harness codex`, or for a `family: not-author` step whose author's family is the harness's own, it is `codex exec --json -o <file>` (and `codex exec resume <thread>`), with the rules ahead of the first prompt and the schema at the end of each; the reply is the JSON in the last message. Codex's `--output-schema` takes only strict schemas, which typeless `out:` fields can't be. Every command and session gets `MAESTRO_RUN_ID`, and `run` refuses to start inside one.

## Template data and run state

Templates see `.ticket` (the `get --json` ticket plus `kind`, `latest` and `trailers`), `.project` (`name`, `summary`, `main_branch`, `setup`, `checks`), `.env` (`harness`, `family`), `.limits` (`step_visits` 5, `retries` 1), `.run` (`id`, `branch`, `worktree`, `scratch`, `evidence_dir`, `skipped`, ...), `.steps.<id>` (`status`, `outcome`, `output`, the reply fields above, `reply` for a prompt step), `.step.with`, `.input.text` and `.retry`. Functions: `json`, `join`, `int`, `add`, `index`, `quote`, `latest`, `line`, `latestLine`, `hasPrefix`, `contains`, `trim`, `lower`, `default`.

`<common>/runs/<run-id>.json` holds the lease, the next step and every step's record; prompts, schemas and check output go in `<run-id>.d/`. `resume` gets the lease back and starts at the step that was running; writes reuse their idempotency keys, so a replay is safe. A done run deletes both and removes a clean worktree; the branch stays.
