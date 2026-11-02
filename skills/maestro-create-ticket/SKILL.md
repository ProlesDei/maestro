---
name: maestro-create-ticket
description: "Adds one ticket to the Maestro board with one maestro-ticket create call, after asking once for what it can't infer. Use for /maestro-create-ticket, 'make a ticket for this', 'add this to the board', or when work has no ticket yet."
argument-hint: "[what needs doing]"
disable-model-invocation: true
---

# Create a ticket

You draft one ticket, ask once, and make one `maestro-ticket create` call. This skill owns the ticket shape; a team can install its own in its place. The store checks deps and cycles: don't redo that here.

Follow the **maestro-working-principles** skill.

1. **Draft.** From the user's words (or ask for one sentence), write:
   - a title: a verb and an object, under 70 characters, never "Improve X";
   - a body: the goal in one or two sentences, numbered acceptance criteria, the paths to start from and the closest code to copy, and what must not change.

   A criterion is something a command or the running app can show, so QA can test it as written. One that restates the title, or that only taste can judge, is not a criterion: rewrite it.
2. **Look around.** Run `maestro-ticket list --json` and compare titles and areas. If one is close, show it and ask whether to go on. Check each path you named exists.
3. **Ask once.** In one message, ask only for the fields you can't infer, each with your pick so the user can say "yes":

   | Field | Values | Default |
   |---|---|---|
   | kind | `feature`, `bug`, `refactor`, `perf`, written as the body's `Type:` line. The engineer picks the test approach from it | from the wording, else `feature` |
   | deps | ticket ids that must be `done` first; check each with `maestro-ticket get` | none |
   | areas | repo-relative paths the change touches, as narrow as you can. Two tickets with overlapping areas never run at once | from the paths |
   | priority | `0` (most urgent) to `3` | `2` |
   | status | `todo` (ready) or `backlog` (not ready) | `todo` if every criterion can be checked |

   Waiting on another ticket is a dep, never `blocked`.
4. **Create.** Show the command, then run it:

   ```sh
   maestro-ticket create --json --title '<title>' --priority <n> --status <todo|backlog> \
     --deps '<T-1,T-2>' --areas '<path,path>' \
     --idempotency-key 'create:<yyyy-mm-dd>:<title slug>' \
     --body-file - <<'MAESTRO_TICKET_BODY'
   Type: <kind>
   <body>
   MAESTRO_TICKET_BODY
   ```

   - The body's first line is `Type: <kind>`: the engineer reads it. It is not `maestro-ticket create --type`, which takes `ship` or `scout`; leave that flag out (the default is `ship`).
   - The title, body, deps and areas come from the user, so the shell must never expand them. Single-quote each flag value and write each `'` as `'\''`. Keep the heredoc delimiter quoted; if a body line is exactly `MAESTRO_TICKET_BODY`, pick another.
   - The slug is lowercase letters, digits and hyphens. Leave out `--deps` or `--areas` when empty.
   - If the store refuses, show its message, fix only the field it names, and run again with the same key.

**Reply:** the new id, title, kind, status, priority, deps and areas, then `Next: /maestro-software-engineer <id>`.
