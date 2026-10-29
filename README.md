# maestro

An automated agentic workflow that takes products from plan to ship, packaged so you can install it into any project.

maestro bundles a full suite of agent roles, playbooks and tools. Install it, give it context about your project, and the workflow is ready to run. No per-project setup of agents, prompts, or tooling.

## What's included

| Part | What it is |
|---|---|
| Roles | Agent definitions. Each role owns one part of the delivery process. |
| Playbooks | Each role's jobs as YAML workflows in `playbooks/<role>/`: typed steps for ticket writes, runner requests, checks, waits, questions for the bot, and prompts rendered from the templates in `playbooks/prompts/`. `maestro-play` runs one per ticket, all its prompt steps in one harness session. See `playbooks/README.md`. |
| Skills | Yours. `skills/` holds your own skills, and `skills/use.yaml` says which prompt steps and bots name them and which install as slash commands. Maestro's playbooks, templates and personas name none, so nothing depends on a skill. See `skills/README.md`. |
| Tools | Commands that do the work. `tools/maestro-play/` is the playbook engine. `tools/maestro-ticket/` is the ticket system: a CLI over a SQLite store shared by every worktree, with leases, versioned transitions, an event log and a generated `Backlog.md`. See `tools/maestro-ticket/README.md`. `tools/maestro-usage/` records usage and checks budgets. |
| Evals | One golden task per playbook in `evals/<role>/`, the situation and the result it must produce, for catching regressions once the eval runner exists. `maestro-play lint` checks the playbooks; `evals/check.py` checks the tasks and docs. See `evals/README.md`. |
| Bot profiles | One folder per bot at `bot-profiles/<name>/persona.md`, plus any supporting files in that folder, and `bot-profiles/team-rules.md` shared by all of them. Outside tools, such as Grok, read a folder and copy it to create the bot. |

## How it works

1. **Install** maestro into your project.
2. **Add project context**: what you're building, the stack, build and test commands, conventions.
3. **Run the workflow.** The agents plan, build, check, and ship.

### The agent team

You talk to one agent: the Chief of Staff. It turns your goals into workstreams, each run by its own Planner session, which writes the tickets. Engineers pick up tickets from the ticket system (`maestro-ticket`). Ship tickets pass automated checks, a code review and QA before the Operations Manager releases them, within the trust level you set; scout tickets end at an evidenced report. Each role runs on the cheapest model tier that can do the job; color marks the tier. The rules every bot follows are in `bot-profiles/team-rules.md`.

<!-- Source: docs/team.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    User([User])

    CoS["<b>Chief of Staff</b><br/>Frontier<br/>Goals and workstreams, never tickets"]
    Planner["<b>Planner sessions</b><br/>Mid-tier<br/>One per workstream, owns its tickets"]
    Tickets[("<b>Ticket system</b><br/>maestro-ticket<br/>Ship and scout tickets, status, blockers<br/>Backlog.md and Jira are mirrors")]

    subgraph Build ["Build, review and test: pools of parallel sessions"]
        subgraph Engineers ["Engineers"]
            SSE["<b>Senior Engineer</b><br/>Frontier"]
            JSE["<b>Junior Engineer</b><br/>Mid-tier"]
        end
        Checks["<b>Automated checks</b><br/>Runner, no tokens"]
        CR["<b>Code Reviewer</b><br/>Mid-tier, another model<br/>family from the author"]
        QA["<b>QA Lead</b><br/>Mid-tier<br/>Evidence from the running app"]
    end

    Ops["<b>Operations Manager</b><br/>Mid-tier"]
    Logs[("<b>Logs, usage and memory inbox</b><br/>Retros, failures, token cost,<br/>memory proposals")]
    Done([Released])

    %% User and Chief of Staff
    User <-->|"goals, decisions, approvals,<br/>digest and recap"| CoS

    %% Planning
    CoS -->|"workstream brief"| Planner
    Planner -->|"writes specs and tickets"| Tickets
    Planner -.->|"workstream summary,<br/>escalates above its authority"| CoS

    %% Status and blockers flow through the ticket system
    Tickets -.->|"status, blockers, escalations,<br/>scout reports"| Planner

    %% Engineers pick up work
    Tickets -->|"complex or risky"| SSE
    Tickets -->|"routine"| JSE
    JSE -.->|"escalates after 2 tries"| SSE
    Engineers -.->|"updates status, escalates,<br/>scout reports"| Tickets

    %% Build, review, test
    Engineers -->|"submits ship ticket"| Checks
    Checks -->|"passed"| CR
    CR -->|"approved"| QA
    Checks -.->|"failed"| Engineers
    CR -.->|"changes requested: +1 try"| Engineers
    QA -.->|"failed: +1 try"| Engineers

    %% Release
    QA -->|"passed: ready to release"| Ops
    Ops -.->|"deploy needs approval"| CoS
    Ops -->|"opens PR; merges and deploys<br/>only as the trust level allows"| Done
    Ops -->|"marks ship ticket done after release"| Tickets

    %% Operations
    CoS -->|"infrastructure requests"| Ops
    Build -.->|"logs"| Logs
    Logs -->|"curates memory, reviews costs and failures"| Ops
    Ops -.->|"suggests one tested workflow change"| CoS

    %% Legend
    subgraph Legend ["Legend"]
        direction LR
        L1["Frontier model"] ~~~ L2["Mid-tier model"] ~~~ L3["No tokens"] ~~~ L4[("Shared storage")] ~~~ L5["Solid line: work<br/>Dotted line: feedback,<br/>status or escalation"]
    end

    %% Styles by cost tier
    classDef frontier fill:#f8c4b4,stroke:#b5472b,color:#1a1a1a
    classDef mid fill:#bcd6f5,stroke:#2f5f9e,color:#1a1a1a
    classDef free fill:#c8e6c9,stroke:#2e7d32,color:#1a1a1a
    classDef storage fill:#e6dcf5,stroke:#6a4c9c,color:#1a1a1a
    classDef person fill:#f5f5f5,stroke:#555,color:#1a1a1a
    classDef note fill:#ffffff,stroke:#999,color:#1a1a1a,stroke-dasharray:3 3

    class CoS,SSE,L1 frontier
    class Planner,JSE,CR,QA,Ops,L2 mid
    class Checks,L3 free
    class Tickets,Logs,L4 storage
    class User,Done person
    class L5 note
```

### Bot anatomy

An outside bot runtime, for example Grok Bot, creates a bot for each persona in `bot-profiles/`. Every persona operates under this same anatomy. The bot runtime loads the role's profile (`persona.md`, the team rules and the role's playbooks) outside the user's repo. The bot starts each job with the runner's `play` request, and `maestro-play` runs the rest: preflight, intake, the claim, each step, the structured reply checked against the step's schema, the handoff or escalation, the log line and clean-up. The bot decides only where the playbook asks it to, and handles escalations. It never touches the repo: harness sessions do the hands-on work, and the runner (`docs/runner.md`) checks every request against the role's permissions and the trust level.

<!-- Source: docs/bot-anatomy.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    Runtime["<b>Bot runtime</b> · outside their repo<br/>Any agent, e.g. a Grok or Claude bot<br/>Thinks and decides. Never touches files or runs commands<br/><i>How it's started is out of scope</i>"]

    subgraph Profile ["Bot profile: loaded by the bot runtime, never installed into their repo"]
        direction LR
        P1["<b>persona.md</b><br/>Persona and<br/>decision authority"] ~~~ P2["<b>team-rules.md</b><br/>Rules every bot<br/>follows, in one place"] ~~~ P3["<b>playbooks/</b><br/>YAML workflows and<br/>prompt templates"] ~~~ P5["<b>skills/use.yaml</b><br/>the user's own skills,<br/>if any"]
    end

    Engine["<b>maestro-play</b> · the playbook engine, no tokens<br/>Runs one playbook's steps in order, keeps the run state<br/>Stops only to ask the bot or to block"]

    Runner["<b>Runner</b> · no tokens<br/>The bot's only way into their repo<br/>Named requests, allowed by role and trust level<br/>Stop file checked first · usage recorded"]

    subgraph Loop ["Playbook run: identical for every role"]
        B0["<b>Preflight</b>, through the runner<br/>Stop file set? Stop<br/>Health check: harness logged in, maestro-ticket doctor passes, git clean<br/>Environment, trust level, budgets"]
        B1["<b>Harness sessions get launch flags</b><br/>playbooks/rules.md · role permissions · model tier<br/>Their config files stay untouched<br/>Stable content first, so it gets cached"]
        B2["<b>Intake</b> · maestro-play --next<br/>Requests → work sent back → new work<br/>Scheduled duties run by name · a crashed run resumes first"]
        B3["<b>Claim</b>, by the engine<br/>The token goes into the run state<br/>The keepalive holds the lease in the background"]
        B4["<b>Next step</b><br/>ticket · run · prompt · gate · wait · ask<br/>No tokens unless it's a prompt"]
        B5["<b>Check the reply</b><br/>The base schema plus the step's out: fields<br/>Evidence included?"]
        B6{"on: outcome"}
        B7["<b>Built-in done or block</b><br/>memory to the inbox · Handoff: or Escalation: note ·<br/>move and release · log line with the retro"]
        B8["<b>Built-in clean-up</b><br/>Listed processes · clean worktree · run state<br/>Then close the harness session"]
        B9{"More in intake?"}
        B10(["Run ends"])
    end

    Contract["<b>playbooks/prompts/_contract.md</b><br/>first in every prompt, never changes<br/>then the skills use.yaml names,<br/>then the step's template<br/><br/>Same session across a playbook's steps ·<br/>fresh session per ticket"]

    subgraph HarnessSide ["Their repo · the harness does all the work"]
        H["<b>Harness session</b>, headless<br/>Claude Code · Codex · Grok Build"]
        Loads["<b>Uses our footprint only</b><br/>project/ setup and memory<br/>skills named in use.yaml, loaded only when named"]
        Work["<b>Does the hands-on work</b><br/>Ticket system CLI: maestro-ticket · files and code ·<br/>commits on maestro/ branches · tests and the running app"]
    end

    Guard["<b>Guardrails</b><br/>Trust levels: propose-only at first · stop file ·<br/>role permissions · budgets and retry limits ·<br/>approval gates · secrets never in prompts, replies or logs"]

    subgraph Roles ["Same anatomy, different profiles: what each role lets the harness do"]
        direction LR
        E1["<b>Chief of Staff</b><br/>Read summaries ·<br/>goals and workstreams"] ~~~ E2["<b>Planner</b><br/>One workstream's tickets ·<br/>read-only code"] ~~~ E3["<b>Engineers</b><br/>Edit and commit on<br/>maestro/ branches"] ~~~ E4["<b>Code Reviewer</b><br/>Read-only code<br/>and diffs"] ~~~ E5["<b>QA Lead</b><br/>Run tests and the app ·<br/>read-only code"] ~~~ E6["<b>Operations</b><br/>PRs, merges, deploys by trust level ·<br/>memory · improvement loop"]
    end

    Runtime -->|"loads the profile it's configured for"| Profile
    Profile -->|"play run, through the runner"| Engine
    Engine --> B0
    Guard -.-> B0
    B0 --> B1 --> B2 --> B3 --> B4
    B4 -.->|"ask or block: the bot answers<br/>with play resume"| Runtime
    Contract -.->|"shapes every prompt"| B4
    B0 -.->|"requests"| Runner
    B4 -.->|"no-token steps"| Runner
    Runner ==>|"launches with flags"| H
    B4 ==>|"prompt"| H
    H ==>|"structured reply"| B5
    B5 --> B6
    B6 -->|"ok or done: next step,<br/>or the step on: names"| B4
    B6 -->|"missing a field:<br/>retry, maybe a tier up"| B4
    B6 -->|"blocked, failed or over<br/>step_visits: block"| B7
    B6 -->|"last step, or on: done"| B7
    B7 --> B8 --> B9
    B9 -->|"yes: next run"| B0
    B9 -->|"no"| B10
    Loads --> H
    H --> Work
    B10 ~~~ Roles

    classDef runtime fill:#f5f5f5,stroke:#555,color:#1a1a1a
    classDef profile fill:#e6dcf5,stroke:#6a4c9c,color:#1a1a1a
    classDef shared fill:#fff3c4,stroke:#b38f00,color:#1a1a1a
    classDef step fill:#f8c4b4,stroke:#b5472b,color:#1a1a1a
    classDef harness fill:#bcd6f5,stroke:#2f5f9e,color:#1a1a1a
    classDef ours fill:#c8e6c9,stroke:#2e7d32,color:#1a1a1a
    classDef role fill:#ffffff,stroke:#6a4c9c,color:#1a1a1a
    classDef note fill:#ffe0b2,stroke:#e65100,color:#1a1a1a

    class Runtime,B10 runtime
    class P1,P2,P3 profile
    class P5,Contract shared
    class B0,B1,B2,B3,B4,B5,B6,B7,B8,B9 step
    class H,Work harness
    class Loads,Runner,Engine ours
    class E1,E2,E3,E4,E5,E6 role
    class Guard note
```

## Install

From a clone of this repo, with the target already a git repo; give the top of its work tree:

```sh
scripts/install.sh /path/to/your-repo
```

It copies every folder in `skills/` into the target's `.claude/skills/` (Claude Code) and `.agents/skills/` (Codex, Grok Build), creates `.maestro/project/` and `.maestro/local/`, hides the skill folders and `.maestro/local/` from git through `.git/info/exclude`, and creates the ticket store if `maestro-ticket` is on your PATH. If any of those skill folders already exists it stops and writes nothing, and if a later step fails it removes what it wrote. Then run `chief-of-staff/setup-project`. To uninstall, run the lines at the top of `scripts/install.sh` in the target repo; the second also deletes your tickets. Decisions 0880 and 0881 say why the installer is this small.

## Supported harnesses

maestro runs on the three main coding agents:

- Claude Code
- Codex
- Grok Build

Installed sessions get Maestro's rules through launch flags and never rely on, or edit, your `AGENTS.md` or `CLAUDE.md`. In this repo, `AGENTS.md` holds the instructions for developing Maestro itself; `CLAUDE.md` imports it.

## Status

Early. The roles, playbooks, the playbook engine and the ticket store are written; `scripts/install.sh` installs the skills and folders (see Install). The runner and the wake loop aren't built yet.
