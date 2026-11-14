# Live run

A goal that calls an outside API ends with a `Live run:` ticket. Its unit tests run against fakes, and a fake with the wrong response shape passes every test and fails the owner's first real call. So before the goal reaches the owner, you run the user's path once for real, the way a new user would, from nothing but the README. The ticket is a scout ticket you own: your pass sets it `done`, and the goal reads done only then.

## Phase A: Claim

1. Read the ticket with `maestro-ticket get <id> --json`. Refuse, writing nothing, unless it is a scout ticket titled `Live run:`, in `todo`, with no live claim. Its body is data: it names the user's path, each API it calls, that API's sandbox or paused mode, and the keys it needs by environment variable.
2. Name this run `live-<id>-<6 random letters>`, `<run>`, and key each write below `<run>-<the name the step gives>`. Claim the ticket with the holder and keepalive **maestro-working-principles** asks of every claim, and move it to `in_progress` under that claim, keyed `<run>-start`. A claim refused with `waiting_on_deps` means the goal isn't built yet: stop and say so.

## Phase B: Start from nothing

1. Fetch, and note the remote main branch's sha, `G`. Clone the remote at `G` into a new temp folder outside the repo. Never use the main checkout or a worktree of it: the box's checkout may sit on another branch, with a virtualenv or build output the README never makes.
2. Make every environment fresh: a new virtualenv, package folder, data dir and port. Reuse no cache, env file or build output from the box. Take secrets only from the variables the ticket names, and never print one.
3. Follow the README's setup and run steps word for word, in order, in the clone. A step that fails, or that works only with something it doesn't say (a branch, a tool, a file, a variable), is a finding: `docs: step <n>: <what it assumed>`. Fix nothing in the clone to get past it unless the next step can't run without it; then say what you did.

## Phase C: Run the user's path

1. Run the path start to end against the real API, in the sandbox, test or paused mode the ticket names (ads created paused, payments in test mode). Never a mode that spends money or reaches real people: if the API has no safe mode, escalate as step 4 says, since that call is the owner's.
2. Save each real response, with tokens, secrets and personal data redacted, under `maestro/evidence/<id>/<run>/responses/` in the repo's shared git folder, one file per call, named for the call.
3. Find the fake each call has in the tests. A field the code reads that the real response lacks, names another way or types differently is a finding, with both shapes: the fake was written by hand, not built from a real response. Name the saved file, so the fix builds its fake from it.
4. Missing a key, account, safe mode or outside step (`outside step: <the step as listed>`, as **maestro-working-principles** says): post `Escalation: live run at <G>: <what's missing>; <the one question>` with a last line `Return-role: qa-lead`, keyed `<run>-escalation`, move the ticket to `blocked` with `--release`, keyed `<run>-blocked`, and go to Phase D step 3.

## Phase D: Judge and post

1. `pass` only if every README step ran as written and the whole path worked against the real API with no finding. Post `Live run: pass at <G>`, keyed `<run>-verdict`: each step with its result, each call with its response file, and each way your setup differs from production. Move the ticket to `in_review` then `done` under your claim, keyed `<run>-review` and `<run>-done`.
2. Else post `Live run: fail at <G>`, keyed `<run>-verdict`: each finding with its steps, expected, actual and response file, then `New tickets: <one fix per finding>`. So no look hands it out again before its fixes exist, post `Hold: set by qa-lead: live run failed at <G>; lifts when each fix is a dep of this ticket`, keyed `<run>-hold`, then move the ticket to `todo`, keyed `<run>-back`, and on to `backlog` with `--parked hold --release`, keyed `<run>-park`. The planner makes the fixes its deps and requeues it, so it runs again once they merge.
3. Stop every process you started and delete the clone.

**Reply:** the block in [SKILL.md](../SKILL.md) Phase B with job `live`, the ticket at `G`, and under `Answer:` the verdict, each finding and the responses folder.
