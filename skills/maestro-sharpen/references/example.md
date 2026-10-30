# One rewrite

Before:

> Look into why the nightly export job is slow and fix it.

The expert: a staff engineer who runs data pipelines. They stay out of the prompt; their standards go in.

After:

```text
Find why the nightly export job got slow, and fix the cause.

Read first: the job's entry point and its cron config [find them: search for the export job's name], the run logs for the last 14 nights, and `git log` on the export code over the same window.

1. Find the first slow night: list run time per night from the logs. List the commits and data changes just before it.
2. Profile one run on production-sized data. Name the step that takes the time before you change anything.
3. Fix that step. If it is a query, compare its `EXPLAIN ANALYZE` output before and after.
4. Diff the export from a fixed run against one from before the fix, row for row.

Done: run time back under [the old baseline | a target you set], the export unchanged, and a new profile shows the step no longer takes the most time. Not done: a longer timeout, a cache that hides the cause, or a speedup measured on a small sample.
```

What changed:

- Step 2: the done line and the three ways it is not done.
- Step 4: the logs, the cron config and `git log`, with a hint for finding the entry point.
- Step 5: pin down the first slow night, and profile before fixing.
- Step 6: the row-for-row diff of the export.

Choices left to you: the old baseline (easy to check, may be too lax) or a target you set (stricter, needs a number).
