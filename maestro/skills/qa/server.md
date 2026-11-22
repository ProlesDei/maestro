# Test a server

Read this for any change a server runs: an endpoint, a worker, a job, a migration.

## Start it

Name what you start with the run prefix in “Worktrees” in the Maestro principles. Add a suffix when resources need distinct names.

- Use a free port no other run holds, and pass it the way the app reads it. If you had to learn how, record it in a `Remember:` line in the verdict.
- Start it so you can stop it and every process it spawns, and log to a folder of your own for this run.
- Use fresh state for this run. Run migrations the way production does.
- Wait until it answers, on its health check if it has one, before you test. Check that your own process holds the port and its log shows it bound there. If another run took the port first, pick a new one.
- If startup fails, quote the log lines that show why.

Start a service the app needs (a database, a queue) the same way, or the way memory says.

## Test it

- Send real requests to the running server for each check. Read the body, not just the status: a 200 with the wrong data fails.
- Try one empty and one wrong input on each changed endpoint. It should refuse them with a clear error, not a 500 or a stack trace.
- Read the server log after each check. An error or warning the check didn't cause goes in the report.
- If the change stores data, restart the server and read the data back. If it changes how data is stored, make the test data on the base branch first, then start the PR on the same data.
- Never call a shared or production service. If a check needs one, block and ask for a test double or a test account.

## Stop it

Stop each server and service you started, with every process it spawned, on every way out: pass, fail, block or error. Stop only what you started; another run's server on this machine is not yours to touch. Then check its port is free. A server that outlives your run takes a port and memory from every run after it.
