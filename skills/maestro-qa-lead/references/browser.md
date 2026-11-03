# Check it in a browser

Read this for anything a user sees: a page, a form, an email preview, a chart. A page read as HTML, or an API that returns 200, proves nothing about the screen.

## Pick the tool

1. Use the browser `## QA` in `.maestro/project/project-setup.md` names, read from main's copy as [test-a-pr.md](test-a-pr.md) Phase A says. Else use what the harness has: a browser tool, a browser MCP server, or a headless browser driven by a script (Playwright or the like) if the project already has one.
2. Run it headless, with a profile of your own and no saved logins, so no other session shares its cookies or storage.
3. With no browser at all, never guess from markup. Mark each screen check `not checked: no browser` and reply `test: blocked: no browser` as Phase D of [test-a-pr.md](test-a-pr.md) says.

## Check each changed screen

1. **The path.** Start logged out with empty storage and walk to the state the criterion names. Screenshot it as `<n>-<slug>-<width>.png`.
2. **Sizes.** At least a phone (375 wide) and a desktop (1280 wide), plus any `## QA` names. Nothing cut off, overlapping or scrolling sideways.
3. **Console and network.** No new console error or warning next to the same screen on the main branch. No failed request except the ones you caused, and none to a host you don't expect.
4. **States.** Loading, with the network slowed; empty; an error, with the API made to fail; very long text, non-Latin text and a name with an apostrophe; a list of hundreds.
5. **Hands.** Keyboard only: tab order, visible focus, Enter and Escape. Double-click every submit. Use back, reload mid-flow, and two tabs at once.
6. **Words.** No `undefined`, `NaN`, `[object Object]`, raw keys like `login.title`, placeholder text or typos.
7. **Access.** Every input has a label and every image alt text. Run an accessibility checker if one is at hand.
8. **Nearby.** Screenshot one screen next to the change on both the main branch and `H`. Any difference the PR doesn't explain is a finding.
