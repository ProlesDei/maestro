---
name: maestro-working-principles
description: How we work in a Maestro repo. One ticket, one worktree, one branch. Small commits and nothing left unstaged. Evidence before any claim. Stop and ask instead of guessing. Use it at the start of any coding session. Every playbook step names it too.
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Working principles

First, find out where you are:
- **In a playbook step.** Your prompt starts with "You are one step of a Maestro playbook". The engine has already claimed the ticket, made the worktree and taken the lease. It makes every ticket write. Do only the step. The rules in your launch flags beat this file. Skip "Start" and "Finish" below.
- **In your own session.** The user is driving. Start at "Start". To have the playbooks do the whole job, use `/maestro-work <id>` instead.

## Start (your own session)

1. **One ticket.** Use the one the user named. If they named none, take the top ready ticket from `maestro-ticket list --status todo --unclaimed --json` (sorted by priority, then id). If the work has no ticket, run `/maestro-create-ticket` first. Work without a ticket leaves no record for anyone else.
2. **Claim it** so that no bot takes it: `maestro-ticket claim <id> --holder <you>/<session> --keepalive $PPID --json`. The keepalive renews the lease while your session lives and lets it lapse when the session dies (decision 0240). Keep `claim.token`, because every later write on the ticket needs `--holder` and `--token`. If you get exit 4, someone else holds it. Don't work on it. If you get exit 2 on `--keepalive`, `$PPID` isn't your session: stop and say so rather than claim without one. `$PPID` is the session only in Claude Code (verified, 0240). In any other harness, first check that `ps -o comm= -p $PPID` names the harness. If it doesn't, stop: a keepalive tied to a short-lived wrapper lets the lease lapse while you work.
3. **One worktree, one branch.** `git worktree add ../<repo>-<id> -b maestro/<id> <main branch>`. The main branch is the one named in `.maestro/project/project-setup.md`. Never work in the main checkout, on another ticket's branch or in another worktree.
4. **Read before you change.** Read the ticket and its acceptance criteria. Read `.maestro/project/memory/` for the areas the ticket names. Find the closest existing code that already does the same kind of thing, and match it.

## While you work

- **Commit often.** Each commit has one reason and a message that says what changed and why. Before every pause, `git status --porcelain` prints nothing. Save half-done work as a `wip:` commit, never a stash: all worktrees share one stash, and other sessions pop it.
- **Stage by path.** Use `git add <paths>`, never `git add -A` without reading `git status` first. Never commit generated files, secrets or another session's files.
- **Stay in scope.** Touch only what the ticket needs. When you find a problem outside it, write it down as a possible new ticket and leave the code alone. No drive-by refactors, no formatting churn.
- **Evidence over claims.** A check you didn't run hasn't passed. Quote the command and its exit code. "Should work" is not a status.
- **Never discard work.** No `git reset --hard`, `git clean`, `--force` or `branch -D`. Never push, merge or deploy from a session: the runner or a human does that.
- **Secrets.** Name a secret only by its environment variable. If one appears in output, say "secret exposed in session output" without the value.
- **Stuck twice the same way?** Stop. Write down what happened, each thing you tried with its failing output line, two or three options, and the one question that unblocks you. Don't guess.
- **Kill switch.** If `$(git rev-parse --git-common-dir)/maestro/STOP` exists, stop now.

## Finish (your own session)

1. Run the checks listed under "Automated checks" in `project-setup.md`. Quote each result.
2. Prove every acceptance criterion with `/maestro-prove-complete`.
3. Leave the worktree clean, with everything committed on `maestro/<id>`.
4. Hand off with a `Handoff:` comment that gives the sha, the checks, the evidence paths and the risks: `maestro-ticket comment <id> --holder ... --token ...`. Then move the ticket on, or release your claim.
5. After the merge, remove the worktree with `git worktree remove <path>`, and only if it is clean.
