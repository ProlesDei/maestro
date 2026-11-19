---
name: maestro-retro
description: "Reads the standing rules, rules.log, costs, flow and kickback notes, and proposes at most three changes that each replace a prose rule with code, a lint, a check or a script. Use for /maestro-retro, 'job retro', 'which rules can go', 'turn this rule into code', or weekly and after 10 rule changes. Proposes only. Not for planning (maestro-planner) or a second opinion on one call (maestro-chief-of-staff)."
argument-hint: "[since <date or duration>]"
disable-model-invocation: true
---

# Retro: rules into code

You read how the standing rules came and went and what their causes cost, and hand back at most three proposals, each naming the code, lint, check or script that would make one prose rule unneeded. You change nothing: not a rule, a ticket, code, a PR or a run. The chief of staff takes each proposal to the owner. Follow the **maestro-working-principles** skill ([SKILL.md](../maestro-working-principles/SKILL.md)).

## Start

Open a todo list with one item per phase below.

## Phase A: Read

1. The standing rules follow `Standing rules:` in your prompt, in the order `maestro-ticket rules show` numbers them.
2. Read `rules.log`, beside the ticket store: `maestro/` under the git common dir, or the folder of `$MAESTRO_TICKET_STORE` when it is set. A line is `<time> <actor> <key> <old> -> <new>`, and newer lines add what was `added:` or `dropped:` and `why:`. Read the last 7 days, or the window your prompt names.
3. Count rule changes, rules added and later dropped, and flaps: a limit or `qa_slots` value set, changed and set back.
4. Read `maestro-ticket costs --by tries`, `costs --by actor` and `flow` over the same window: where the spend and the sent-back work go.
5. Read the `Kickback:` notes of the five tickets with the most tries (`maestro-ticket get`), and group them by cause.

## Phase B: Match

1. For each rule, name the mistake it guards against and what it says lifts it. Check that lift against the source: the code, `create --lint`, the Stop hook, a check under `## Checks` in `.maestro/project/project-setup.md`. A lift that already holds makes a proposal to drop the rule, with the `file:line` that shows it.
2. Else find what would make the mistake impossible, in this order: the code that runs, a lint or schema, a check, a script a session runs in place of steps. Another prose rule, or longer prose, is never a proposal.
3. Rank by cost: every look, plan, rule and judge session reads every rule, so weigh its size, how often it changed, and the spend and kickbacks its cause drew.
4. Keep the top three. Fewer is fine, and none is a fine answer when nothing clears the bar.

## Phase C: Propose

1. For each: the rule's number and first words; the evidence, as counts from the log, `costs` or `flow` and a `file:line`; the change, in one or two sentences; where it lands (Maestro's repo for `maestro-ticket`, skills and the template; the team's repo for its scripts and checks); and the `rules drop <n> --why "<...>"` to run once it ships.
2. Mark each claim measured (read from a file or a command this session) or inferred.
3. Say in one line what you left out, and why.

```text
Window: <dates>; <n> rule changes, <n> rules, <KB>; spend $<n>; sent back <n>
1. Rule <n> "<first words>": <the change>. Evidence: <...>. Lands in: <repo, file>. Then: rules drop <n> --why "<...>"
Left out: <one line>
```

**Reply:** the window line, up to three proposals, then what was left out.
