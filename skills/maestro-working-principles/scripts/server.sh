#!/usr/bin/env bash
# server.sh: start, list and stop the servers a session runs in its worktree.
#
#   server.sh start -- <command> [args]
#     Runs the command in its own process group, output to a log, and
#     prints pid= and log=. A watcher stops the group once the harness
#     (the parent of the calling shell, or MAESTRO_HARNESS_PID) exits.
#   server.sh list            prints live|dead and each ledger line; drops dead ones
#   server.sh stop <pid|all>  stops a group after checking its start time
#
# The ledger is maestro/servers in this worktree's git dir, one tab-separated
# line per server: pid, group, start time, log, cwd, command. Works on Linux
# and macOS. Exit 2: bad usage; 3: unknown or stale pid; 1: any other failure.
set -eu

die() {
	printf 'server: %s\n' "$2" >&2
	exit "$1"
}

is_pid() {
	[[ $1 =~ ^[1-9][0-9]*$ && $1 != 1 ]]
}

started() {
	LC_ALL=C TZ=UTC0 ps -p "$1" -o lstart= -o pgid= -o stat= 2>/dev/null |
		LC_ALL=C awk -v group="${2-}" '$NF !~ /^Z/ && (group == "" || $(NF-1) == group) {
			sub(/^[[:space:]]+/, "")
			sub(/[[:space:]]+[0-9]+[[:space:]]+[^[:space:]]+[[:space:]]*$/, "")
			print
		}'
}

matches() {
	[[ -n $2 && $(started "$1" "${3-}") == "$2" ]]
}

group_has_children() {
	ps -axo pid=,pgid=,stat= |
		awk -v group="$1" '$2 == group && $1 != group && $3 !~ /^Z/ { found = 1 } END { exit !found }'
}

run_group() {
	local ready=$1 attempt status
	shift
	trap ':' TERM
	for ((attempt = 0; attempt < 100; attempt++)); do
		[[ -e $ready ]] && break
		sleep 0.1
	done
	[[ -e $ready ]] || exit 1
	set +m
	"$@" &
	wait "$!" || :

	set -m
	while :; do
		group_has_children "$$" &
		status=0
		wait "$!" 2>/dev/null || status=$?
		[[ $status != 1 ]] || return 0
		sleep 0.1
	done
}

cleanup() {
	[[ -z ${draft-} ]] || rm -f -- "$draft"
	[[ -z ${locked-} ]] || unlock
	[[ -z ${watch_file-} ]] || rm -f -- "$watch_file"
}

lock() {
	local attempt
	locked=1
	for ((attempt = 0; attempt < 50; attempt++)); do
		if (set -C; printf '%s\n' "$$" >"$lock_file") 2>/dev/null; then
			return
		fi
		sleep 0.1
	done
	return 1
}

unlock() {
	local owner
	if read -r owner 2>/dev/null <"$lock_file" && [[ $owner == "$$" ]]; then
		rm -f -- "$lock_file"
	fi
	locked=
}

new_draft() {
	draft=$(mktemp "$state/servers.XXXXXXXX")
}

save_ledger() {
	if [[ -s $draft ]]; then
		mv -f -- "$draft" "$ledger"
	else
		rm -f -- "$ledger" "$draft"
	fi
	draft=
}

stop_group() {
	local pid=$1 birth=$2 group=$3 attempt
	matches "$pid" "$birth" "$group" || return 3
	kill -TERM -- "-$group" 2>/dev/null || :
	for ((attempt = 0; attempt < 20; attempt++)); do
		matches "$pid" "$birth" "$group" || return 0
		sleep 0.1
	done
	if matches "$pid" "$birth" "$group"; then
		kill -KILL -- "-$group" 2>/dev/null || :
	fi
	for ((attempt = 0; attempt < 20; attempt++)); do
		matches "$pid" "$birth" "$group" || return 0
		sleep 0.1
	done
	return 1
}

watch_server() {
	local harness=$1 harness_birth=$2 log=$3 work_dir=$4 command=$5
	local pid birth cancelled=
	shift 5
	watch_file=$log.watch
	trap 'cancelled=1' TERM INT
	lock || die 1 ledger_busy
	[[ -z $cancelled ]] && matches "$harness" "$harness_birth" || die 1 harness_gone

	set -m
	"$BASH" "$script" _group "$watch_file" "$@" </dev/null >>"$log" 2>&1 &
	pid=$!
	set +m
	birth=$(started "$pid" "$pid")
	[[ -n $birth ]] || die 1 group_start_failed
	new_draft
	if [[ -f $ledger ]]; then
		cat -- "$ledger" >"$draft"
	fi
	printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$pid" "$pid" "$birth" "$log" "$work_dir" "$command" >>"$draft"
	save_ledger
	new_draft
	printf '%s\t%s\n' "$$" "$(started "$$")" >"$draft"
	mv -f -- "$draft" "$watch_file"
	draft=
	unlock
	trap '' PIPE
	printf '%s\n' "$pid" || :
	exec >/dev/null

	while matches "$pid" "$birth" "$pid"; do
		if [[ -n $cancelled ]] || ! matches "$harness" "$harness_birth"; then
			stop_group "$pid" "$birth" "$pid" || [[ $? == 3 ]] || die 1 stop_failed
			break
		fi
		sleep 1
	done
	wait "$pid" 2>/dev/null || :

	lock || die 1 ledger_busy
	if [[ -f $ledger ]]; then
		new_draft
		LOG_PATH=$log awk -F '\t' '$4 != ENVIRON["LOG_PATH"]' "$ledger" >"$draft"
		save_ledger
	fi
	unlock
}

check_ledger() {
	local action=$1 target=${2-} line pid group birth log
	local result found= status=0 ready watcher watcher_birth attempt
	local -a watchers=()
	lock || die 1 ledger_busy
	if [[ -f $ledger ]]; then
		new_draft
		while IFS= read -r line; do
			IFS=$'\t' read -r pid group birth log _ <<<"$line"
			if [[ $action == stop && $target != all && $target != "$pid" ]]; then
				printf '%s\n' "$line" >>"$draft"
				continue
			fi
			found=1
			result=3
			if is_pid "$pid" && [[ $group == "$pid" ]]; then
				if [[ $action == stop ]]; then
					stop_group "$pid" "$birth" "$group" && result=0 || result=$?
				elif matches "$pid" "$birth" "$group"; then
					result=0
				fi
			fi
			case $action:$result in
				list:0) printf 'live\t%s\n' "$line" ;;
				list:3) printf 'dead\t%s\n' "$line" ;;
				stop:0) printf 'stopped=%s\n' "$pid"; watchers+=("$log.watch"); continue ;;
				stop:3) printf 'stale=%s\n' "$pid" >&2; [[ $status != 0 ]] || status=3 ;;
				stop:1) printf 'stop_failed=%s\n' "$pid" >&2; status=1 ;;
			esac
			[[ $result == 3 ]] || printf '%s\n' "$line" >>"$draft"
		done <"$ledger"
		save_ledger
	fi
	unlock

	for ready in ${watchers[@]+"${watchers[@]}"}; do
		IFS=$'\t' read -r watcher watcher_birth 2>/dev/null <"$ready" || continue
		for ((attempt = 0; attempt < 60; attempt++)); do
			matches "$watcher" "$watcher_birth" || break
			sleep 0.1
		done
		if matches "$watcher" "$watcher_birth"; then
			printf 'watcher_busy=%s\n' "$watcher" >&2
			status=1
		fi
	done

	if [[ $action == stop && $target != all && -z $found ]]; then
		die 3 "unknown_pid=$target"
	fi
	return "$status"
}

script=${BASH_SOURCE[0]}
[[ $script == /* ]] || script=$PWD/$script

if [[ ${1-} == _group ]]; then
	shift
	run_group "$@"
	exit
fi

case ${1-} in
	start) [[ $# -ge 3 && $2 == -- && -n $3 ]] || die 2 'usage: server.sh start -- <command> [args]' ;;
	stop) [[ $# == 2 ]] && { [[ $2 == all ]] || is_pid "$2"; } || die 2 'usage: server.sh stop <pid|all>' ;;
	list) [[ $# == 1 ]] || die 2 'usage: server.sh list' ;;
	_watch) ;;
	*) die 2 'usage: server.sh {start -- <command> [args]|stop <pid|all>|list}' ;;
esac

git_dir=$(git rev-parse --absolute-git-dir) || die 1 no_git_dir
state=$git_dir/maestro
ledger=$state/servers
lock_file=$state/servers.lock
draft= locked= watch_file=
trap cleanup EXIT
case $1 in
	start)
		shift 2
		harness=${MAESTRO_HARNESS_PID-}
		if [[ -z $harness ]]; then
			harness=$(ps -p "$PPID" -o ppid= | tr -d '[:space:]')
		fi
		is_pid "$harness" || die 1 invalid_harness
		harness_birth=$(started "$harness")
		[[ -n $harness_birth ]] || die 1 harness_gone
		work_dir=$(pwd -P)
		if [[ $git_dir$work_dir == *[$'\t\n']* ]]; then
			die 1 invalid_path
		fi
		(umask 077; mkdir -p -- "$state")
		log=$(mktemp "$state/server.XXXXXXXX")
		printf -v command '%q ' "$@"
		pid=$(
			set -m
			nohup "$BASH" "$script" _watch "$harness" "$harness_birth" "$log" "$work_dir" "${command% }" "$@" </dev/null 2>>"$log" &
		)
		is_pid "$pid" || die 1 "start_failed log=$log"
		printf 'pid=%s\nlog=%s\n' "$pid" "$log"
		;;
	_watch)
		shift
		watch_server "$@"
		;;
	stop|list)
		if [[ ! -d $state ]]; then
			[[ $1 == list || $2 == all ]] && exit 0
			die 3 "unknown_pid=$2"
		fi
		check_ledger "$@"
		;;
esac
