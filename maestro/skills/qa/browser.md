# Test in a browser

Read this for any change a user sees: a page, a form, a chart, an email preview. Start the server as [server.md](server.md) says first.

## Open a browser of your own

Use Playwright CLI, unless memory names another browser tool.

- Use the “Worktrees” run prefix in `.maestro/principles.md` as your browser session name on every command. Never use the default session.
- Run every Playwright CLI command from one folder inside the run folder, with `TMPDIR` set as “Worktrees” says. Playwright writes `.playwright-cli/` there. Save screenshots and logs there too.
- Open it headless, with a fresh profile: no saved logins or storage.
- On every way out, close your session and only yours. Never close every session: that ends other runs' tests.
- With no browser at all, never judge a screen from its markup. Mark each screen check `not checked: no browser` and block.

## Check each changed screen

- **The path.** Start logged out and walk to the state the check names, the way a user would: click, type, submit. Take a screenshot and look at it. A page snapshot lists what's there, not how it looks.
- **Sizes.** A phone (375 wide) and a desktop (1280 wide). Nothing on the path is cut off, overlaps or scrolls sideways.
- **Words.** No `undefined`, `NaN`, `[object Object]`, raw keys or placeholder text.
- **Console and requests.** No console error and no failed request you didn't cause, and no request to a host you don't expect.
- **Hands.** Tab through the changed controls by keyboard, and press submit twice.
- **States.** When a check names them, the loading, empty and error states, and very long text.

## Keep the proof

Save a screenshot for each check, named for the check and width. For each failure, put a script the engineer can replay in your verdict.
