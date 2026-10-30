---
name: maestro-working-principles
description: "Use at the start of any coding session in a Maestro repo, and in every Maestro playbook step. One ticket, one worktree, one branch; small clean commits; evidence before any claim; stop and ask instead of guessing."
---

# Working principles

One ticket, one worktree, one branch. Commit small and leave nothing unstaged. Prove before you claim. Ask instead of guessing.

## 1. Know where you are

In a playbook step, your prompt starts "You are one step of a Maestro playbook". The engine has claimed the ticket and made the worktree, and it makes every ticket write. Do only the step. The rules in your launch flags beat this file. Skip phases 2 and 4.

In your own session, the user directs the work: start at phase 2. To have the playbook do the whole job, run `/maestro-work <id>` instead.

## 2. Start (in your own session)

1. **Take one ticket.** Use the one the user named, else the first of `maestro-ticket list --status todo --unclaimed --json`. Work with no ticket leaves no record, so in your own session run `/maestro-create-ticket` first.
2. **Claim it** so no bot takes it: `maestro-ticket claim <id> --holder <you>/<session> --keepalive $PPID --json`. Keep `claim.token`; every later write on the ticket needs `--holder` and `--token`. Exit 4 means someone else holds it: leave it.
3. **Check the keepalive.** It renews the lease while `$PPID` lives. `ps -o comm= -p $PPID` must name your harness. If it names a short-lived wrapper, release the claim and stop: the lease would lapse while you work.
4. **Make the worktree.** `git worktree add .maestro/worktrees/<id> -b maestro/<id> <main branch>`, with the branch from `## Main branch` in `.maestro/project/project-setup.md`. Never work in the main checkout or another ticket's worktree.
5. **Read before you change.** Read the ticket and its acceptance criteria. Find the closest code that already does the same kind of thing, and match it.

## 3. While you work

- **Commit often.** One reason per commit; the message says what changed and why. Before each pause, `git status --porcelain` prints nothing. Save half-done work as a `wip:` commit, never a stash: every worktree shares one stash.
- **Stage by path.** `git add <paths>`, never `git add -A` unread. Never commit generated files, secrets or another session's files.
- **Stay in scope.** Touch only what the ticket needs. Note a problem outside it as a possible ticket and leave the code alone.
- **Show evidence.** A check you didn't run hasn't passed. Quote the command and its exit code. "Should work" is not a status.
- **Never discard work.** No `git reset --hard`, `git clean`, `--force` or `branch -D`. Never push, merge or deploy yourself: `maestro-play` opens the PR and review merges it.
- **Guard secrets.** Name a secret only by its environment variable. If one shows in output, say "secret exposed in session output" without the value.
- **Stuck twice the same way? Stop.** Write what happened, each try with its failing output line, two or three options, and the one question that unblocks you. In a step, reply blocked with that question.
- **Obey the kill switch.** If `$(git rev-parse --git-common-dir)/maestro/STOP` exists, stop now.

## 4. Finish (in your own session)

1. Run each command under `## Checks` in `.maestro/project/project-setup.md`. Quote each result.
2. Prove each acceptance criterion with the maestro-prove-complete skill.
3. Leave the worktree clean, with everything committed on `maestro/<id>`.
4. Post a handoff: `maestro-ticket comment <id> --holder <holder> --token <token> --body 'Handoff: <sha, checks, evidence paths, risks>'`. Then move the ticket on, or `maestro-ticket release` it.
5. After the merge, `git worktree remove <path>`, only if it is clean.

Reply: in a step, the step's own reply. In your own session: the ticket id and status, the branch and sha, each check with its exit code, the evidence paths, and open risks.
