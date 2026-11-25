#!/usr/bin/env bash
# prune-lock.sh: give one planner at a time the daily prune run.
# Run inside any worktree: take | done | release, followed by your holder.
# take prints run or the reason it skipped. done stamps success and unlocks.
# release unlocks without a stamp. Use the same holder across calls.
set -euo pipefail

die() { printf 'prune-lock: %s\n' "$*" >&2; exit 1; }
action=${1-}
case "$action" in
	take|done|release) ;;
	*) die 'usage: prune-lock.sh {take|done|release} <holder>' ;;
esac
[ "$#" -eq 2 ] && [ -n "$2" ] || die 'expected one holder'
holder=$2
case "$holder" in
	*[[:space:]]*) die 'holder must have no spaces' ;;
esac

common=$(git rev-parse --git-common-dir)
common=$(cd -- "$common" && pwd -P)
state=$common/maestro
lock=$state/prune.lock
stamp=$state/last-prune
[ ! -L "$state" ] || die 'refusing symlinked maestro directory'
mkdir -p "$state"

now=$(date +%s)
max_age=$((6 * 60 * 60))
max_gap=$((24 * 60 * 60))
utc() { date -u '+%Y-%m-%dT%H:%M:%SZ'; }

age() {
	local mtime
	mtime=$(stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null) || return 1
	printf '%s\n' "$((now - mtime))"
}

fresh() {
	local seconds
	[ ! -L "$stamp" ] && [ -f "$stamp" ] && seconds=$(age "$stamp") && [ "$seconds" -lt "$max_gap" ]
}

expired() {
	local seconds
	seconds=$(age "$1") && [ "$seconds" -gt "$max_age" ]
}

skip_locked() {
	local owner= time=
	if [ ! -L "$lock/owner" ] && [ -f "$lock/owner" ]; then
		read -r owner time 2>/dev/null < "$lock/owner" || true
	fi
	printf 'skip: locked %s %s\n' "${owner:-unknown}" "${time:-unknown}"
}

close_lock() (
	local mode=$1 owner scratch=
	[ ! -L "$lock" ] && cd -- "$lock" 2>/dev/null || return 3
	[ . -ef "$lock" ] || return 3
	if [ "$mode" = stale ]; then
		expired . || return 3
	fi
	# Pin the old directory. Serialize close and stale recovery inside it.
	if [ -f closing ] && expired closing; then
		rm -f closing || return 3
	fi
	(set -C; : 2>/dev/null > closing) || return 3
	trap 'rm -f closing; if [ -n "$scratch" ]; then rm -f "$scratch/stamp"; rmdir "$scratch" 2>/dev/null || :; fi' EXIT
	[ . -ef "$lock" ] || return 3
	if [ "$mode" != stale ]; then
		[ ! -L owner ] && [ -f owner ] && read -r owner _ < owner || die 'cannot read lock holder'
		[ "$owner" = "$holder" ] || die "lock belongs to $owner"
	fi
	scratch=$(mktemp -d "$state/.prune.XXXXXXXX") || die 'cannot make scratch directory'
	if [ "$mode" = done ]; then
		[ ! -L "$stamp" ] || die 'refusing symlinked last-prune stamp'
		[ ! -e "$stamp" ] || [ -f "$stamp" ] || die 'last-prune stamp is not a file'
		utc > "$scratch/stamp" && mv -f "$scratch/stamp" "$stamp" || die 'cannot write last-prune stamp'
	fi
	# Move the old lock out as one step so take can make a new one.
	mv "$lock" "$scratch/lock" || die 'cannot move the lock'
	rm -f owner closing && cd -- "$state" && rmdir "$scratch/lock" "$scratch" || die 'cannot remove the lock'
	trap - EXIT
)

if [ "$action" != take ]; then
	if close_lock "$action"; then
		exit 0
	else
		status=$?
		[ "$status" -ne 3 ] || die 'lock is missing or busy'
		exit "$status"
	fi
fi

if fresh; then
	printf 'skip: fresh %s\n' "$(cat "$stamp")"
	exit
fi

take_lock() {
	local attempt status
	for attempt in 1 2 3; do
		mkdir "$lock" 2>/dev/null && return 0
		[ ! -L "$lock" ] || die 'refusing symlinked lock directory'
		[ -d "$lock" ] || continue
		close_lock stale && continue
		status=$?
		[ "$status" -eq 3 ] || exit "$status"
		[ -d "$lock" ] || continue
		skip_locked
		exit 0
	done
	die 'cannot make the lock directory'
}

time=$(utc)
take_lock
[ ! -L "$lock" ] || die 'refusing symlinked lock directory'
# Some mkdir builds return success when they race and lose. Claim the owner once.
if ! (set -C; printf '%s %s\n' "$holder" "$time" 2>/dev/null > "$lock/owner"); then
	[ -f "$lock/owner" ] || die 'cannot write lock holder'
	skip_locked
	exit
fi
if fresh; then
	close_lock release || die 'cannot release the lock'
	printf 'skip: fresh %s\n' "$(cat "$stamp")"
else
	printf 'run\n'
fi
