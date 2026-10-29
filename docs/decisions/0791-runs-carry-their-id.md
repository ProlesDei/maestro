# 0791 Every process a run starts carries MAESTRO_RUN_ID, and no run starts inside one

- **Context:** 0787 asks the runner to refuse `play` from any session a run started, "the engine marks its sessions, for example with `MAESTRO_RUN_ID`", but nothing set or checked it. A ticket body that says "run `maestro-play`" could reach a session that has a shell.
- **Choice:** The engine sets `MAESTRO_RUN_ID=<run-id>` on every runner call once the run has an id, and `docs/runner.md` has the runner pass it to every session it launches. `maestro-play run` and `resume` refuse to start when their own environment has it. A `run: play …` step fails lint and blocks at run time. Sessions are also denied `Bash(maestro-play *)` and every `maestro-ticket` write.
- **Alternatives:** A lock file per bot (a crashed run would block the bot's next one); the runner's own check only (the engine is also run from the user's sessions, without a runner).
- **Why:** An environment variable crosses every process boundary the run creates for free, and three independent checks cover the cases where one is skipped.
