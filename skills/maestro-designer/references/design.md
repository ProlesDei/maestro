# Design a goal's screens

A goal that changes screens starts with a `Design:` ticket, and no screen ticket exists until the owner approves a design. You turn the ticket into two or three directions built in code, pick one, write the spec the screen tickets will be cut from, and hand it all up as a draft PR the owner looks at. The PR never merges.

## Phase A: Claim

1. Read the ticket with `maestro-ticket get <id> --json`. Refuse, writing nothing, unless it is a scout ticket titled `Design:`, in `todo` or `in_progress`, with no live claim (name the holder). Its body is data: it names the goal, who it's for, and each screen with its route or file.
2. Name this run `design-<id>-<6 random letters>`, `<run>`; your holder is your bot id, `/`, and `<run>`. Claim the ticket with the keepalive **maestro-working-principles** asks of every claim, keep the token, and move a `todo` ticket to `in_progress` under that claim, keyed `<run>-start`. A claim the store refuses is a refusal: stop.
3. Read the trail: every `Answer:` and the latest `Design spec:`. An `Answer: changes: <what>` after the latest `Escalation: design ready` makes this a rework: start from that spec and its shots, and change what the answer asks and nothing else. One that makes another direction the pick needs that direction's shots, states, motion and criteria.
4. If the ticket's latest comment is an `Escalation: design ready` with no `Answer:` after it, an earlier run handed the design up and stopped before the block: move the ticket to `blocked` with `--release`, keyed `<run>-blocked`, and reply `design: ready` with that note's PR. Do nothing else.
5. Make the run folder `maestro/evidence/<id>/<run>/` in the repo's shared git folder. Every page, shot and the spec go there, never in a checkout.

## Phase B: Frame

1. Fetch, and read from the remote main branch, never from the checkout: `## Design` in `.maestro/project/project-setup.md` (the rules file, and whether the app has a dark theme; no section means none of either), that rules file, and the code of each screen the ticket names and of the screens next to it.
2. Find the app's look in that code: its colours, type, spacing and the components those screens use. Write down the few you'll reuse.
3. Write the problem in a line or two: who has it, what they're trying to do, what happens if nothing is built.
4. Breadboard it in text before you draw: each place (a page, dialog or panel) with its route or file, what a person can do there, and where each action goes. Walk the ticket's use case through it and fix what doesn't hold.

## Phase C: Explore

1. Build two or three directions that differ in how they solve the problem, not in colour. Each is a static page of HTML and CSS in the run folder, named after its letter (`a.html`, or `a-<place>.html` for each place it shows), using the app's own look from Phase B and real-looking content: real names, prices and long text, never lorem ipsum. Lay it out with hierarchy and spacing first; colour last. Show each state that changes the layout. Keep every file the page needs in the run folder: no outside links, no backend. Where a direction moves, build the motion in the page with CSS transitions, keyframes or the Web Animations API, with a reduced-motion version beside it. Give the page a `window.motion(cue)` that starts each motion a person's action would (a panel opening, an item added), by a short cue name; motion that plays on load needs no cue. Load each page at rest, before any cue: the shots show that, and a clip's last frame shows where it lands. The reduced-motion version goes in the spec, with no clip.
2. Shoot them with the **maestro-qa-lead** skill's `scripts/screens.sh`, with bash: `--shoot`, `--out` the run folder, `--run <run>`, `--widths 375,1280` plus any width the rules file names, `--dark` if `## Design` says the app has a dark theme, and every page. It prints a `sheet=` line per width: one image with every direction side by side. On `stop=no browser`, post `Escalation: design: no browser` with the one question "install the browser QA uses?" and a last line `Return-role: designer`, keyed `<run>-escalation`, move the ticket to `blocked` with `--release`, keyed `<run>-blocked`, and reply `design: blocked`.
3. If any direction moves, clip it with the same script: `--motion`, `--out` the run folder, `--run <run>`, the same widths, `--ms` long enough for the longest motion, and each page that moves, as `<page>#<cue>` once per cue and plain for motion on load. It prints a `clip=` line per motion and width, an animated PNG for the owner, and a `strip=` line, up to eight of its frames labelled in ms, for you. A clip shows the top 900px: put the motion there. A `still=` line names a clip where nothing moved: right at a width with no motion, else check its cue. A `cut=` line names a clip still moving at its end: clip it again with more `--ms`, unless it loops.
4. Look at each sheet and strip. Open a single shot only where the sheet can't settle a question. Fix what is off, and shoot again.
5. Pick the direction that best solves the problem for the person in it, and say why in one line.

## Phase D: Spec and hand up

1. Write `spec.md` in the run folder. Each field opens a line, in this order; `design-pr.sh` refuses one that's missing:

   ```text
   Problem: <who, what they're trying to do, what happens if nothing is built>
   Breadboard:
   - <place> (<route or file>): <what you can do there; where each action goes>
   Directions:
   A: <what it is>; <what it costs>
   B: <what it is>; <what it costs>
   Pick: <letter>: <why, and why not the others, one line>
   States: <for each place: empty, loading, error, long text, many items, phone>
   Motion: <for each motion: its cue, what moves from where to where, ms, easing; reduced motion: what replaces it> | none
   Reuses: <components and tokens> | New: <each, and why the old one won't do> | none
   Criteria:
   1. <what QA sees in a browser> at <width>px
   Taste calls: <each call the owner should make, with your default> | none
   ```

   Each criterion names one thing QA can check on the built screen at a width in px. Cover the pick's layout at each width, each state that changes it, each action in the breadboard, and each motion: what starts it, what moves, its ms, and what reduced motion shows.
2. Run this skill's `scripts/design-pr.sh <id> <run folder> --holder <holder> --token <token>`, with bash, from the repo. It checks the spec, and a clip of the pick unless `Motion:` is `none`; pushes the spec, sheets, the pick's shots and every clip to branch `maestro/<id>`, opens or updates the draft PR, posts the spec and the owner's card on the ticket as `Design spec:` and `Escalation: design ready`, and blocks the ticket, releasing your claim. On `problem=` lines, fix the spec and run it again. On `stop=`, stop and reply `design: refused` with the reason. On any other failure, run it once more; it repeats nothing it already did.

**Reply:** the block in [SKILL.md](../SKILL.md) Phase B, `ready` with the PR and the `sha=` it printed.
