# Check it in a browser

Read this for anything a user sees: a page, a form, an email preview, a chart. A page read as HTML, or an API that returns 200, proves nothing about the screen.

## Open a browser of your own

Many runs share this machine and its browsers. Keep yours apart from theirs.

1. Use Playwright CLI (`playwright-cli`), unless `## QA` in `.maestro/project/project-setup.md`, read from main's copy as [test-a-pr.md](test-a-pr.md) Phase A says, names another browser tool for sessions to drive (a test command, such as an e2e suite, is a check, not one): then use that one, the same way. Its help on a command gives that command's options. Skip the agent skill its help names: it runs everything in one shared session and closes every browser.
2. Name the browser session `<run>`, your run's name, on every command. Never use its default session: every other run shares it. A job whose reference names no run (second look, repro, answer) names one `qa-<job>-<6 random letters from the OS>` and keeps every file it writes inside `maestro/evidence/<job>/<run>/` in the repo's shared git folder, none beside it.
3. Open it headless, in Playwright's Chromium (`chromium`), with its in-memory profile (no saved profile or logins) and a 15-minute idle timeout (900000 ms), so a run that dies doesn't hold its browser for an hour. If it has closed by the time you come back, open it again and walk the path from a fresh start.
4. Run every browser command from the evidence folder, so the snapshots, logs and screenshots it writes land there, not in the checkout. Give a file a bare name, never a path outside that folder.
5. When you finish or stop, close session `<run>` and only it. Never close or kill every session: that ends other runs' browsers mid-test.
6. With no browser at all, never guess from markup: mark each screen check `not checked: no browser`. In a test, reply `test: blocked`, with `no browser` under `Answer:`, as Phase D of [test-a-pr.md](test-a-pr.md) says.

## Check each changed screen

1. **The path.** Start logged out with empty storage and walk to the state the criterion names. Screenshot it as `<n>-<slug>-<width>.png`, then open the image and look at it: a page snapshot lists what is on the page, not how it looks.
2. **Sizes.** At least a phone (375 wide) and a desktop (1280 wide), plus any `## QA` names. Nothing cut off, overlapping or scrolling sideways.
3. **Console and network.** No new console error or warning next to the same screen on the main branch. No failed request except the ones you caused, and none to a host you don't expect.
4. **States.** Loading, with the network slowed; empty; an error, with the API made to fail; very long text, non-Latin text and a name with an apostrophe; a list of hundreds.
5. **Hands.** Keyboard only: tab order, visible focus, Enter and Escape. Double-click every submit. Use back, reload mid-flow, and two tabs at once.
6. **Words.** No `undefined`, `NaN`, `[object Object]`, raw keys like `login.title`, placeholder text or typos.
7. **Access.** Every input has a label and every image alt text. Run an accessibility checker if one is at hand.
8. **Nearby.** Screenshot one screen next to the change on both the main branch and `H`. Any difference the PR doesn't explain is a finding.
9. **Rerun.** Record the steps of each failure as Playwright code and save it in the evidence folder, so the engineer can replay it.
