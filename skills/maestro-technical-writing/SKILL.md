---
name: maestro-technical-writing
description: A layered standard for docs, RFCs, READMEs, PR descriptions and commit messages. Diataxis picks the type, Google developer style sets the voice, Simplified Technical English shapes the sentences and Global English makes it read for everyone. Named in tickets, propose and draft-setup steps.
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Technical writing

Apply the four layers in order. In your own session, run `/maestro-unslop` as the last pass. In a playbook step, don't: it's a command for the user, and the step's prompt names the skills it needs.

## 1. Diataxis: what kind of document is it?

Each document is one type. If you're mixing types, split the document.

| Type | Reader wants to | Shape |
|---|---|---|
| Tutorial | learn by doing | one path, from start to a working result, nothing optional |
| How-to | finish a task | goal, prerequisites, numbered steps, a result they can check |
| Reference | look something up | complete and consistent: tables, every flag, no story |
| Explanation | understand why | context, the trade-offs, the alternatives, the reasoning |

## 2. Google developer style: voice

- Second person ("you"), active voice, present tense.
- Use sentence case in headings, and start each heading with the task or the topic.
- Put the condition before the instruction: "If the build fails, run…".
- Number the steps, with one action per step. Put each command in a code block on its own line.
- Use code font for code, commands, paths, flags and values. Use bold only for UI labels.
- Write link text that names the target. Never write "click here".

## 3. Simplified Technical English (ASD-STE100): sentences

- Keep procedural sentences to 20 words or fewer, and descriptive ones to 25 or fewer.
- Give one instruction per sentence and one topic per paragraph.
- Give each word one meaning in the document: if "run" means execute, it never means a test run. Use the same term for the same thing every time.
- Prefer simple verbs: "use" not "utilise", "start" not "initiate", "show" not "display".
- Write "the server sends" or "send the request", never "sending of the request is performed".

## 4. Global English: readable by everyone

- No idioms, sports or cultural references, or humour that doesn't translate.
- Use a single verb instead of a phrasal verb where one exists ("remove", not "get rid of").
- Keep "that" and the other small words that make the grammar explicit. Keep subjects explicit too.
- Write dates in ISO format (2026-10-29), give units and time zones, and spell out an abbreviation the first time.

## Per artifact

- **README:** what it is (one sentence), why you'd use it, a quickstart that works when pasted, then links to everything else.
- **RFC or decision:** context, choice, alternatives, why, open questions. In Maestro, use the decision-log format (Context, Choice, Alternatives, Why).
- **PR description:** what changed, why, how it was verified (commands and results), risk and rollback, and what's out of scope.
- **Commit message:** a subject line of 72 characters or fewer that says what changed. A body that says why, and anything the diff can't show. No list of the files changed.
- **Ticket body:** follow `/maestro-planner`: a goal, testable criteria, pointers by path.

## Check before you hand it on

- Can the reader do or decide the thing in one pass, without asking?
- Did you run each command and copy it from that run?
- Is every term used the same way throughout?
- Is everything that doesn't serve the reader cut?
