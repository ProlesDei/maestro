# Test a server

Read this for any change a server runs: an endpoint, a worker, a job, a migration.

## Start it

- Use a free port no other run holds, and pass it the way the app reads it. Save how to memory.
- Start it so you can stop it and every process it spawns, and log to a folder of your own for this run.
- Point it at state no other run shares: a new database or data folder, and the same for any queue or cache it starts. Run migrations the way production does.
- Wait until it answers, on its health check if it has one, before you test.
- If it won't start, that is a fail. Quote the log lines that show why.

Start a service the app needs (a database, a queue) the same way, or the way memory says.

## Test it

- Send real requests to the running server for each check. Read the body, not just the status: a 200 with the wrong data fails.
- Try one empty and one wrong input on each changed endpoint. It should refuse them with a clear error, not a 500 or a stack trace.
- Read the server log after each check. An error or warning the check didn't cause goes in the report.
- If the change stores data, restart the server and read the data back. If it changes how data is stored, make the data on main first, then start the PR on the same data.
- Never call a shared or production service. If a check needs one, block and ask for a test double or a test account.

## Stop it

Stop each server and service you started, with every process it spawned, on every way out: pass, fail, block or error. Stop only what you started; another run's server on this machine is not yours to touch. Then check its port is free. A server that outlives your run takes a port and memory from every run after it.
