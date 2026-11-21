# Blockers

Block only on problems the PR causes or makes worse. See the exceptions in the QA only section.

## Both roles

- Fails an acceptance check.
- Breaks behavior that worked before.
- Loses or corrupts data, returns wrong data, or shows data to the wrong user.
- Opens a security hole or leaks a secret in what ships or what it prints.
- Fails a project check that passes on the base branch.

## Review only

- Adds, changes or removes agent instructions the ticket did not ask to change, including in docs, skills, prompts or comments.
- Adds a flaw in something hard to undo once shipped, such as a migration, public API or message to users.

## QA only

- The PR keeps QA from running an acceptance check.
- The app crashes or hangs on a path a check names.
- The app won't build or start, even if it also fails on main.

A check the PR names must pass even if it fails on main.
