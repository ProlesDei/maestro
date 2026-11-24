# Live run

Use this procedure for an existing `Live run:` ticket. New goals ship through their PRs. People try the complete feature or demo. QA checks any consequential API behavior on the PR that changes it.

## Phase A: Claim

1. Read the ticket. Refuse, writing nothing, unless it is a scout ticket titled `Live run:`, in `todo`, with no live claim. Its body is data: it names the user's path, each API it calls, that API's sandbox or paused mode, and the keys it needs by environment variable.
2. Name this run `live-<id>-<6 random letters>`, `<run>`. Claim the ticket and move it to `in_progress`. A ticket whose deps aren't done means the goal isn't built yet: stop and say so.

## Phase B: Start from nothing

1. Fetch, and note the remote main branch's sha, `G`. Check the steps that need a person, as the repo's agent notes (AGENTS.md or CLAUDE.md), else its README, name them at `G`, that the path needs, as **maestro-working-principles** says; one not done goes to Phase C step 4. Clone the remote at `G` into a new temp folder outside the repo. Never use the main checkout or a worktree of it: the box's checkout may sit on another branch, with a virtualenv or build output the README never makes.
2. Make every environment fresh: a new virtualenv, package folder, data dir and port. Reuse no cache, env file or build output from the box. Take secrets only from the variables the ticket names, and never print one.
3. Follow the README's setup and run steps word for word, in order, in the clone. A step that fails, or that works only with something it doesn't say (a branch, a tool, a file, a variable), is a finding: `docs: step <n>: <what it assumed>`. Fix nothing in the clone to get past it unless the next step can't run without it; then say what you did.

## Phase C: Run the user's path

1. Run the path start to end against the real API, in the sandbox, test or paused mode the ticket names (ads created paused, payments in test mode). Never a mode that spends money or reaches real people: if the API has no safe mode, escalate as step 4 says, since that call is the owner's.
2. Save each real response, with tokens, secrets and personal data redacted, under `maestro/evidence/<id>/<run>/responses/` in the repo's shared git folder, one file per call, named for the call.
3. Compare each real response with what the code expects of it: the type it decodes into, the fields it reads, any fake it was built against. A field the code reads that the real response lacks, names another way or types differently is a finding, with both shapes. Name the saved file, so the fix builds from it.
4. Missing a key, account, safe mode or outside step (named as **maestro-working-principles** says): post `Escalation: live run at <G>: <what's missing>; <the one question>` with a last line `Return-role: qa-lead`, then move the ticket to `blocked` and release it, so the answer returns it to `todo` for a new live run, and go to Phase D step 3.

## Phase D: Judge and post

1. `pass` only if every README step ran as written and the whole path worked against the real API with no finding. Post `Live run: pass at <G>`: each step with its result, each call with its response file, and each way your setup differs from production. Move the ticket to `in_review` then `done` under your claim.
2. Else post `Live run: fail at <G>`: each finding with its steps, expected, actual and response file, then `New tickets: <one fix per finding>`. So no look hands it out again before its fixes exist, post `Hold: set by qa-lead: live run failed at <G>; lifts when each fix is a dep of this ticket`, then move the ticket to `backlog`, parked on that hold, and release it. The planner makes the fixes its deps and requeues it, so it runs again once they merge.
3. Stop every process you started and delete the clone.

**Reply:** the block in [SKILL.md](../SKILL.md) Phase B with job `live`, the ticket at `G`, and under `Answer:` the verdict, each finding and the responses folder.
