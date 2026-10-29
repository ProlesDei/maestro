---
name: maestro-clean-up
description: Remove what this session created (processes, temp files, worktree, claim, scratchpad) after wrap-up; use it only when the harness has no cleanup hook.
disable-model-invocation: true
metadata:
  internal: true
---

# Clean up

You leave the repo the way the session found it, apart from the work you handed off. You remove only what this session owns, as recorded in its scratchpad. Running this twice must be safe: anything already gone counts as done.

## Inputs

- Ticket id and holder `<bot-id>/<session-id>`.
- Whether wrap-up succeeded.

The ticket may already be released: the handoff and escalate skills release it, which also stops its background heartbeat.

Paths below are relative to `$(git rev-parse --git-common-dir)/maestro/`. The scratchpad is `scratch/<bot-id>/<ticket-id>.md`.

## What you own

- The scratchpad, and the `temp_dir` it names, if that dir is under `scratch/<bot-id>/`.
- Processes listed under `processes` in the scratchpad.
- The worktree it names, if that path is under `worktrees/`.
- This holder's lease on the ticket.

Anything else is not yours, even if it looks like leftover work.

## Steps

Read the scratchpad first and delete it last, because it is the record of what you own. If it's missing, skip to step 4.

1. **Processes.** For each recorded PID, check `ps -p <pid> -o args=` still matches the recorded command. If it matches, `kill <pid>`. If it doesn't match or is gone, leave it.
2. **Temp files.** Delete the `temp_dir` and what's in it.
3. **Worktree.** Run `git -C <worktree> status --porcelain`.
   - Clean: `git worktree remove <worktree>`, then `git worktree prune`. The branch stays.
   - Not clean: keep it and report the path. Never use `--force`.
4. **Claim.** `maestro-ticket release <id> --holder <holder> --token <token> --idempotency-key <session-id>:release --json`, with the token from the scratchpad.
   - Exit `0`: released, or already released.
   - Exit `4`: another holder, or a newer claim of yours, has it now. That's fine; it's not yours.
5. **Scratchpad.** Delete it if wrap-up succeeded. If wrap-up failed, keep it so a later session can resume, and say so.

## Reply

```yaml
status: done
summary: cleaned up T-12; worktree removed, lease released
evidence:
  - kill 41822 (npm run dev) → exit 0
  - git worktree remove .git/maestro/worktrees/T-12 → exit 0
  - maestro-ticket release T-12 → exit 0
  - removed scratch/jse-1/T-12.md
kept: []
next: close the session
questions: []
```

Use `partial` if you kept anything, and list it under `kept` with the reason.

## Rules

- Never delete, kill or release anything outside what you own.
- Never edit tickets beyond releasing your own lease.
- A missing file, dead process or released lease is not an error.
