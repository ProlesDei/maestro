#!/usr/bin/env bash
set -euo pipefail
runner=$(realpath -- "${BASH_SOURCE[0]%/*}/../maestro/maestro-run")
scratch=$(mktemp -d -- "$(dirname -- "$runner")/../.maestro-run-test.XXXXXXXX")
scratch=$(realpath -- "$scratch")
export MAESTRO_RUN_ROOT=$scratch/runs
export MAESTRO_RUN_GRACE_MIN=60
export MAESTRO_TEST_TOKEN=$scratch
export T3_FIXTURE_LOG=$scratch/t3-calls
export PATH=$scratch/bin:$PATH
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
export GIT_CEILING_DIRECTORIES=$scratch
unset T3CODE_THREAD_ID MAESTRO_RUN
unset GIT_DIR GIT_COMMON_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

live() {
  local line
  { IFS= read -r line < "/proc/$1/stat"; } 2>/dev/null || return 1
  [[ ${line##*) } != [ZX]\ * ]]
}

finish() {
  local file pid value
  shopt -s nullglob
  for file in "$scratch"/pids/*; do
    read -r pid < "$file" || continue
    while IFS= read -r -d '' value; do
      if [[ $value == "MAESTRO_TEST_TOKEN=$scratch" ]]; then
        kill -KILL "$pid" 2>/dev/null || true
        break
      fi
    done 2>/dev/null < "/proc/$pid/environ" || true
  done
  wait 2>/dev/null || true
  chmod -R u+rwx -- "$scratch"
  rm -rf -- "$scratch"
}
trap finish EXIT
trap 'exit 1' HUP INT TERM

[[ $(stat -fc %T -- "$scratch") != tmpfs ]] || fail 'test fixture is on tmpfs'
mkdir -p -- "$scratch/"{bin,repo,pids,home}
cd -- "$scratch/repo"
git init -q --template=
git -c user.name=Fixture -c user.email=fixture@example.invalid commit --allow-empty -qm fixture
common=$(git rev-parse --absolute-git-dir)
git worktree add -q --detach "$scratch/unrelated"
mv -- "$scratch/unrelated" "$scratch/moved"

upper=ABCDEF00-0000-0000-0000-000000000001
delegated='thread:delegated-task:command%3Amcp%3Aabcdef00-0000-0000-0000-000000000002%3Adelegate-task%3Amy-task'
export DEAD=abcdef00-0000-0000-0000-000000000003
export LIVE=abcdef00-0000-0000-0000-000000000004
export FRESH=abcdef00-0000-0000-0000-000000000005
export GONE=abcdef00-0000-0000-0000-000000000006
export RECENT_FILE=abcdef00-0000-0000-0000-000000000007
export SHARED=abcdef00-0000-0000-0000-000000000008
export T3_FIXTURE_MODE=ok

cat > "$scratch/bin/t3" <<'T3'
#!/usr/bin/env bash
[[ $# == 6 && $1 == thread && $2 == show && $4 == --json && $5 == --messages && $6 == 0 ]] || exit 2
printf '%s\n' "$3" >> "$T3_FIXTURE_LOG"
case $T3_FIXTURE_MODE in
  down) echo 'Server no longer available' >&2; exit 1 ;;
  partial)
    if [[ $3 == "$LIVE" ]]; then echo 'Connection refused' >&2; exit 1; fi
    ;;
esac
case $3 in
  "$GONE") echo "Thread $3 is no longer available." >&2; exit 1 ;;
  "$DEAD"|"$FRESH"|"$RECENT_FILE"|"$SHARED") printf '{"thread":{"threadId":"%s","archived":true}}\n' "$3" ;;
  *) printf '{"thread":{"threadId":"%s","status":"waiting","activeRunId":null,"pendingRequestCount":1}}\n' "$3" ;;
esac
T3
chmod +x -- "$scratch/bin/t3"

cat > "$scratch/worker" <<'WORKER'
#!/usr/bin/env bash
if [[ ${IGNORE_TERM-} == yes ]]; then trap '' TERM; fi
printf '%s\n' "$$" > "$1"
shift
if (( $# )); then exec -a "$1" sleep 300; fi
exec sleep 300
WORKER
cat > "$scratch/server" <<'SERVER'
#!/usr/bin/env bash
setsid bash "$1/worker" "$1/pids/daemon" &
bash "$1/worker" "$1/pids/group" &
printf '%s\n' "$$" > "$1/pids/server"
printf '%s\n' "$PWD" "$TMPDIR" "$MAESTRO_RUN"
exec sleep 300
SERVER
cat > "$scratch/agent" <<'AGENT'
#!/usr/bin/env bash
set -eu
runner=$1 scratch=$2
run=$("$runner" make qa crash)
printf '%s\n' "$run" > "$scratch/run"
"$runner" start server -- bash "$scratch/server" "$scratch" > "$scratch/start"
TMPDIR=$run/tmp bash "$scratch/worker" "$scratch/pids/build" &
(cd -- "$run"; exec bash "$scratch/worker" "$scratch/pids/cwd") &
bash "$scratch/worker" "$scratch/pids/cmdline" "--dir=$run/build" &
bash "$scratch/worker" "$scratch/pids/colon" "dir:$run" &
bash "$scratch/worker" "$scratch/pids/space" "dir $run/build" &
bash "$scratch/worker" "$scratch/pids/prefix" "/other/prefix$run/file" &
bash "$scratch/worker" "$scratch/pids/suffix" "$run:other" &
wait
AGENT

worker_pid() {
  local file=$scratch/pids/$1 pid comm i
  for ((i = 0; i < 500; i++)); do
    if { read -r pid < "$file" && read -r comm < "/proc/$pid/comm"; } 2>/dev/null &&
      [[ $comm == sleep ]]; then
      printf '%s\n' "$pid"
      return
    fi
    sleep 0.01
  done
  fail "worker $1 did not start"
}

identity() {
  local line
  IFS= read -r line < "/proc/$1/stat"
  printf '%s\n' "${line##*) }"
}

crash_run() {
  local id=$1 expected=$2 agent run service daemon group kind pid start log pgrp sid
  T3CODE_THREAD_ID=$id bash "$scratch/agent" "$runner" "$scratch" &
  agent=$!
  printf '%s\n' "$agent" > "$scratch/pids/agent"
  for kind in server daemon group build cwd cmdline colon space prefix suffix; do
    worker_pid "$kind" > /dev/null
  done
  read -r run < "$scratch/run"
  read -r start log < "$scratch/start"
  service=$(< "$scratch/pids/server")
  daemon=$(< "$scratch/pids/daemon")
  group=$(< "$scratch/pids/group")
  [[ $run == "$MAESTRO_RUN_ROOT/maestro-qa-crash-$expected" ]] || fail 'thread key'
  [[ $start == "$service" && $log == "$run/logs/server.log" ]] || fail 'start output'
  [[ $(< "$log") == "$PWD"$'\n'"$run/tmp"$'\n'"$run" ]] || fail 'service cwd or environment'
  [[ $(< "$run/thread") == "$id" && $(< "$run/git-common-dir") == "$common" ]] || fail 'run metadata'
  [[ $(stat -c %a -- "$run") == 700 ]] || fail 'run mode'
  read -r _ _ pgrp sid _ <<< "$(identity "$group")"
  [[ $pgrp == "$service" && $sid == "$service" ]] || fail 'service process group'
  read -r _ _ pgrp sid _ <<< "$(identity "$daemon")"
  [[ $pgrp == "$daemon" && $sid == "$daemon" ]] || fail 'daemon did not escape service group'
  git worktree add -q --detach "$run/tmp/checkout"
  mkdir -p -- "$run/tmp/readonly"
  touch -- "$run/tmp/readonly/file"
  chmod u-w -- "$run/tmp/readonly/"{file,}
  kill -KILL "$agent"
  wait "$agent" 2>/dev/null || true
  for kind in server daemon group build cwd cmdline colon space prefix suffix; do
    live "$(< "$scratch/pids/$kind")" || fail "$kind did not survive the agent crash"
  done
  (
    cd -- "$run/tmp/checkout"
    "$runner" clean "$id"
    [[ ! -e $run ]] || fail 'clean from inside the run left the folder'
  )
  "$runner" clean "$id"
  [[ ! -e $run ]] || fail 'run folder survived clean'
  [[ $(git worktree list --porcelain) != *"$run/tmp/checkout"* ]] || fail 'Git worktree entry survived clean'
  for kind in server daemon group build cwd cmdline colon space suffix; do
    pid=$(< "$scratch/pids/$kind")
    if live "$pid"; then fail "$kind PID $pid survived clean"; fi
  done
  pid=$(< "$scratch/pids/prefix")
  live "$pid" || fail 'clean matched a partial path'
  kill -KILL "$pid"
  rm -f -- "$scratch/pids/"*
  printf 'ok - crashed agent cleaned (%s)\n' "$id"
}

crash_run "$upper" "${upper,,}"
digest=$(printf '%s' "$delegated" | sha256sum)
crash_run "$delegated" "${digest:0:8}-${digest:8:4}-${digest:12:4}-${digest:16:4}-${digest:20:12}"
[[ $(< "$common/worktrees/unrelated/gitdir") == "$scratch/unrelated/.git" ]] ||
  fail 'clean removed an unrelated moved worktree registration'
printf 'ok - clean matches whole paths and preserves an unrelated worktree registration\n'

export T3CODE_THREAD_ID=$LIVE
run=$("$runner" make qa restart)
[[ $("$runner" make qa restart) == "$run" ]] || fail 'make is not idempotent'
[[ $("$runner" make qa new-label) == "$run" ]] || fail 'make created a second run for the same thread'
"$runner" start service -- bash "$scratch/worker" "$scratch/pids/restart" > /dev/null
pid=$(worker_pid restart)
"$runner" stop service
if live "$pid"; then fail 'stop left the service running'; fi
rm -- "$scratch/pids/restart"
IGNORE_TERM=yes "$runner" start service -- bash "$scratch/worker" "$scratch/pids/restart" > /dev/null
pid=$(worker_pid restart)
"$runner" clean
if live "$pid"; then fail 'clean left the restarted service running'; fi
rm -- "$scratch/pids/restart"
unset T3CODE_THREAD_ID
printf 'ok - stop, restart, and force-kill a service that ignores TERM\n'

dead_run=$(T3CODE_THREAD_ID=$DEAD "$runner" make qa a-dead)
live_run=$(T3CODE_THREAD_ID=$LIVE "$runner" make qa b-live)
fresh_run=$(T3_FIXTURE_MODE=down T3CODE_THREAD_ID=$FRESH "$runner" make qa c-fresh 2>&1)
[[ -d $fresh_run ]] || fail 'make did not return just the run path during T3 outage'
gone_run=$(T3CODE_THREAD_ID=$GONE "$runner" make qa d-gone)
recent_run=$(T3CODE_THREAD_ID=$RECENT_FILE "$runner" make qa e-recent)
touch -- "$recent_run/tmp/build"
touch -d '2 hours ago' -- "$dead_run" "$live_run" "$gone_run" "$recent_run" "$recent_run/tmp" \
  "$dead_run/"* "$live_run/"* "$gone_run/"* "$recent_run/"*
T3CODE_THREAD_ID=$DEAD bash "$scratch/worker" "$scratch/pids/shared-dead" &
T3CODE_THREAD_ID=$SHARED bash "$scratch/worker" "$scratch/pids/shared-no-run" &
T3CODE_THREAD_ID=$DEAD TMPDIR=$dead_run/tmp bash "$scratch/worker" "$scratch/pids/stale-worker" &
shared_dead=$(worker_pid shared-dead)
shared_no_run=$(worker_pid shared-no-run)
stale_worker=$(worker_pid stale-worker)
for mode in down partial; do
  output=$(T3_FIXTURE_MODE=$mode "$runner" prune 2>&1)
  [[ $output == 'Maestro: T3 unavailable, keeping runs' ]] || fail "unexpected prune output: $output"
  [[ -d $dead_run && -d $live_run && -d $fresh_run && -d $gone_run && -d $recent_run ]] ||
    fail 'prune removed a run during T3 outage'
  live "$stale_worker" || fail 'prune killed a worker during T3 outage'
done
output=$("$runner" prune)
[[ ! -e $dead_run && ! -e $gone_run && -d $live_run && -d $fresh_run && -d $recent_run ]] ||
  fail 'prune did not follow liveness and grace period'
[[ $output == *"Untagged PID $shared_dead for dead thread $DEAD"* &&
   $output == *"Untagged PID $shared_no_run for dead thread $SHARED"* ]] || fail 'missing untagged report'
if ! live "$shared_dead" || ! live "$shared_no_run"; then fail 'prune killed a shared process'; fi
if live "$stale_worker"; then fail 'prune left a stale worker running'; fi
printf '%s\n' "$output"
printf 'ok - prune preserves live and recent runs, shared processes, and all runs during T3 outages\n'

cat > "$scratch/spawner" <<'SPAWNER'
#!/usr/bin/env bash
trap 'bash "$1/worker" "$1/pids/late-worker" & echo "$!" > "$1/pids/late"; exit 0' TERM
sleep 300 & wait
SPAWNER
late_run=$(T3CODE_THREAD_ID=$DEAD "$runner" make qa late)
(cd -- "$scratch/repo" && T3CODE_THREAD_ID=$DEAD "$runner" start spawner -- bash "$scratch/spawner" "$scratch" > /dev/null)
sleep 0.5
find "$late_run" -exec touch -h -d '2 hours ago' -- {} +
"$runner" prune > /dev/null
[[ -s $scratch/pids/late ]] || fail 'spawner did not get TERM'
late=$(< "$scratch/pids/late")
[[ ! -e $late_run ]] || fail 'prune left the run'
if live "$late"; then fail 'prune left a child spawned during shutdown'; fi
rm -f -- "$scratch/pids/late" "$scratch/pids/late-worker"
printf 'ok - prune stops a child spawned during shutdown\n'

printf '0\n' > "$MAESTRO_RUN_ROOT/.maestro-prune"
: > "$T3_FIXTURE_LOG"
exec {lock}>> "$MAESTRO_RUN_ROOT/.maestro-prune"
flock "$lock"
T3CODE_THREAD_ID=$FRESH timeout 5s "$runner" make qa c-fresh > /dev/null
exec {lock}>&-
[[ ! -s $T3_FIXTURE_LOG ]] || fail 'make pruned while another sweep held the lock'
T3CODE_THREAD_ID=$FRESH "$runner" make qa c-fresh > /dev/null
[[ $(< "$T3_FIXTURE_LOG") == "$LIVE" ]] || fail 'auto-prune queried a recent run or untagged process'
: > "$T3_FIXTURE_LOG"
T3CODE_THREAD_ID=$FRESH "$runner" make qa c-fresh > /dev/null
[[ ! -s $T3_FIXTURE_LOG ]] || fail 'make called T3 again within the prune cooldown'
printf 'ok - make skips a held prune lock and checks T3 at most once per cooldown\n'

bind_run=$(T3CODE_THREAD_ID=$SHARED "$runner" make qa bind)
mkdir -- "$scratch/source" "$bind_run/bind mount"
printf 'keep\n' > "$scratch/source/file"
if unshare -rm -- true 2>/dev/null; then
  bind_status=0
  unshare -rm -- bash -s -- "$runner" "$scratch" "$bind_run" <<'BIND' || bind_status=$?
set -eu
runner=$1 scratch=$2 run=$3
mount --bind "$scratch/source" "$run/bind mount" 2>/dev/null || exit 77
if "$runner" clean "$SHARED"; then exit 1; fi
[[ $(< "$scratch/source/file") == keep && $(< "$run/thread") == "$SHARED" &&
   -s $run/git-common-dir ]]
umount -- "$run/bind mount"
"$runner" clean "$SHARED"
[[ ! -e $run && $(< "$scratch/source/file") == keep ]]
BIND
  case $bind_status in
    0) printf 'ok - clean preserves same-device bind mount data and can retry after unmount\n' ;;
    77) printf 'skip - unprivileged bind mount unavailable\n' ;;
    *) fail 'clean did not preserve the bind mount source or allow a retry' ;;
  esac
else
  printf 'skip - unprivileged mount namespace unavailable\n'
fi

default_run=$(env -u MAESTRO_RUN_ROOT HOME="$scratch/home" T3CODE_THREAD_ID="$LIVE" "$runner" make qa default)
[[ $default_run == "$scratch/home/.local/share/maestro-runs/"* ]] || fail 'default run root path'
[[ $(stat -c %d -- "$default_run") == "$(stat -c %d -- "$PWD")" &&
   $(stat -fc %T -- "$default_run") != tmpfs ]] || fail 'default root is not on the checkout filesystem'
mkdir -- "$default_run/browser"
(
  export HOME=$scratch/home T3CODE_THREAD_ID=$LIVE
  unset MAESTRO_RUN_ROOT
  cd -- "$default_run/browser"
  if git rev-parse --show-toplevel >/dev/null 2>&1; then fail 'browser fixture is in a Git checkout'; fi
  "$runner" start browser -- bash "$scratch/worker" "$scratch/pids/browser" > /dev/null
  pid=$(worker_pid browser)
  "$runner" stop browser
  if live "$pid"; then fail 'stop from a browser folder left the process running'; fi
  "$runner" prune > /dev/null
  "$runner" clean
  [[ ! -e $default_run ]] || fail 'clean from a browser folder left the run'
)
printf 'ok - start, stop, prune, and clean work outside a Git checkout\n'
if [[ -d /dev/shm && $(stat -c %d -- /dev/shm) != "$(stat -c %d -- "$PWD")" ]]; then
  candidate=/dev/shm/${scratch##*/}
  ln -s -- "$candidate" "$common/maestro-runs"
  if output=$(MAESTRO_RUN_ROOT=$candidate T3CODE_THREAD_ID=$LIVE "$runner" make qa fallback 2>&1); then
    fail 'make accepted a fallback on tmpfs'
  fi
  [[ $output == 'Maestro: Run root is on tmpfs: '* && ! -e $candidate ]] || fail 'tmpfs fallback error'
  rm -- "$common/maestro-runs"
  ln -s -- /proc/sys "$common/maestro-runs"
  if output=$(MAESTRO_RUN_ROOT=$candidate T3CODE_THREAD_ID=$LIVE "$runner" make qa fallback 2>&1); then
    fail 'make accepted a fallback on a different device'
  fi
  [[ $output == 'Maestro: Run root is on a different device from the checkout: '* ]] ||
    fail 'cross-device fallback error'
  rm -- "$common/maestro-runs"
  fallback=$(MAESTRO_RUN_ROOT=$candidate T3CODE_THREAD_ID=$LIVE "$runner" make qa fallback)
  [[ $fallback == "$common/maestro-runs/"* && ! -e $candidate ]] || fail 'cross-filesystem fallback'
  MAESTRO_RUN_ROOT=$candidate "$runner" clean "$LIVE"
  [[ ! -e $fallback ]] || fail 'clean did not find the fallback from its Git checkout'
fi
printf 'ok - default root and cross-filesystem fallback\n'

session_run=$(T3CODE_THREAD_ID=$LIVE "$runner" make qa session)
cat > "$scratch/regroup" <<'REGROUP'
#!/usr/bin/env bash
set -m
env -i MAESTRO_TEST_TOKEN="$MAESTRO_TEST_TOKEN" bash "$1/worker" "$1/pids/regroup" &
wait
REGROUP
T3CODE_THREAD_ID=$LIVE "$runner" start regroup -- bash "$scratch/regroup" "$scratch" > /dev/null
regroup=$(worker_pid regroup)
read -r _ _ pgrp sid _ <<< "$(identity "$regroup")"
[[ $pgrp != "$sid" ]] || fail 'regroup worker kept its process group'
other_run=$(T3CODE_THREAD_ID=$SHARED "$runner" make qa other)
(cd -- "$session_run" && cd -- "$other_run" && exec env T3CODE_THREAD_ID=$SHARED bash "$scratch/worker" "$scratch/pids/oldpwd") &
oldpwd=$(worker_pid oldpwd)
(cd -- "$scratch" && exec env -u TMPDIR PWD="$session_run" sleep 300) &
pwd_worker=$!
printf '%s\n' "$pwd_worker" > "$scratch/pids/pwd"
cat > "$scratch/foreign" <<'FOREIGN'
#!/usr/bin/env bash
bash "$1/worker" "$1/pids/foreign" &
sleep 0.2
FOREIGN
setsid bash "$scratch/foreign" "$scratch" &
foreign=$(worker_pid foreign)
read -r _ _ _ foreign_sid _ <<< "$(identity "$foreign")"
while kill -0 "$foreign_sid" 2>/dev/null; do sleep 0.05; done
mkdir -- "$session_run/services/stale"
printf '%s %s 1\n' "$foreign_sid" "$foreign_sid" > "$session_run/services/stale/pid"
T3CODE_THREAD_ID=$LIVE "$runner" clean
if live "$pwd_worker"; then fail 'clean left a worker started in the run'; fi
live "$foreign" || fail 'clean killed a session whose recorded leader is gone'
kill -KILL "$foreign"
[[ ! -e $session_run ]] || fail 'clean left the session run'
if live "$regroup"; then fail 'clean left a service child in its own process group'; fi
live "$oldpwd" || fail 'clean killed another run through OLDPWD'
T3CODE_THREAD_ID=$SHARED "$runner" clean > /dev/null
rm -f -- "$scratch/pids/"{regroup,oldpwd,pwd,foreign}
printf 'ok - clean follows live service sessions, matches PWD and ignores OLDPWD\n'
