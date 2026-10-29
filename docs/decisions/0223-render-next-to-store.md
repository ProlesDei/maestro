# 0223 Render Backlog.md next to the store

- **Context:** F8. `render` wrote `<toplevel>/.maestro/Backlog.md` in whichever worktree ran it, so a session on `maestro/T-1` left `?? .maestro/` for the next `git add -A`. The brief: render in one place only, never committed on agent branches.
- **Choice:** Without `--out`, `render` writes `Backlog.md` in the store's directory: `<git common dir>/maestro/Backlog.md` by default, shared by every worktree and inside no work tree. With `--store` or `$MAESTRO_TICKET_STORE`, it goes next to that file. `--out` still writes anywhere (relative to the working directory). Skills that run plain `maestro-ticket render` need no change.
- **Alternatives:** The main worktree's `.maestro/local/` (ignored by the installer's `.gitignore`, which doesn't exist yet, so it shows as untracked until Phase 7); a path in `.git/info/exclude` (edits the user's git config area per worktree).
- **Why:** The git dir can't be committed from anywhere, and it's already where the store lives. When the installer exists, it can link the human-facing copy from `.maestro/local/` (0239).
