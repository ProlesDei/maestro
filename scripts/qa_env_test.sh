#!/usr/bin/env bash
# Test for templates/qa_env.sh, the reference QA setup script, as it
# ships: up starts the app on a free port and prints its keys, two runs on
# one ticket share nothing, a second up of one run refuses, doctor reads
# the state from a later shell, down stops the app and what it started and
# removes the folder, a second down is no error, an app that never answers
# leaves nothing, and a bad ticket id is refused.
# Run from anywhere: bash scripts/qa_env_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
env_sh=$here/templates/qa_env.sh
tmp=$(mktemp -d)
pids=()
cleanup() {
	local p
	for p in "${pids[@]}"; do kill -KILL -- "-$p" 2>/dev/null || true; done
	rm -rf "$tmp"
}
trap cleanup EXIT
fail() { cat "$tmp/out" 2>/dev/null || true; echo "FAIL: $*" >&2; exit 1; }
val() { sed -n "s/^$1=//p" "$tmp/out"; }
q() { MAESTRO_RUN_ID=$1 bash "$2" "$3" "$4" >"$tmp/out" 2>&1; }
export TMPDIR=$tmp/t
mkdir -p "$TMPDIR"

q R-aaaaaaaa "$env_sh" up T-12 || fail "up exited $?"
a_pid=$(val pid) a_url=$(val url) a_root=$(val root)
pids+=("$a_pid")
[ "$a_root" = "$TMPDIR/qa-env-T-12-R-aaaaaaaa" ] || fail "root is $a_root"
[ "$(val session)" = qa-T-12-R-aaaaaaaa ] || fail "no session name of the run's own"
[[ $a_url =~ ^http://127\.0\.0\.1:[0-9]+$ ]] || fail "url is $a_url"
curl -fsS -o /dev/null "$a_url/" || fail "the app doesn't answer at $a_url"
[ "$(ps -o pgid= -p "$a_pid" | tr -d ' ')" = "$a_pid" ] || fail "the app has no process group of its own"

q R-bbbbbbbb "$env_sh" up T-12 || fail "a second run's up exited $?"
b_pid=$(val pid)
pids+=("$b_pid")
[ "$(val url)" != "$a_url" ] || fail "two runs share a port"
q R-aaaaaaaa "$env_sh" up T-12 && fail "a second up of one run went through"
grep -q "is up already" "$tmp/out" || fail "a second up didn't say why"

q R-aaaaaaaa "$env_sh" doctor T-12 || fail "doctor exited $?"
grep -q "answers" "$tmp/out" || fail "doctor didn't check the url"

q R-aaaaaaaa "$env_sh" down T-12 || fail "down exited $?"
kill -0 "$a_pid" 2>/dev/null && fail "down left the app running"
[ ! -e "$a_root" ] || fail "down left $a_root"
q R-aaaaaaaa "$env_sh" doctor T-12 && fail "doctor passed with nothing up"
q R-aaaaaaaa "$env_sh" down T-12 || fail "a second down exited $?"
kill -0 "$b_pid" 2>/dev/null || fail "one run's down stopped another's app"
q R-bbbbbbbb "$env_sh" down T-12 || fail "the second run's down exited $?"

# An app that starts a child: down stops the child too.
# shellcheck disable=SC2016 # "$1" is literal text in the template, not this shell's
sed 's|exec python3 -m http.server "$1"|python3 -m http.server "$1"|' "$env_sh" >"$tmp/child.sh"
q R-cccccccc "$tmp/child.sh" up T-7 || fail "up of an app with a child exited $?"
c_pid=$(val pid)
pids+=("$c_pid")
child=$(pgrep -g "$c_pid" -f http.server | grep -vx "$c_pid" | head -n1)
[ -n "$child" ] || fail "no child in the app's group"
q R-cccccccc "$tmp/child.sh" down T-7 || fail "down exited $?"
sleep 0.5
kill -0 "$child" 2>/dev/null && fail "down left the app's child running"

# An app that never answers: up fails and leaves nothing.
sed 's|exec python3 -m http.server .*|exec sleep 300|; s|^wait_secs=60|wait_secs=1|' "$env_sh" >"$tmp/hang.sh"
q R-dddddddd "$tmp/hang.sh" up T-7 && fail "up of an app that never answers went through"
grep -q "didn't answer" "$tmp/out" || fail "up didn't say the app never answered"
[ -z "$(ls -A "$TMPDIR")" ] || fail "something is left in TMPDIR: $(ls -A "$TMPDIR")"

q R-aaaaaaaa "$env_sh" up ../x && fail "a bad ticket id went through"
MAESTRO_RUN_ID='a b' bash "$env_sh" up T-1 >"$tmp/out" 2>&1 && fail "a bad run id went through"
[ -z "$(ls -A "$TMPDIR")" ] || fail "a refused up made something"

echo "ok: qa_env.sh starts a run's own app on a free port, refuses a second up, reads its state from a later shell, and down stops the app and its children and removes the folder; a hung app and bad ids leave nothing"
