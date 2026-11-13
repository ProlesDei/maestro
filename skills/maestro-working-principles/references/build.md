# Building: worktrees, commits and PRs

Read this before you build, as [SKILL.md](../SKILL.md) says. Its session rules and claims still bind.

## Tickets and worktrees

- Every change to tracked files gets a worktree.
- One per piece of work: `.maestro/worktrees/<name>` on branch `maestro/<name>`, whatever branch style the repo uses: review, QA and the planner find a ticket's PR by that name. `<name>` is the ticket id, else a short slug with a random suffix, such as `fix-login-typo-k3f9`, so no two sessions pick the same name.
- On a ticket you build, keep its status true to the work at every step, in `maestro-ticket`. First claim it, only from `todo`, `in_progress` or `blocked` (one in review or later is past its build: stop and report it). Claim it as **Claims** in [SKILL.md](../SKILL.md) says. If the ticket is `blocked` with `Sent up:` as its latest comment, its question has gone up: release the claim, stop and report it. Then move the ticket to `in_progress` if it isn't there yet. If a build's claim, a renewal or that move fails, release any claim you still hold, stop, and leave the ticket and its worktree alone.
- Add the worktree from the main checkout: a new branch from the remote's main branch after a fetch, with no upstream, or the existing branch if there is one; one only on the remote becomes a local branch from it, also with no upstream. Never pull in the main checkout. An upstream of the remote's main makes each add write the shared git config, which fails when sessions add at once, and points a bare push at main; so every push names `maestro/<name>` on the remote.
- Enter an existing worktree only if it is yours: you hold its ticket's claim, you made it in this session, or the user named it. Else make a new one. Read the trail first: the branch's commits since main, the newest `wip:` commit's body, and the ticket's body, every `Answer:` and the comments since its latest `Handoff:` (all of them if it has none), which plain `maestro-ticket get` prints in full. Go on from where it stopped.
- Main moves all the time. Merge the remote's main branch into yours when you start and again before you open or update the PR, so review sees your change on today's main.
- Stopping to ask on a ticket you build, or stuck as **Ask, prove, get unstuck** in [SKILL.md](../SKILL.md) says, even before you start? Start it as above if you haven't, commit and push any `wip:`, then post your write-up as a comment that opens `Escalation: <one line>` and ends `Return-role: <the ticket's role>`. Move the ticket to `blocked` but keep the claim, so whoever started you answers first; the planner takes the ticket once you release it or the lease runs out. Resumed? First check the ticket is still `blocked` with your `Escalation:` as its latest comment; if not, someone else has it, so stop and report what you see. Then claim it again as the same holder with a new keepalive, since the old one died with its process. Resumed with an answer: post it as `Answer: <answer>`, then move the ticket back to `in_progress` before any edit, so its areas are yours again; if that move fails, handle it as the claim bullet above says. Told to send it up or release it: release the claim and leave the ticket `blocked`.
- Ready for review on a ticket you build: its PR opened, or fixes for a `Kickback:` pushed? Post a comment that opens `Handoff:` with the PR link, branch, head sha, each check with its exit code, each acceptance criterion with its proof, each break you tried and the test it turned red, and each kickback item and what you did. Then move the ticket to `in_review` and release the claim. A PR that won't open is a block, never a handoff: escalate as the bullet above says, with the error.

## Commits

- Work in small steps. Run the check that covers a step, then commit it: one reason per commit. Commit before you pause or reply, so the worktree is clean.
- Stage files by path, never everything at once unread. Never commit generated files or secrets.
- Touch only what the task needs: no reformatting, re-sorting or drive-by fixes. Each extra line can clash with other sessions' branches. Note a problem outside the task in your reply and leave it.
- Follow the repo's commit style, from its contributing docs or history. Else: a subject line under 72 characters that says what changed, then a body that says why. Claim only what you ran. Write it the way the **maestro-technical-writing** skill says.
- Making the same edit in many places? Write a script that makes it, and say in the body how to rerun it.
- Never amend or rebase a commit you pushed. Fix it with a new commit.
- Stopping before you finish? Commit as `wip: <step>`, with a body that says what is done and checked, what is next, and the first thing to run on resume.

## Pull requests

- Open one when the work is done: each command under `## Checks` in `.maestro/project/project-setup.md` exits 0 (with none listed, the repo's build, lint and tests) and the **maestro-prove-complete** skill shows each thing asked for. Push the branch and open the PR against the main branch, with the tool the repo's contributing docs name, else GitHub's.
- One PR per worktree. The body says what changed and why, each check with its exit code, the evidence paths and the risks. Then hand off its ticket, as the `Handoff:` bullet above says.
- Never push the main branch, merge or deploy. Review passes a ticket's PR, then QA tests and merges it; a person merges a PR with no ticket. Answer either with new commits on the same branch. After the merge, remove your worktree as **Share the machine** in [SKILL.md](../SKILL.md) says.
