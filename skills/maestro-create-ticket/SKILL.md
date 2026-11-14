---
name: maestro-create-ticket
description: "Adds one ticket to the Maestro board with one maestro-ticket create call, after asking once for what it can't infer. Use for /maestro-create-ticket, 'make a ticket for this', 'add this to the board', or when work has no ticket yet."
argument-hint: "[what needs doing]"
disable-model-invocation: true
---

# Create a ticket

You draft one ticket, ask once, and make one `maestro-ticket create` call. This skill owns the ticket shape. The store checks deps and cycles: don't redo that here.

Follow the **maestro-working-principles** skill.

1. **Draft.** From the user's words (or ask for one sentence), write:
   - a title: a verb and an object, under 70 characters, never "Improve X";
   - a body: the goal in one or two sentences, numbered acceptance criteria, the paths to start from and the closest code to copy, what must not change, and for each dep the code this ticket needs from it.

   A criterion is something a command or the running app can show, so QA can test it as written. One that restates the title, or that only taste can judge, is not a criterion: rewrite it. Read the code each criterion touches on the main branch: rewrite one the code already contradicts, or that pulls against another, since the engineer would stop and ask.
2. **Look around.** Run `maestro-ticket list --status backlog,todo,in_progress,in_review,blocked --json | jq -c '.tickets[] | {id,title,areas}'` and compare titles and areas. If one is close, show it and ask whether to go on. Check each path you named exists.
3. **Ask once.** In one message, ask only for the fields you can't infer, each with your pick so the user can say "yes":

   | Field | Values | Default |
   |---|---|---|
   | deps | ticket ids that must be `done` first; check each with `maestro-ticket get` | none |
   | areas | repo-relative paths the change touches, as narrow as you can, with the tests the criteria need. Two tickets with overlapping areas never run at once | from the paths |
   | priority | `0` (most urgent) to `3` | `2` |
   | status | `todo` (ready) or `backlog` (not ready) | `todo` if every criterion can be checked |
   | parked | why a `backlog` ticket waits: `hold` (someone said wait), `deferred` (cut from this slice) or `unready` (a criterion can't be checked yet) | `unready` |
   | goal | slug of the goal it serves, so the board counts progress per goal | the goal you were given, else none |
   | role | who builds it: `engineer`, the one role the store takes that builds tickets. QA and review take only built tickets, so they are never a ticket's role | `engineer` |

   Waiting on another ticket is a dep, never `blocked` or `backlog`.
4. **Create.** Show the command, then run it:

   ```sh
   maestro-ticket create --json --title '<title>' --priority <n> --status <todo|backlog> --role <role> \
     --deps '<T-1,T-2>' --areas '<path,path>' --goal '<slug>' --parked <reason> \
     --idempotency-key 'create:<yyyy-mm-dd>:<title slug>' \
     --body-file - <<'END_<8 random letters>'
   <body>
   END_<the same 8 letters>
   ```

   - Leave out `--type`: the default, `ship`, is a ticket that ends in a merged PR.
   - The title, body, deps and areas come from the user, so the shell must never expand them. Single-quote each flag value and write each `'` as `'\''`. Keep the heredoc delimiter quoted and new each time, as **maestro-working-principles** says for quoted text.
   - The slugs are lowercase letters, digits and hyphens. Leave out `--deps`, `--areas` or `--goal` when empty, and `--parked` unless the status is `backlog`.
   - A ticket that replaces a cancelled one (a re-file) gets `--split-from '<old id>'`, so its tries carry over and `maestro-ticket costs` counts the old ticket's runs toward it. One made without it can be linked later with `maestro-ticket update '<new id>' --split-from '<old id>'`.
   - A `hold` needs a note right after: comment `Hold: set by <the owner, or the bot>, <date>: <why>; lifts when <a fact the board shows>`, sent on stdin in a heredoc as the body was.
   - If the store refuses, show its message, fix only the field it names, and run again with the same key. On `idempotency_conflict`, another ticket got this title today: read it; if it is this work and not `cancelled`, reply with its id, else change the title, and with it the key.

**Reply:** the new id, title, status and its parked reason, priority, goal, deps and areas, then `Next: /maestro-software-engineer <id>`.
