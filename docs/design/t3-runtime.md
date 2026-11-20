# T3 runtime: a spec for Maestro without Grok Bot

**Status:** draft v4, 2026-11-20. v1 went through two fresh-context audits on 2026-11-18; v2 fixed what they found (section 13). v3 takes the owner's answers (section 1). v4 adds subscription routing and a Codex chief of staff. Nothing here is built. The team works on this spec from here.
**Labels:** **[code]** means the claim was checked in the source at the `file:line` given. **[docs]** means a vendor doc says it; no run here has shown it. **(guess)** means neither has. Section 11 lists the probes that settle each **[docs]** and **(guess)** claim the build depends on.

## 0. The point

1. Grok Bot goes as the runtime. It bills each model call times its whole chat, about 100-200K cached tokens a call ([decisions](../decisions.md), 19). Its seven profiles stay in `bot-profiles/`. Setting a bot's webhook again brings it back, so a team can still run Maestro on Grok.
2. Each role gets a **server**: a webhook (a Grok bot, as today), a command (the chief of staff's wake), or code (the planner's routing and the five worker roles). Every place that now asks "does this bot have a webhook?" asks "is this role served?" instead.
3. Agent sessions plan, judge and rule. Section 8 maps each role to a harness, model and account. Code takes only the relaying that Grok bots did: start the next step, refill a slot, take a look, pass on what needs a person (section 3).
4. One Codex thread in T3 Code plays the chief of staff. The owner talks to it from the T3 phone app. It runs behind a wall: no worker session can type into it, and it reaches the board through one command, `maestro-ticket cos <verb>`.
5. One machine. The board lives on MongoDB Atlas. Two Claude Max 20x accounts and one ChatGPT Pro account serve the roles in section 8.

```mermaid
flowchart LR
  OWNER["Owner's phone<br/>T3 app"]
  subgraph COSSIDE["Chief-of-staff side"]
    T3["T3 Code"]
    COS["Chief-of-staff thread<br/>Codex Astra"]
  end
  subgraph WORKSIDE["Worker side: user maestro"]
    SUP["Supervisors<br/>code roles, 0 tokens"]
    RUNS["Claude Code / Codex runs<br/>plan, judge, build,<br/>review, QA, design"]
  end
  DB[("MongoDB Atlas<br/>the board + inbox")]
  GH["GitHub"]
  OWNER <--> T3 --- COS
  SUP -->|"wake: no text"| COS
  COS -->|"cos verb"| SUP
  SUP -->|"starts"| RUNS
  RUNS -->|"run ends"| SUP
  SUP <--> DB
  RUNS <--> DB
  RUNS <--> GH
```

## 1. The owner's answers

| Call | Current answer | What it changes here |
|---|---|---|
| Accounts | Two Claude Max 20x plans and ChatGPT Pro; prefer Codex Astra at high or medium effort for chief of staff | Section 8 sets the initial assignments and capacity rules. |
| Second user for the chief of staff | Yes. Today T3 runs in WSL on a Windows desktop; that may move to the native Windows app, or a container | Section 6 gives the wall three ways to build it. |
| Planner as code | Asked what it means: isn't the planner the product manager? | Section 3 answers it. |
| Several boxes | One machine | Phase 2 leaves the build. Section 12 keeps what the audits found, for later. |
| Where the board lives | MongoDB Atlas | Section 7. |
| Rewrite the rules | Yes: a full rewrite of how Maestro runs | Section 9 lists every file. |

## 2. Who serves each role

| Role | Today | T3 runtime |
|---|---|---|
| Junior, senior engineer | Grok webhook | code |
| Reviewer, QA lead, designer | Grok webhook | code |
| Planner | Grok webhook | code for the routing; agent sessions for decisions (section 3) |
| Chief of staff | Grok webhook | command: wake its T3 thread (section 5) |

`maestro-ticket notify set <bot>` takes one of `--url-file` (today), `--exec-file` or `--code`. The chain, `look` and the supervisor read that server through one `served(bot)` check. Today each reads `hasHook`:
- `handOn` **[code]** `tools/maestro-ticket/cli/chain.go:247`;
- `startSends` **[code]** `tools/maestro-ticket/cli/look.go:282`;
- the refill and ack gates **[code]** `tools/maestro-ticket/cli/chain.go:110-120`;
- the ring itself **[code]** `tools/maestro-ticket/runs/supervise.go:473`.

The servers mix, so the move off Grok goes one role at a time (section 10).

## 3. The planner: Claude thinks, code relays

The planner is still the product manager. What changes is who carries its messages. Decision 19 already bars the Grok planner from reading a ticket, a PR or a reply to judge it, so today every planning and judging call already runs in a Claude session. The Grok bot only wakes, runs `look`, passes lines on and acks. That relay is the part code takes.

| Planner's work | Who does it today | T3 runtime |
|---|---|---|
| Turn a goal into ordered, tiered tickets with criteria | Claude: `job plan`, the **maestro-planner** skill | same |
| Split or rewrite a ticket, carry out a ruling, finish a split | Claude: `job plan`, `job rule` | same |
| Judge blocks, disputed findings and parked tickets | Claude: the judge session `look --judge` starts | same |
| Ask the chief of staff before changing scope | Claude: the plan session leaves a card | same; the card goes to the inbox |
| Pick what starts next, fill slots, route by tier | code: `look`, refill, the chain | same |
| Wake on a message, run `look`, pass lines on, ack | Grok bot | **code** (3.1) |
| Report progress, ETAs | Grok bot, from `look --report` | the chief of staff runs `cos look`; the plan session records each ETA it gives |

A standing planner thread in T3 could come later, if the owner wants a product manager that keeps its own memory between goals. It would wake on every planner line, so it costs more than code. That is a later call, not part of this build.

### 3.1 What the code relay does

A run's supervisor routes the run once it ends **[code]** `tools/maestro-ticket/cli/chain.go:51`. When the role the result is for is served by code, the supervisor acts on each line in its own process:

```mermaid
flowchart TD
  END["A run ends: its supervisor routes it<br/>matches line shapes, reads no ticket or reply"]
  END --> A["Start the next step<br/>build, review, qa, live, design T-n<br/>review T-n --approved sha<br/>do: resume --died, --go-on, senior escalation,<br/>--escalate, --decide; job again"]
  END --> B["Refill<br/>slot free T-n, merged T-n"]
  END --> C["Look<br/>kickback, block, timed_out,<br/>failed, mismatch, live fail, a stop"]
  END --> D["Needs a person<br/>usage_limit, disk_full, died twice,<br/>goal done, cause, a fault line,<br/>a card, a design card, a QA question"]
  A --> RUN["Next eligible run, as that role"]
  B --> FILL["Builds for the next tickets"]
  C --> LOCK{"Acquire lock or mark dirty atomically"}
  LOCK -- yes --> JUDGE["look --judge<br/>a judge session"]
  LOCK -- no --> DIRTY["Dirty recorded; return"]
  D --> INBOX[("Inbox in the board")]
  INBOX --> WAKE["wake the chief of staff"]
```

| Line | Code does |
|---|---|
| `build`, `review`, `qa`, `live`, `design <id>` | Starts it as that role. Today `chainLine` takes only the first three **[code]** `tools/maestro-ticket/cli/chain.go:29`. |
| `review <id> --approved <sha> --drop …` from a judge | Starts that review |
| `do: resume <run> --died`, `--go-on`, `job again <run>` | Runs it, as now **[code]** `tools/maestro-ticket/cli/chain.go:169` |
| `do: resume <run> --opus`, `--escalate`, `--decide`, `job again <run> --full` | Map the existing `--opus` line to a senior escalation under section 8. Keep the other fixed shapes; the grammar still allows no rules write. |
| `slot free`, `merged` | Refill **[code]** `tools/maestro-ticket/cli/look.go:304` |
| `kickback`, `block`, `timed_out`, `failed`, `mismatch`, `live fail`, a stop | One `look --judge`, under the look lock (3.2) |
| `usage_limit`, `disk_full`, `died twice`, `goal <slug> done`, `cause …`, a fault line, a card, a design card, a QA question | Into the chief of staff's inbox (5.2) |

Persist one handoff ID per action. Replay returns the recorded run or inbox item, including during a switch back to a webhook. Ack after all actions have durable receipts. A run a usage limit stopped stays unacked, as today, so its ring at the reset fires **[code]** `tools/maestro-ticket/cli/launch.go:409`. With code as the server, that ring runs the run's `do: resume --go-on` line. The same holds for a handoff a pause held, and for the STOP lift.

It never forwards a full `--report`. The planner's persona sends a report only when a goal finishes, the line stops, a block needs it or the chief of staff asks, and the relay keeps to that.

### 3.2 The look lock

300 runs can end in one minute. They must not each start a look.

```mermaid
sequenceDiagram
  participant A as Supervisor A
  participant B as Supervisor B
  participant C as Supervisor C
  participant M as Board marks
  A->>M: atomically acquire or set dirty
  M-->>A: acquired
  B->>M: atomically acquire or set dirty
  M-->>B: dirty recorded
  Note over A: look --judge
  A->>M: atomically consume dirty and retain lock, or release if clean
  M-->>A: run again
  A->>M: after second look, release if clean
```

Renew the 10-minute lease during a long look. On takeover, verify the holder token before clearing or releasing the lock.

- `look` already marks each row it hands a judge **[code]** `tools/maestro-ticket/cli/look.go:410`, so a second look never judges the same row again.

## 4. A ticket's life in the T3 runtime

```mermaid
sequenceDiagram
  actor O as Owner
  participant S as Chief of staff
  participant R as Code relay
  participant P as Plan session
  participant E as Build session
  participant V as Review session
  participant Q as QA session
  O->>S: goal: add billing, high priority
  S->>R: cos plan
  R->>P: job plan
  P-->>R: tickets written
  R->>E: refill: job build T-12
  E-->>R: PR open, in review
  R->>V: job review T-12
  V-->>R: Review GO at sha
  R->>Q: job qa T-12
  Q-->>R: merged, done
  R->>E: slot free: next ticket's build
  Note over R: no wake, no token
```

A send-back from review or QA goes straight back to the engineer. A block goes to the judge. Only what needs a person reaches the chief of staff.

## 5. The chief of staff

### 5.1 The thread

- One Codex thread in T3 Code, on Astra, in full access, so tool asks never reach the phone ([decisions](../decisions.md), 12).
- Its persona, the team rules and the `cos` verbs sit in `AGENTS.md` in the chief of staff’s private working directory, built by `scripts/bot-bundle.sh`. P0 checks that Codex follows those instructions on startup and after compaction. Keep its memory in the same private directory.
- It reads what is short itself. Long reads (a PR, many tickets) go through `cos ask`, a separate run, so its own context stays small.

### 5.2 The inbox

```mermaid
sequenceDiagram
  participant R as Code relay
  participant I as Inbox in the board
  participant S as Chief of staff
  actor O as Owner's phone
  R->>I: add: died twice T-31
  R->>S: wake, fixed text: ring, run pickup
  S->>I: cos pickup
  I-->>S: died twice T-31, with its card
  S->>O: one decision card, push
  O-->>S: answer: B
  S->>R: cos rule T-31, answer on stdin
  S->>I: cos ack
  Note over R,S: no ack yet? the gate rings again at 5, 10 and 40 min
```

- The inbox is a space in the board's coord store, `inbox/chief-of-staff`. Each item is one line of a fixed shape, with a run or ticket id.
- Code adds items. Only the chief of staff's `cos ack` removes them, so a failed or lost wake is rung again and an alert can't vanish.
- A card's text stays data, as [decisions](../decisions.md), 7 says.
- The wake goes through the existing gate: at most once each 4 minutes **[code]** `tools/maestro-ticket/runs/gate.go`, and again at 5, 10 and 40 minutes while anything waits unacked **[code]** `tools/maestro-ticket/runs/supervise.go:46`.
- If Atlas is down, code can't write the inbox. Then the wake carries one fixed word, `store-down`. The host exposes the fault through a status command readable without a model or Atlas. The chief of staff runs `cos look` once Atlas is back.

### 5.3 The `cos` verbs

| Verb | Runs, signed as |
|---|---|
| `pickup`, `ack <ids>` | the inbox, as `chief-of-staff` |
| `plan` (goal on stdin), `rule <id>`, `resume <run-id>` | `job plan`, `job rule`, `resume`, as `planner` |
| `look` | `look --judge --report`, as `planner`. It also restarts a quiet board after a reboot or an outage. |
| `rules show`, `rules set limit <n>`, `rules add`, `rules drop <n>` | as `chief-of-staff`, which `rulesWriters` already allows **[code]** `tools/maestro-ticket/cli/rules.go:48` |
| `ask` (question on stdin) | a `/maestro-chief-of-staff` run on the worker side; its answer comes back through the inbox |
| `list`, `get`, `events`, `goals`, `flow`, `eta`, `stop`, `start` | as today's commands, as `chief-of-staff` |

`cos` refuses any verb off this list.

## 6. The wall

Every worker run has full permissions and reads ticket text. If one could type into the chief of staff's thread, text planted in a ticket could steer the team, since the thread has a shell and signs as the planner. Under Grok this hole was smaller, because a bot only ever read fixed-shape lines. So the two sides run apart, with two doors:

| Door | From → to | What crosses |
|---|---|---|
| `maestro-ticket wake` | worker side → chief of staff | nothing but "wake", or the one word `store-down`. It sends the thread the fixed text `ring: run pickup`. |
| `maestro-ticket cos <verb>` | chief of staff → worker side | one verb from the list, text on stdin |

So a worker session can wake the thread, at worst too often, but can't put words in it. The thread can't run a shell on the worker side.

```mermaid
flowchart LR
  subgraph W["Worker side"]
    SUP["Supervisors + code relay"]
    RUNS["Claude Code / Codex runs<br/>full permissions"]
    STORE["repo, Atlas URI,<br/>worker profile logins"]
  end
  subgraph C["Chief-of-staff side"]
    T3["T3 Code + thread"]
    CMD["AGENTS.md: persona,<br/>rules, cos verbs"]
  end
  SUP -->|"wake: no text"| T3
  T3 -->|"cos verb, fixed list"| SUP
  RUNS -.->|"blocked: can't type into the thread"| T3
  T3 -.->|"blocked: no shell on this side"| RUNS
```

### 6.1 Three ways to build it

The worker side must be Linux. The supervisor's process handling is Unix-only, with a stub elsewhere **[code]** `tools/maestro-ticket/cli/proc_unix.go`, `tools/maestro-ticket/cli/proc_other.go`, and the skills' scripts are bash.

| | A: two users in WSL (default) | B: T3 native on Windows, workers in WSL | C: workers in a container |
|---|---|---|---|
| Worker side | user `maestro` in the WSL distro | user `maestro` in the WSL distro | a Linux container |
| Chief-of-staff side | user `maestro-cos` in the same distro, running `t3 serve`; the Windows T3 app and the phone connect to it | the Windows T3 app, as the Windows user | T3 on the host |
| `wake` door | a sudoers rule: `maestro` may run only `maestro-ticket wake` as `maestro-cos` | a T3 webhook task bound to the thread, with a fixed prompt that ignores the body (needs T3 Connect) **[docs]** | the same webhook task |
| `cos` door | a sudoers rule: `maestro-cos` may run only `maestro-ticket cos …` as `maestro` | `wsl.exe -d <distro> -u maestro maestro-ticket cos …` | `docker exec <container> maestro-ticket cos …` |
| What else must hold | worker cannot reach the host identity, private files or T3 listener | WSL interop off (`[interop] enabled=false`) and no Windows drives mounted (`[automount] enabled=false`) in the worker distro's `/etc/wsl.conf`; else a worker can run `t3.exe` or read the thread's files | the container gets no host socket or host folder beyond the repo |
| Change from today | one more Linux user | moves T3 out of WSL | adds Docker |

P0 checks that workers cannot reach the host identity, private thread files or T3 listener through WSL interop. A is the default: it changes least from today's setup. B and C wait on a reason to move, such as a speed gain from the native app. In every option, keep the repo on the Linux filesystem, not under `/mnt/c`; WSL reads Windows drives far slower (guess, P0 measures it).

A team on one Linux box with no T3 can skip the wall. Then the same commands run directly, without the guard.

### 6.2 Setup for option A

1. Make user `maestro-cos`. `scripts/install.sh` prints the two sudoers lines; the owner adds them with `visudo`.
2. As `maestro-cos`: install T3 Code, pin a stable version (`t3 update <version>`) and run `t3 up --connect` **[docs]**.
3. In T3, add a Codex provider instance with its own config dir, logged in to the owner’s ChatGPT Pro plan, in full-access mode.
4. Put the chief of staff's `AGENTS.md` bundle in the private working directory.
5. Start the thread in that directory. Keep its instructions and privileged helpers outside worker-writable paths. Save its id where `maestro-ticket wake` reads it.
6. Turn on phone pushes. Background pushes need T3 Connect **[docs]**.

## 7. The board on Atlas

`maestro-ticket` already runs on MongoDB, `mongodb+srv://` URIs included **[code]** `tools/maestro-ticket/README.md:24`.

1. Make an Atlas cluster in a region near the machine. Let only the machine's IP reach it, and give Maestro a user with rights on one database only.
2. As `maestro`, write the URI with `maestro-ticket store --uri-file -`. It lands in `store.uri`, mode 0600, and only the host ever prints.
3. Move today's board with `maestro-ticket migrate`.
4. The chief-of-staff side never holds the URI. It reaches the board through `cos` verbs.

On MongoDB, the store's folder still holds this machine's own state: `runs/`, `slots/`, `notify/` **[code]** `tools/maestro-ticket/README.md:24`. On one machine that costs nothing.

Risks:
- **Latency.** Each command, claim and heartbeat makes a round trip to Atlas, where SQLite made none. P0 times a `look` on a real board.
- **Tier limits.** Atlas's free tier caps connections and operations (guess). 300 sessions, each with a keepalive, may need a paid tier. P0 counts the connections one run holds.
- **An outage stops every run.** The `store-down` wake (5.2) tells the owner, and `cos look` restarts the board after.
- **Backups.** Atlas's backups come with paid tiers (guess). `maestro-ticket` keeps its own `backups/` folder too.

## 8. Profiles, model choice and capacity

The installation defines each profile by account alias, harness, approved models and effort levels, eligible roles, and concurrency cap. Each profile uses its owner's native login and its own config. Credentials stay out of commands, run records and logs. Use subscription billing by default. Strip inherited API credentials and reject a launch whose billing mode cannot be verified. API billing requires explicit configuration. See [Claude plan usage](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan) and [Codex authentication](https://developers.openai.com/codex/auth).

| Profile | Starting assignment | Capacity rule |
|---|---|---|
| ChatGPT Pro / Codex | Chief of staff on Astra. Start at medium effort for routine turns and use high for hard rulings. Review is its first shared worker role. | Give owner turns priority. Keep worker review disabled outside the trial until measured headroom supports it. |
| Claude Max A / Claude Code | Planning and judging. Qualify review as a second role. | Reserve capacity for decisions that unblock other tickets. |
| Claude Max B / Claude Code | Builds, design, QA and review. Start with the existing Claude model settings. | Give long builds and short reviews separate cost estimates. |

These are install defaults. An account may serve only workloads its owner authorizes under its plan. Keep both Claude profiles on their assigned workloads when one reaches a limit. For Codex reviews, sign in through a separate worker-side config. Keep the chief of staff’s config and thread private. P0 must prove both contexts work under the authorized login. Account-wide limits apply across those contexts.

**Fresh starts.** First select models and effort levels approved for the role and tier. Senior escalation requests a senior-capable model. Within that set, select an eligible profile with room after active reservations, the expected job cost and protected capacity. Use reliable remaining allowance and reset times where available. With missing or stale readings, use conservative concurrency caps and keep protected profiles off optional work. Use observed usage by role, model and effort to tune estimates. Keep exported stats anonymous.

Reserve capacity and claim the handoff together before launch. Concurrent supervisors must see the same reservation. Record the role, profile alias, harness, model, effort, provider session ID when available, worktree and handoff ID. Release the reservation at completion. After a crash, reconcile it against the recorded run before allowing another start.

**Recovery.** Resume with the original profile, session and worktree. If a qualified role changes harness, start a fresh run from a durable handoff with the ticket, commit, worktree and unfinished step. Preserve pending edits. A reset notification checks that the old run still owns the step. Pause one profile for expired login or exhausted allowance, one provider for a provider outage, and all board-dependent work during a store outage. Show active and waiting jobs in `look`, with the profile, wait reason and reset time when known. Provide a model-free owner status path for quota, login and store failures.

## 9. The rewrite

The owner asked for a full rewrite of how Maestro runs. Every file below changes in step S6.

| File | Change |
|---|---|
| `docs/decisions.md` | 1: a ring may run a command; a run's end may run the judge's look under the lock. 7: the inbox and `cos` verbs hold the ticket-text guard in code. 12: the thread runs in full access. 15: the thread is the one agent session `launch` didn't start. 19 and 21: name served roles, not Grok bots. A new decision for the wall. |
| `AGENTS.md` | "What we're building": the outer layer is the T3 thread plus code; Grok is one supported runtime |
| `README.md`, `docs/team.mermaid`, `docs/bot-anatomy.mermaid` | redraw for served roles and the wall |
| `bot-profiles/chief-of-staff/persona.md` | rewrite for the T3 thread and the `cos` verbs |
| `bot-profiles/planner/persona.md` | becomes the spec the code relay follows, and the Grok persona if a team keeps one |
| `bot-profiles/team-rules.md`, `routine.md` | Grok bots only; the thread gets its own short rules |
| `skills/maestro-chief-of-staff`, `skills/maestro-planner` | stop calling their caller "the bot" |
| `INSTALL.md`, `scripts/install.sh`, `scripts/bot-bundle.sh` | configure runtime, profiles, role models, effort and reserves; print the sudoers lines; build the thread's `AGENTS.md` |
| `templates/project-setup.md` | a `## Runtime` section |

## 10. The move off Grok, one role at a time

The servers mix, so each step moves one group of roles and can go back by setting the old webhook again.

```mermaid
flowchart LR
  M0["Now<br/>all seven on Grok"] --> M1["Step 1<br/>workers on code;<br/>Grok planner and chief of staff"]
  M1 --> M2["Step 2<br/>planner relay on code;<br/>Grok chief of staff"]
  M2 --> M3["Step 3<br/>chief of staff in T3;<br/>Grok off"]
```

| Step | Needs | Rollback |
|---|---|---|
| 1. Workers on code | S1, S2 | `notify set <worker> --url-file` |
| 2. Planner relay on code | S2, S3 (the inbox, so its lines reach the Grok chief of staff through a webhook ring) | `notify set planner --url-file` |
| 3. Chief of staff in T3 | S4, S5 | `notify set chief-of-staff --url-file` |

## 11. Build order and probes

| Step | Change | Check |
|---|---|---|
| P0 | Probes, below | each answers with a captured run |
| S1 | Profile and role settings, per-harness start, result and resume handling (section 8); `served(bot)` in place of `hasHook`; `notify set --code`, `--exec-file` | unit tests: each gate starts work for a code role and none for an unserved one |
| S2 | Code relay: line table, wider grammar, reset ring, look lock | one start after replay; concurrent profile reservations respect the cap; a unit test for each row of 3.1; two supervisors racing for the lock; a dead holder's lock taken over |
| S3 | Inbox, `wake`, `cos` verbs | an item survives a failed wake and is rung again; `cos` refuses a verb off the list |
| S4 | The wall, option A: user, sudoers lines, thread's `AGENTS.md` | a worker session's `t3 thread send` fails; the owner sends a goal from the phone and a plan run starts |
| S5 | Atlas setup and subscription billing checks | `doctor` shows `mongodb at <host>`; a run's environment holds no API key |
| S6 | The rewrite (section 9) | review |

Use a thin trial driver in P0. Repeat the routing and recovery checks against the production relay in S2. P0 starts with one shared role, review. Use a scratch board and one fixed PR head. Have Astra take the owner's goal and Claude build the change. Give Claude and Codex independent fresh reviews of that head. Each must load the role instructions, read the diff and record a verdict against the exact SHA. Capture the chosen model, effort, billing mode and session identity. Verify Astra medium and high on the installed Pro profile.

For the routing trial, occupy the eligible Claude review capacity and submit a fresh review with measured Pro capacity available. Expect one Codex review and a responsive chief of staff. Replay the handoff after launch and confirm the same run ID. Exhaust the worker reserve and confirm the next review waits with a reason. Exercise profile login loss, provider outage and restart; verify pause scope, resume affinity and owner status without a model. Qualify builds and QA separately before adding Codex to either role.

| Claim | Label | Probe |
|---|---|---|
| `t3 thread send` from a local script needs no token and queues on a busy thread | [docs] | send twice to a busy thread; read `t3 thread show` |
| `t3 thread send` exits non-zero when delivery fails | (guess) | stop T3, send, read the exit code |
| A queued send survives a T3 restart | (guess) | queue one, restart, read the thread |
| T3 pushes to the phone when the thread ends a turn | [docs] | wake it with the phone locked |
| `AGENTS.md` in the private directory reloads after compaction | (guess) | compact the thread; ask it a rule from `AGENTS.md` |
| Owner turns leave enough headroom for optional reviews | (guess) | measure ten wakes on the Pro profile and record the available usage signals |
| A `look` on Atlas runs fast enough | (guess) | time `look` on a 140-ticket board, Atlas against SQLite |
| One run's connection count fits Atlas's tier | (guess) | count connections during 20 runs |
| A repo under `/mnt/c` is much slower in WSL | (guess) | time `git status` and one build in both places |

## 12. Later: what the audits found for scale

Out of scope for this build, but kept so the work isn't redone.

### 12.1 Several machines

Each box keeps this on its own disk today, so a second box can't join until it moves into the board:

| State | Today | Needed |
|---|---|---|
| Run records, acks, `.resumed` marks | `besideStore("runs")`, per box **[code]** `tools/maestro-ticket/cli/launch.go:53` | a run index in the board: id, host, launcher, state, acked |
| Ring gate | `<bot>.gate` per box **[code]** `tools/maestro-ticket/runs/gate.go` | one per bot per board |
| Limit, QA slots, standing rules | `board.json`, `rules.md` per box **[code]** `tools/maestro-ticket/cli/rules.go:28-31` | the board |

Then: `resume` and `job again` hop to the run's own box, since transcripts and worktrees stay there. Planning jobs run only on the home box. A run's end refills its own box, then hops `refill` to the box with the most free slots, under one refill mark per box, which keeps SSH under sshd's default of 10 starts at once. Each hop is an SSH key with a forced command.

## 13. What the audits changed

| v1 said | Found | Now |
|---|---|---|
| Workers become code with no webhook | Every automatic start checks for a webhook first, so none would run | `served(bot)` (section 2) |
| The code planner handles the planner's rings | Worker runs' results, `design`, `live`, `--opus`, `--escalate` and design cards had no taker; nothing woke at a limit's reset | Full line table, reset ring (3.1) |
| Forward lines, then ack | A failed send lost the alert for good | Lasting inbox, acked only by the chief of staff (5.2) |
| The ring sends its body to the thread | Any session can type into the thread, which has a shell | The wall: `wake` with no text, `cos` verbs (section 6) |
| One look at a time | The lock never expired, and the dirty check could miss a mark | Renewable lease and atomic dirty check (3.2) |
| Forward `look --report` | A wake on every look: Grok's cost shape again | Report only when the persona says (3.1) |
| Persona as the first message | Compaction wears it down | `AGENTS.md` in the private working directory (5.1) |
| A stopped run restarts on another account | Unpushed work lost; the ticket is held till the reset anyway | Resume on the original profile (section 8) |
| Token in the run's command words | They print in the run record | Native profile login (section 8) |
| Boxes share limits, rules and runs through the board | Those live on each box's disk | Listed for later (12.1) |
