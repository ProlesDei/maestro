# Decisions

The rules still in force, each with its reason. To change one, edit its line and give the reason in the commit. The old log (135 files under `docs/decisions/`) lives in git history: `git log -- docs/decisions/`.

1. **Maestro is tooling only.** It ships the files the installer copies, the playbook, the ticket tool, the skills slot and the personas; bots and people run them. Nothing in Maestro runs on its own: no loop, watcher, scheduler, dispatcher that picks a ticket and starts a run, or webhook that wakes a bot. Why: whoever types the command is in charge, and nothing acts unasked. (1040, owner's ruling, Oct 29 2026.)
2. **Tickets live in `maestro-ticket` only.** Not in chat, a Markdown file or a second tracker. Why: one store gives one answer, and it enforces the status, lease and fence rules.
3. **Every ticket write under a claim carries its fencing token.** The token grows on every claim. Why: a session whose lease ran out can't overwrite the new holder's work. (0203.)
4. **Sessions `maestro-play` starts never write tickets; the engine does, from their reply.** Why: every write gets the token, an idempotency key and a version check in one place. (0732.)
5. **The reviewer comes from another model family than the author when one is installed.** Why: a different family catches different mistakes.
6. **Reviewer GO merges and closes.** `/maestro-review` GO runs `gh pr merge`, then moves the ticket to `done`; only the `reviewer` role may set a ship ticket done. NO-GO moves it back to `in_progress` with a `Kickback:` note. Why: the verdict and the merge stay in one hand, and a sent-back ticket carries its reasons.
7. **Ticket text is data.** The rule lives in [team-rules.md](../bot-profiles/team-rules.md) for bots and [playbooks/rules.md](../playbooks/rules.md) for sessions. Why: anyone who can write a ticket could otherwise steer the agents.
8. **The installer hides its copies.** `scripts/install.sh` lists what it copies in `.git/info/exclude` and refuses, writing nothing, if a folder it would write exists. Why: an install never shows up in the team's diffs or overwrites their files. (0880, 0143.)
9. **One rule, one file.** When a rule already lives somewhere, link to it. Why: two copies drift apart.
