# Test a PR

Verify the requested outcome on the merge candidate. A pass merges the PR. A failed requirement sends it back to the engineer.

## Phase A: Resolve

1. From the repo, run [review-prep.sh](../../maestro-code-review/scripts/review-prep.sh) with bash as `../maestro-code-review/scripts/review-prep.sh --qa <PR or branch>`, with the script path relative to the QA skill folder. For a ticket id, pass `maestro/<ticket-id>`. Use the ticket the ask names or the one on the PR's branch. Never use a fork's branch name to find a ticket.

   Prep names the run and prints its keys. It merges main at `G` into PR head `H` in detached worktree `R`. Read `C/files.txt` and `C/prep.txt`. Keep evidence in `E`. Follow **maestro-working-principles** for shell calls.

   On `stop=fork`, `stop=merged` or a base mismatch, prep returns PR keys without a worktree. If merged, read the merge commit from the PR. If prep finds no PR, write nothing and reply `test: refused` with `Next: planner: <id>: no PR`. Report any other prep failure and stop.

   On any stop, clean up as Phase F step 5 says and remove `E` if empty. Post each note below to the PR and ticket, using **maestro-working-principles** for quoted text. Skip a note already posted. A `Kickback:` moves the ticket to `in_progress`, and an `Escalation:` moves it to `blocked`. Release the claim on the way out.
2. Refuse without writing if the ticket is outside `in_review` or already claimed. Name the holder if claimed. Otherwise claim it. Refuse if the claim fails or the ticket moved first. If you lose the claim, reclaim it before writing. Stop if you can't. With no ticket, use `pr-<n>` as the id and the PR body as the spec. Invent no criteria.

   If the PR merged and its remote branch is gone or still at `H`, skip the test. On a ticket, post `Escalation: QA at <H>: PR <url> merged as <merge commit>` with a last line `Return-role: <the ticket's role>`. The planner closes it. With no ticket, report the merge in your reply.
3. Skip the test if the PR closed unmerged, merged with its branch past `H`, targets a base other than main, or comes from a fork. On a ticket, post `Kickback: QA fail at <H> (PR <url>): not tested: <why>` and send it back. With no ticket, say why in your reply.

   On a ticket, require `review_go=H` from prep, or, on a ticket the planner marked for direct QA, a `Handoff:` at `H` and `tier2_paths=0`. For a reviewed PR, read the reviewer's `For QA:` leads. For a direct handoff, confirm eligibility before using even an earlier QA pass. If neither matches, or the diff needs review, post `Rereview: QA at <H>: <reason>`, release the claim and return it to review.

   **Direct eligibility.** Read the full change, its callers, and the paths the agent notes at `G` call sensitive. The effect must be contained, observable, and easy to undo for affected users. Any tier-2 change under [the review rules](../../maestro-code-review/SKILL.md), lasting side effect, uncertain reach, or impact beyond the planner's low-risk scope needs review. Never infer eligibility from diff size. A passed eligibility check selects focused QA.
4. Use an earlier QA pass only if `qa_last=pass`, `qa_last_at=H` and `qa_last_main` names a sha. Focused QA also requires `qa_last_main=G`. Go to Phase F unless the ask or a later `Retest: planner at <H>` calls for a new test. If a `Retest:` or `Answer:` casts doubt on the last verdict, check its concern first and report the result.

   A latest `Answer:` to `Escalation: QA at <H>` binds later steps. Mark each check it waives `waived: <answer comment id>`. If what it says is now there is still missing, escalate once more and say so. Escalate a settled issue at `H` only if an `outside step:` is undone again.
5. Test in `R`. Treat `merged=conflict` as a fail with `not tested: conflicts with <main> at <G>`.
6. Read the agent notes on remote main or, if it has none, from the main checkout. Use them for the production build, startup, checks and critical flows. If you can't tell how to run the app, escalate as Phase D step 8 says.

   Check the outside steps this test needs as **maestro-working-principles** says. If one is missing, skip the work that needs it and escalate as Phase D step 8 says. Name the step and ask "take it, then send QA again?".

## Phase B: Plan by risk

1. Pick the tier from the changed files in `C/files.txt`, with the table in Phase C of the **maestro-code-review** skill ([SKILL.md](../../maestro-code-review/SKILL.md)). When unsure, go up.
2. **Focused QA.** For a direct handoff, plan only to prove each criterion on the candidate through the changed path and run main’s project checks. For docs, inspect the changed artifact directly. For a screen, inspect the result and try its changed interaction at the affected sizes. For a CLI or API, invoke the changed command or endpoint. Record that proof. Skip unrelated journeys, exploratory passes and extra tier angles. If a new risk appears, return the ticket to review as in Phase A.
3. For reviewed PRs, before you run anything, write `plan.md` in the evidence folder: each criterion with its proof, the critical flows (from the agent notes, else the app's main journeys), and the tier's angles. Each tier adds to the ones above it. Plan no check of a case the ticket's body lists under `Not handled:`, unless it would fail the test as Phase E step 1 counts. Prove each criterion in the running app, not by rerunning the commands review ran: what QA adds is what reading the code and one-off runs can't show.
4. **Reviewed retests.** On `mode=retest`, your latest verdict is a `Kickback: QA fail at <K>` that tested, `K` is in `H`'s history, and the author's change since, `C/delta.patch`, is small. Plan only: each item that failed, was `not checked` or was `waived` there (a waiver binds at one sha); each criterion, flow and angle whose code, or anything it calls, the author's change or main's merge since `K` touches; and the checks, the production build's start and one pass through each critical flow, since main moved. Every other item that passed or was carried at `K` is `carried: <K>, <comment id>`. Plan in full instead on `mode=full`, if the delta raises the tier, or if the delta or main since that verdict's `G` changes how the app builds, starts, migrates or is configured, or its dependencies: `delta_classes` or `main_since_classes` names `deps`, `schema` or `ci`, or is `unknown`.

| Tier | Angles |
|---|---|
| 0 | Only docs or inert config: each criterion and the checks, with nothing built or started, bar what Phase C step 5 asks. Else: the production build starts; each criterion on the inputs it names; one pass through each critical flow |
| 1 | Each criterion with one empty and one wrong input; every changed screen as [browser.md](browser.md) says; one feature next to the change |
| 2 | Only the angles the diff reaches; name each one you skip and why. **Upgrade** and **rollback**, when it changes stored data's shape: data made on the main branch still works at `H`, and the main branch still runs on data `H` wrote. **Second user**, when it touches auth, permissions or records owned by a user: they can't see or change the first's data. **Dependencies**, when it adds or changes a call to one: slow, then down. **Time**, when it touches a worker, queue, cache or other state that outlives a request: a restart, then 10 minutes of steady use with errors and memory watched. **Together**, when it writes a record two users can reach: two users or tabs on it at once |

## Phase C: Set up what ships

1. For docs or inert config, go straight to the changed artifact and the checks unless step 5 applies. For a running product, build and start it the way production does: production mode, real config shape, migrations run, real services in place of mocks where the project can run them. Note each way your setup still differs from production.
2. Keep state private: a new data dir or database, a free port, a browser profile of your own, and test accounts you create. Use a shared test account read-only. Never touch a shared or production service.
3. At tier 2, start the main branch first on that state, make the data the change touches, stop it, then start `H` on the same data.
4. If it won't build or start, that is a fail: keep the command and the error line. One that died of a full disk is no verdict: stop as the `Stop on refused` rule of **maestro-working-principles** says, after Phase E step 2, and reply `test: blocked: <id | PR url> at <H>` with `infra block: disk full` under `Answer:`.
5. If the diff changes README or agent note setup or startup steps, clone `H` merged with `G` into a new temp folder outside the repo.
   Use fresh environments. Follow the setup and startup steps word for word, in order.
   If a step fails or works only with something the instructions leave out, fail the test.

## Phase D: Test

1. Prove each criterion on the running app with the proof table in Phase B of **maestro-prove-complete**. For a screen, use the private browser in [browser.md](browser.md). On a focused run, check the interaction and sizes from the focused plan. On a reviewed PR, use the full browser procedure.
2. For reviewed PRs, run each angle in the plan. Then explore once: one pass through the change as a new user and one as a careless one, and at tier 2 one as a hostile one. Stop there: the plan sets the test's size, and a longer hunt always finds something.
3. When something looks wrong, run the same steps on the main branch. Wrong there too means it was already there: a new ticket, unless `H` makes it worse.
4. Rerun each failure that Phase E step 1 counts from a fresh start, 3 times if it comes and goes, and give the rate. A failure that comes and goes is still a failure.
5. Run each of main's checks (Phase A step 6) in the worktree, hooks included. Run checks needing live state fresh. A red one fails the PR, unless it is red the same way on `G` alone, in a second detached worktree, or died of a full disk, as Phase C step 4 says.
6. For each check, keep the command, exit code and a short excerpt, and save a script or step list that reruns it in the evidence folder.
7. A criterion or angle you couldn't check is `not checked` with the reason, never a pass. One no one can check as written is `not checkable`, never a pass: escalate it as step 8 says, asking "rewrite <criterion> as <your rewrite>, or waive it at <H>?". Only the planner rewrites a criterion.
8. If a tool or account the plan needs is missing, test the rest and escalate. Post `Escalation: QA at <H>`, what's missing, what you tested, the one question "add <what's missing>, or waive <these checks> at <H>?", and a last line `Return-role: <the ticket's role>`. An escalation ends the job with no verdict: do Phase E step 2 and Phase F step 5, then reply `test: blocked: <id | PR url> at <H>`, with what's missing under `Answer:`.

## Phase E: Give the verdict

1. For focused QA, fail a broken requested outcome, a required check that fails only on the candidate, or a regression that prevents use of the changed path. Escalate a missing prerequisite as in Phase D. Record small defects that leave the requested behavior working under `New tickets:` and pass. If the defect shows wider or lasting harm, return to review.

   For reviewed PRs, fail a criterion that fails on an input it names or is `not checked` or `not checkable`. Fail regressions, lost or wrong data, data shown to the wrong user, security holes untrusted input can reach, and secrets in what ships or what it prints. Fail crashes or hangs on a criterion or critical flow's path and input, broken upgrades or rollbacks, setup failures under Phase C step 5, and red checks under Phase D step 5.

   Pass otherwise. If unsure whether a finding counts as a fail, fail at tier 2 or if it may involve a security hole or lost or leaked data. A `waived` or `carried` check cannot fail. List other findings under `New tickets:`, split by `;`, worst first, with where to see each one. Mark those that need prompt action `urgent`. The planner files that line and your reply's `Open:` line on the area's `Follow-ups:` ticket.
2. Stop every process you started, and close your browser session as [browser.md](browser.md) says.
3. If the PR's head has moved past `H`, post at `H` anyway and merge nothing: the new head needs review first, so on a pass post the `Rereview:` of Phase A step 3 after your verdict, naming the new head.
4. Post this note.

   ```text
   QA: pass at <H> (PR <url>), tier <n>, on <main> at <G>
   Setup: <build, data and services used; each way it differs from production>
   - 1 pass: <proof>: <excerpt or screenshot path>
   - 2 fail: steps <exact, from a fresh start>; expected <...>; actual <quoted>; on main: <same | works>; rate <n/3>; evidence <path>
   - 3 not checked: <why> | not checkable: <question> | waived: <answer comment id> | carried: <K>, <comment id>
   Checks: <each command: exit code>
   Flows: <each critical flow: pass | fail>
   Angles: <each one tried: what you did, what happened>
   Rerun: <evidence folder>
   New tickets: <problems outside the verdict, worst first | none>
   ```

   A fail opens `Kickback: QA fail at <H> (PR <url>), tier <n>, on <main> at <G>` instead: every send-back, review's or yours, is a `Kickback:`.

5. A fail's note sent the ticket back: do Phase F step 5. On a pass, go on to Phase F.

## Phase F: Ship a pass

Only for a ticket with a valid basis at `H` from Phase A and your `QA: pass at <H>` on the tested criteria. With no ticket, merge nothing and skip to step 5: a person merges.

1. Read the PR's head and the ticket again. If the head isn't `H`, merge nothing and post the `Rereview:` as Phase E step 3 says. If the ticket isn't `in_review`, or you can't claim it again after losing the claim, merge nothing and say so in the reply.
2. Fetch. If the main branch isn't at the `G` your pass names, set `G` to its new sha, merge it into the worktree if it isn't in yet, and rerun Phase D step 5, twice at most. For a direct handoff, recheck eligibility and the changed path on the new candidate before merging. If it now needs review, return it as in Phase A. If an outcome fails, post a QA fail. After a successful retest on a ticket marked for direct QA, post a new Phase E pass naming the tested `G`. A conflict or a red check is now a fail: post it as Phase E step 4 says, then do step 5 below and stop. On a third move, merge nothing and say so. Clean up as in step 5.
3. Set `SKILL_DIR` to this QA skill’s absolute path. Pass the PR, `H`, the main branch from QA prep and the `G` your pass names. The script merges only while the PR is open, from this repo, on the main branch and at `H`, and the remote main is still at `G`, and prints the merge commit only once the PR shows merged at that base and head. Then move the ticket to `done` with the note `Merged: <M> (QA pass at <H>)`. Never squash, rebase, force-push or `--no-verify`.
   ```bash
   M=$("$SKILL_DIR/scripts/merge-reviewed.sh" <n> "$H" "$main" "$G")
   ```
4. On `base changed since QA pass`, return to step 2 within its retry limit. If the merge otherwise fails, or `M` is empty because gh only queued the PR or turned on auto-merge (on a branch with a merge queue it exits 0 before it merges), skip the move to `done`; turning auto-merge off keeps GitHub from merging a later push no one reviewed or tested. If the PR now shows merged, escalate as Phase A step 2 says for a merged PR; if its head is no longer `H`, do as step 1 says. Else post `Escalation: QA at <H>: merge: <gh's output>`, such as merge commits turned off or a missing approval, saying first if it printed `auto-merge off: false`, with a last line `Return-role: <the ticket's role>`; the reply opens `test: blocked`. Never try another merge method.
5. Release your claim and remove each worktree and temp folder you made, including `C` and any fresh clone. After a merge, also remove the engineer's worktree for the branch if it is on this box, keeping and naming it in your reply if the **maestro-working-principles** check finds ignored files outside the rebuildable folders. Remove each without force; one git refuses for changes stays, emptied of its rebuildable folders as **maestro-working-principles** says, and you name it in the reply.

Under `Answer:`, give the tier, one line per criterion, steps for each failure, the comment id, the merge commit or why there is none, and any worktree left behind. Use `Next:` to name who acts and what they do. Use `none` after a merge.
