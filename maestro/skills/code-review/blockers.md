# Blockers

Block a PR if it does any of these:

- Fails an acceptance check or keeps you from running it. A check the PR names must pass even if it fails on main.
- Breaks behavior that worked before.
- Loses or corrupts data, returns wrong data, or shows data to the wrong user.
- Opens a security hole or leaks a secret in what ships or what it prints.
- Stops the app from building or starting, or makes it crash or hang on a path a check names.
- Fails a project check that passes on the base branch.
- Adds, changes or removes agent instructions the ticket did not ask to change, including in docs, skills, prompts or comments.
- Adds a flaw in something hard to undo once shipped, such as a migration, public API or message to users.

Apart from acceptance checks, block only on problems the PR causes or makes worse.
