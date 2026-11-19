#!/usr/bin/env bash
# qa_env.sh: one QA run's own copy of the app, started and stopped in one
# call each, so a QA session retypes no setup steps and leaves nothing
# behind. A reference: copy it to scripts/qa_env.sh in your repo, fill in
# the three EDIT places, prove up, doctor and down once, and point
# ## QA in .maestro/project/project-setup.md at it.
#
#   qa_env.sh up <ticket>      make the run's folder and data, start the app on a
#                              free port, wait till it answers; prints root=, url=,
#                              pid=, session= and log=
#   qa_env.sh doctor <ticket>  exit 0 while the app's process lives and the url
#                              answers, else 1, with what is wrong
#   qa_env.sh down <ticket>    stop what up started, by its process group, and
#                              remove the folder; nothing up is no error
#
# The folder is $TMPDIR/qa-env-<ticket>-<run>, <run> being $MAESTRO_RUN_ID,
# which each Maestro run has its own of, so QA runs at once share no folder,
# port or browser session, and doctor and down find it from a later shell.
# Under maestro-ticket slot, TMPDIR is the slot's, which goes when the run
# ends, so even a run that never calls down leaves no folder.
set -euo pipefail

cmd=${1:-}
ticket=${2:-}
case $cmd in
up | doctor | down) ;;
*)
	echo "usage: qa_env.sh up|doctor|down <ticket>" >&2
	exit 2
	;;
esac
[[ $ticket =~ ^[A-Za-z]+-[0-9]+$ ]] || {
	echo "qa_env: want a ticket id such as T-12, not '$ticket'" >&2
	exit 2
}
run=${MAESTRO_RUN_ID:-by-hand}
[[ $run =~ ^[A-Za-z0-9-]+$ ]] || {
	echo "qa_env: MAESTRO_RUN_ID holds more than letters, digits and -" >&2
	exit 2
}
tmp=${TMPDIR:-/tmp}
root=${tmp%/}/qa-env-$ticket-$run
state=$root/state
session=qa-$ticket-$run

# EDIT 1: copy what a fresh run needs into "$root/data", such as the
# fixtures, so no run writes to a shared copy. Run from the repo's root.
seed() {
	mkdir -p "$root/data"
	echo "qa_env: $ticket" >"$root/data/index.html"
	# cp -R fixtures/demo/. "$root/data/"
}

# EDIT 2: start the app in the foreground on 127.0.0.1 port $1, with its
# state in "$root/data". up runs it in the background, logs to
# "$root/app.log", and stops it with all it started.
start() {
	exec python3 -m http.server "$1" --bind 127.0.0.1 --directory "$root/data"
	# exec env APP_DATA="$root/data" npm run start -- --port "$1"
}

# EDIT 3: the path that answers once the app is up, and how long to wait.
health_path=/
wait_secs=60

free_port() {
	python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'
}

field() { sed -n "s/^$1=//p" "$state" 2>/dev/null || true; }

answers() { curl -fsS -o /dev/null --max-time 5 "$1$health_path"; }

down() {
	local pid
	pid=$(field pid)
	if [[ $pid =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
		kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
		for _ in $(seq 1 20); do
			kill -0 "$pid" 2>/dev/null || break
			sleep 0.5
		done
		kill -KILL -- "-$pid" 2>/dev/null || true
	fi
	rm -rf -- "$root"
	echo "down: $root removed"
}

case $cmd in
up)
	[ ! -e "$state" ] || {
		echo "qa_env: $ticket is up already in $root: run down first" >&2
		exit 1
	}
	mkdir -p "$root"
	seed
	port=$(free_port)
	url=http://127.0.0.1:$port
	# Job control gives the app a process group of its own, so down stops
	# what it started too; it ignores the hangup of the shell that ends.
	set -m
	(
		trap '' HUP
		start "$port"
	) >"$root/app.log" 2>&1 </dev/null &
	pid=$!
	set +m
	printf 'pid=%s\nport=%s\nurl=%s\n' "$pid" "$port" "$url" >"$state"
	for _ in $(seq 1 $((wait_secs * 2))); do
		answers "$url" 2>/dev/null && break
		kill -0 "$pid" 2>/dev/null || break
		sleep 0.5
	done
	if ! answers "$url" 2>/dev/null; then
		echo "qa_env: the app didn't answer at $url within ${wait_secs}s; the end of its log:" >&2
		tail -n 20 "$root/app.log" >&2 || true
		down >&2
		exit 1
	fi
	printf 'root=%s\nurl=%s\npid=%s\nsession=%s\nlog=%s\n' "$root" "$url" "$pid" "$session" "$root/app.log"
	;;
doctor)
	[ -e "$state" ] || {
		echo "doctor: $ticket is not up: run up" >&2
		exit 1
	}
	pid=$(field pid)
	url=$(field url)
	bad=0
	if kill -0 "$pid" 2>/dev/null; then echo "process: $pid running"; else
		echo "process: $pid gone"
		bad=1
	fi
	if answers "$url" 2>/dev/null; then echo "url: $url answers"; else
		echo "url: $url doesn't answer"
		bad=1
	fi
	exit "$bad"
	;;
down)
	down
	;;
esac
