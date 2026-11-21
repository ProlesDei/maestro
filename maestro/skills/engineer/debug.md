# Fix a bug

Find the cause before you change code.

1. **Reproduce it.** Write the steps, what you expected and what happened, then make it happen on your branch. Use the path a user takes, or a throwaway script when that path is slow. If nothing reproduces it, stop and report what you tried.
2. **Find where it starts, not where it shows.** Follow the bad value back to the first line that made it. Test one guess at a time with a run, not by reading.
3. **Fix it at the cause.** Search for the same mistake elsewhere. Fix the places the ticket covers and list the rest in the PR.
4. **Prove it.** The repro fails without the fix and passes with it. Keep both outputs for the PR.

When two fixes built on the same idea fail, stop fixing and test the idea. The cause may sit in code you assumed was fine.
