# 0431 Every write to a held ticket carries the lease token

- **Context:** 0203 makes `transition` and `comment` on a claimed ticket need `--holder` and `--token`. Phase 4 and 5 rewrote the skills and playbooks without them, so the merge would have shipped start steps that exit 2, handoff notes that exit 4, and an Operations Manager who can't set done.
- **Choice:** `maestro-claim` records `claim.token` in the scratchpad. Every `transition` and `comment` on a ticket the session holds passes `--holder <holder> --token <token>`, in runner **Run:** lines and in skills; `release` passes the token too. `maestro-handoff-note` passes `--actor-role <your role>` on every status move. Writes to tickets nobody holds pass neither. `evals/check.py` fails on a `transition` or `comment` without `--token`, unless the line is about tickets nobody holds (0434).
- **Alternatives:** Have the runner add the holder and token (sessions in skills call `maestro-ticket` directly, so it would cover half the writes, and the check couldn't see it).
- **Why:** A missing token is a hard failure on the bot's first write. Spelling it out where the command is written keeps it checkable.
