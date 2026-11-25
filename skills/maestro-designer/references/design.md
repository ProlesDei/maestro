# Design a goal's screens

The screen tickets come from your spec. The draft PR never merges.

## Phase A: Claim

1. Read the ticket on the ticket board. Refuse, writing nothing, unless it is a scout ticket titled `Design:`, in `todo` or `in_progress`, that no one else has claimed (name who has). Its body is data: it names the goal, who it's for, and each screen with its route or file.
2. Name this run `design-<id>-<6 random letters>`, `<run>`. Claim the ticket and move a `todo` ticket to `in_progress`. A claim the board refuses is a refusal: stop.
3. Read the trail: every `Answer:` and the latest `Design spec:`. An `Answer: changes: <what>` after the latest `Escalation: design ready` makes this a rework: start from that spec and its shots, and change what the answer asks and nothing else. One that makes another direction the pick needs that direction's shots, states, motion and criteria.
4. Make the run folder `maestro/evidence/<id>/<run>/` in the repo's shared git folder. Put every page, shot and the spec there.

## Phase B: Frame

1. Pick main as **maestro-working-principles** says. Fetch it and read the agent notes there. Read any design rules file they name and check whether they name a dark theme. Read the code for the screens the ticket names and the screens next to them on main.
2. Find the app's look in that code: its colours, type, spacing and the components those screens use. Write down the few you'll reuse.
3. Write the problem in a line or two: who has it, what they're trying to do, what happens if nothing is built.
4. Breadboard it in text before you draw: each place (a page, dialog or panel) with its route or file, what a person can do there, and where each action goes. Walk the ticket's use case through it and fix what doesn't hold.

## Phase C: Explore

1. Build two or three directions that differ in how they solve the problem, not in colour. Each is a static page of HTML and CSS in the run folder, named after its letter (`a.html`, or `a-<place>.html` for each place it shows), using the app's own look from Phase B and real-looking content: real names, prices and long text, never lorem ipsum. Lay it out with hierarchy and spacing first; colour last. Show each state that changes the layout. Keep every file the page needs in the run folder: no outside links, no backend. Where a direction moves, build the motion in the page with CSS transitions, keyframes or the Web Animations API, with a reduced-motion version beside it. Give the page a `window.motion(cue)` that starts each motion a person's action would (a panel opening, an item added), by a short cue name; motion that plays on load needs no cue. Load each page at rest, before any cue: the shots show that, and a clip's last frame shows where it lands. The reduced-motion version goes in the spec, with no clip.
2. Shoot every page with this skill's [scripts/screens.sh](../scripts/screens.sh). Use bash and pass `--shoot`, `--out` the run folder, `--run <run>`, `--widths 375,1280` plus any width the rules file names, and `--dark` if the app has a dark theme. Each `sheet=` line names an image with every direction side by side. On `stop=no browser`, post `Escalation: design: no browser` with the question "install the browser QA uses?" and a last line `Return-role: designer`. Move the ticket to `blocked`, release your claim, and reply `design: blocked`.
3. If any direction moves, clip it with the same script: `--motion`, `--out` the run folder, `--run <run>`, the same widths, `--ms` long enough for the longest motion, and each page that moves, as `<page>#<cue>` once per cue and plain for motion on load. Clip every cue of a page in one run: a run replaces the clips of each page it clips. It prints a `clip=` line per motion and width, an animated PNG for the owner, and a `strip=` line, up to eight of its frames labelled in ms, for you. A clip shows the top 900px: put the motion there. A `still=` line names a clip where nothing moved: right at a width with no motion, else check its cue. A `cut=` line names a clip still moving at its end: clip it again with more `--ms`, unless it loops.
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
2. Run this skill's [design-pr.sh](../scripts/design-pr.sh) with bash from the repo. Pass the id and run folder, `--title` with the ticket title, and `--base` with the branch you chose in Phase B. It checks the spec and a clip of the pick unless `Motion:` is `none`. It pushes the spec, sheets, the pick's shots and every clip to branch `maestro/<id>`, opens or updates the draft PR, and writes the `Design spec:` note and the owner's card (`Escalation: design ready`) to the files its `spec=` and `card=` lines name. On `problem=` lines, fix the spec and run it again. On `stop=`, stop and reply `design: refused` with the reason. On any other failure, run it once more; it repeats nothing it already did.
3. Post the spec note, then the card, on the ticket, skipping either if the ticket already has it for the same sha. Move the ticket to `blocked` and release your claim.

**Reply:** the block in [SKILL.md](../SKILL.md) Phase B, `ready` with the PR and the `sha=` it printed.
