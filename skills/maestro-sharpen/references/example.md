# One rewrite

Before:

> Look into why the nightly export job is slow and fix it.

The expert: a staff engineer who runs data pipelines. They stay out of the prompt; their standards go in.

After:

```text
Find why the nightly export job got slow, and fix the cause. [Why: what waits on the export, and by when.]

Settled: fix it, don't just report it. Off limits: the export's output, a longer timeout, and a cache that hides the cause.

Read first: the job's entry point and its cron config [find them: search for the export job's name], the run logs for the last 14 nights, and `git log` on the export code over the same window.

Done: run time back under [the old baseline | a target you set] on production-sized data, with proof:
- the first slow night, and the commits and data changes just before it;
- a profile that names the slow step before any change, and one after where it no longer leads;
- a row-for-row diff showing the export unchanged.
Not done: a speedup measured on a small sample.
```

What changed:

- Step 2: the goal names the cause, and the why is left open.
- Step 4: the output, a longer timeout and a hiding cache are off limits.
- Step 5: the logs, the cron config and `git log`, with a hint for finding the entry point.
- Step 6: finding the first slow night and profiling before the fix are proof, not steps.
- Step 7: the row-for-row diff of the export.

Choices left to you: the why (it decides how much time the fix is worth); the old baseline (easy to check, may be too lax) or a target you set (stricter, needs a number).
