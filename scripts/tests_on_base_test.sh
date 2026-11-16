#!/usr/bin/env bash
# Test for skills/maestro-code-review/scripts/tests-on-base.sh, against a
# local origin, a main checkout that holds the setup file and a linked
# worktree for the change. Covers a real test (red at the base) and a
# tautological one (green); {} runs per test file, from a subfolder too;
# the tree in between, base code under head tests, with the untracked
# dependencies still there; a change that adds, deletes and edits files,
# one in a folder with a space; --base, and the merge base once main has
# moved; a bare main repo; no changed test file; a red head; a dirty
# tree; a command that can't start; a file turned folder, refused; and
# the tree, index, HEAD and branch the same after every run, also when
# the command dies, the script is stopped mid-run, or it is killed and
# the command its log names puts the tree back.
# Run from anywhere: bash scripts/tests_on_base_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
tob=$here/skills/maestro-code-review/scripts/tests-on-base.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() {
	cat "$tmp/out" "$tmp/err" 2>/dev/null || true
	echo "FAIL: $*" >&2
	exit 1
}
has() { grep -qxF -- "$1" "$tmp/out" || fail "want line '$1'"; }
hasnt() { ! grep -q -- "$1" "$tmp/out" || fail "want no line with '$1'"; }
val() { sed -n "s/^$1=//p" "$tmp/out"; }

export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export TMPDIR=$tmp/t
mkdir -p "$TMPDIR"

# origin, and a main checkout with the setup file. calc.sh only adds.
git init -q --bare -b main "$tmp/origin.git"
git clone -q "$tmp/origin.git" "$tmp/work" 2>/dev/null
cd "$tmp/work"
mkdir -p .maestro/project src tests
printf '# Project setup\n\n## Main branch\n\nmain\n\n## Checks\n' >.maestro/project/project-setup.md
echo 'deps/' >.gitignore
# shellcheck disable=SC2016 # the scripts' own $1 and $2
echo 'echo $(($1 + $2))' >src/calc.sh
echo 'echo old' >src/old.sh
# shellcheck disable=SC2016
echo 'cd "$(dirname "$0")/.." && [ "$(sh src/old.sh)" = old ]' >tests/gone_test.sh
git add -A && git commit -qm base && git push -q origin main
F=$(git rev-parse HEAD)

# The change, in a worktree of its own, with untracked dependencies.
wt=$tmp/wt
git worktree add -q -b maestro/T-1 "$wt" main
cd "$wt"
mkdir deps && echo 1 >deps/installed
# K1: calc.sh multiplies and turns executable, a helper comes in a new
# folder with a space, old.sh and its test go, and a test that passes
# with or without all that.
# shellcheck disable=SC2016
echo 'case ${3-add} in add) echo $(($1 + $2)) ;; mul) echo $(($1 * $2)) ;; esac' >src/calc.sh
chmod +x src/calc.sh
mkdir "src/new dir" && echo 'echo helped' >"src/new dir/helper.sh"
git rm -q src/old.sh tests/gone_test.sh
mkdir -p tests # git rm took the folder with its last file
# shellcheck disable=SC2016
echo 'cd "$(dirname "$0")/.." && [ "$(sh src/calc.sh 2 3)" = 5 ]' >tests/taut_test.sh
git add -A && git commit -qm code
K1=$(git rev-parse HEAD)
# K2: the tests that need the change.
mkdir "tests/sp ace"
# shellcheck disable=SC2016
echo 'cd "$(dirname "$0")/.." && echo NOISE-FROM-THE-TEST && [ "$(sh src/calc.sh 2 3 mul)" = 6 ]' >tests/mul_test.sh
# shellcheck disable=SC2016
echo 'cd "$(dirname "$0")/../.." && [ "$(sh "src/new dir/helper.sh")" = helped ]' >"tests/sp ace/helper_test.sh"
git add -A && git commit -qm tests
K2=$(git rev-parse HEAD)
# K3: one more file with its test. H: code only.
echo 'echo extra' >src/extra.sh
# shellcheck disable=SC2016
echo 'cd "$(dirname "$0")/.." && [ "$(sh src/extra.sh)" = extra ]' >tests/extra_test.sh
git add -A && git commit -qm extra
K3=$(git rev-parse HEAD)
echo '# a comment' >>src/extra.sh
git commit -qam tweak
H=$(git rev-parse HEAD)

# snap: every file, folder, link, exec bit and byte of the worktree, and
# its HEAD, branch, index and status.
snap() {
	(
		cd "$wt"
		git rev-parse HEAD
		git symbolic-ref -q HEAD || echo detached
		git write-tree
		git status --porcelain
		find . -name .git -prune -o -print | LC_ALL=C sort | while IFS= read -r f; do
			if [ -L "$f" ]; then
				echo "$f -> $(readlink "$f")"
			elif [ -f "$f" ]; then
				echo "$f $([ -x "$f" ] && echo x) $(cksum <"$f")"
			else
				echo "$f/"
			fi
		done
	)
}
same() { # same <what>: the worktree is as $tmp/before has it
	snap >"$tmp/after"
	cmp -s "$tmp/before" "$tmp/after" || { diff "$tmp/before" "$tmp/after" || true; fail "the tree changed: $1"; }
}
# t <want exit> <dir> <args...>: runs it in <dir>; the tree must come out
# as it went in, and stdout must hold key=value lines only.
t() {
	local want=$1 dir=$2 code=0
	shift 2
	snap >"$tmp/before"
	(cd "$dir" && bash "$tob" "$@") >"$tmp/out" 2>"$tmp/err" || code=$?
	[ "$code" -eq "$want" ] || fail "exit $code, want $want: $*"
	same "$*"
	! grep -vqE '^[a-z_]+=' "$tmp/out" || fail "stdout has a line that is no key=value: $*"
}
snap >"$tmp/golden"

# A real test: red at the base, exit 0. Its output goes to the log only.
t 0 "$wt" -- bash tests/mul_test.sh
for l in "base_sha=$F" "head_sha=$H" tests=4 runs=1 base=red; do has "$l"; done
hasnt '^test='
hasnt NOISE
[ "$(grep -c NOISE-FROM-THE-TEST "$(val log)")" -eq 2 ] || fail "the log lacks the two runs' output"
case $(val log) in "$TMPDIR"/*/log) ;; *) fail "log at $(val log)" ;; esac

# A tautological test: green at the base, exit 1.
t 1 "$wt" -- bash tests/taut_test.sh
for l in runs=1 green=1 base=green; do has "$l"; done

# {}: one run per changed test file the head has, never the deleted one.
t 1 "$wt" -- bash {}
for l in tests=4 "test=tests/mul_test.sh red" "test=tests/taut_test.sh green" "test=tests/sp ace/helper_test.sh red" \
	"test=tests/extra_test.sh red" runs=4 green=1 base=green; do has "$l"; done
hasnt gone_test
# From a subfolder {} is the path from there; test= stays from the top.
t 1 src -- bash {}
has "test=tests/sp ace/helper_test.sh red"
has green=1
grep -qF '== base: bash ../tests/mul_test.sh' "$(val log)" || fail "{} from src/ is not ../tests/mul_test.sh"
t 1 "tests/sp ace" -- bash {}
grep -qF '== base: bash helper_test.sh' "$(val log)" || fail "{} from the test's own folder is not its name"

# --base: only what changed since it. K2..H has one test, red without
# extra.sh; K3..H has code only.
t 0 "$wt" --base "$K2" -- bash {}
for l in "base_sha=$K2" tests=1 "test=tests/extra_test.sh red" runs=1 base=red; do has "$l"; done
t 1 "$wt" --base "$K1" --skip-head -- bash {}
for l in tests=3 green=2 base=green; do has "$l"; done
t 3 "$wt" --base "$K3" -- bash {}
for l in tests=0 base=no-tests; do has "$l"; done
hasnt '^log='
[ -z "$(ls "$TMPDIR")" ] && fail "no run wrote a log"
before=$(find "$TMPDIR" -mindepth 1 -maxdepth 1 | wc -l)
t 3 "$wt" --base "$H" -- bash {}
[ "$(find "$TMPDIR" -mindepth 1 -maxdepth 1 | wc -l)" -eq "$before" ] || fail "a run with no test wrote to the temp dir"

# The tree in between: base code under head tests, in place, so the
# untracked dependencies are still there. A deleted file is back and
# tracked; an added one is gone with its folder.
cp "$tmp/work/src/calc.sh" "$tmp/calc.base"
cat >"$tmp/probe.sh" <<EOF
cd "$wt"
{
	for f in src/old.sh "src/new dir" src/extra.sh tests/mul_test.sh tests/gone_test.sh deps/installed; do
		if [ -e "\$f" ]; then echo "have \$f"; else echo "lack \$f"; fi
	done
	[ -x src/calc.sh ] || echo calc=plain
	cmp -s src/calc.sh "$tmp/calc.base" && echo calc=base
	git ls-files --error-unmatch src/old.sh
	git ls-files --others --exclude-standard
} >"$tmp/probe" 2>&1
exit 1
EOF
t 0 "$wt" --skip-head -- bash "$tmp/probe.sh"
has base=red
printf '%s\n' 'have src/old.sh' 'lack src/new dir' 'lack src/extra.sh' 'have tests/mul_test.sh' \
	'lack tests/gone_test.sh' 'have deps/installed' calc=plain calc=base src/old.sh >"$tmp/probe.want"
cmp -s "$tmp/probe" "$tmp/probe.want" || { cat "$tmp/probe"; fail "the tree at the base"; }

# A red head stops it before anything moves; --skip-head skips that run.
t 4 "$wt" -- false
has head=red
hasnt '^base='
t 4 "$wt" -- bash -c 'exit 1' _ {}
grep -q 'red at the head: tests/extra_test.sh' "$tmp/err" || fail "a red head names no file"
# A command killed mid-run is a red; one that can't start is no verdict.
# shellcheck disable=SC2016 # $$ is the command's own
t 0 "$wt" --skip-head -- bash -c 'kill -KILL $$'
has base=red
t 1 "$wt" --skip-head -- no-such-command-here
hasnt '^base='
grep -q 'could not start' "$tmp/err" || fail "a missing command reads as a red"

# A dirty tree is refused, untracked or edited, and left as it is.
echo x >stray.txt
t 2 "$wt" -- bash tests/mul_test.sh
grep -q 'dirty' "$tmp/err" || fail "a dirty tree: no why"
rm stray.txt
echo x >>src/calc.sh
t 2 "$wt" -- bash tests/mul_test.sh
git checkout -q src/calc.sh
# Usage: no --, no command, a --base that is no commit.
t 2 "$wt" bash tests/mul_test.sh
t 2 "$wt" --skip-head --
t 2 "$wt" --base nope -- bash tests/mul_test.sh

# The default base is the merge base, not main's tip, once main moved on.
(cd "$tmp/work" && echo more >more.txt && git add -A && git commit -qm more && git push -q origin main)
git fetch -q origin
t 0 "$wt" -- bash tests/mul_test.sh
has "base_sha=$F"
# With no setup file there is no main to find, and --base still works.
mv "$tmp/work/.maestro/project/project-setup.md" "$tmp/setup"
t 1 "$wt" -- bash tests/mul_test.sh
grep -q 'give --base' "$tmp/err" || fail "no setup file: no hint"
t 0 "$wt" --base "$F" --skip-head -- bash tests/mul_test.sh
mv "$tmp/setup" "$tmp/work/.maestro/project/project-setup.md"
snap >"$tmp/after"
cmp -s "$tmp/golden" "$tmp/after" || fail "the tree is not as it was before the first run"

# Stopped mid-run: the command dies with its children and the tree is back.
cat >"$tmp/slow.sh" <<EOF
echo \$\$ >"$tmp/slow.pid"
sleep 300
EOF
start() { # start: a run that hangs at the base; its pid in $pid
	rm -f "$tmp/slow.pid"
	(cd "$wt" && exec bash "$tob" --skip-head -- bash "$tmp/slow.sh") >"$tmp/out" 2>"$tmp/err" &
	pid=$!
	for _ in $(seq 1 100); do [ -s "$tmp/slow.pid" ] && break; sleep 0.1; done
	[ -s "$tmp/slow.pid" ] || fail "the slow command never started"
	[ -e src/old.sh ] && [ ! -e "src/new dir" ] || fail "the slow command is not running at the base"
}
gone() { # gone <pid>: no such process, a few seconds at most from now
	for _ in $(seq 1 30); do kill -0 "$1" 2>/dev/null || return 0; sleep 0.1; done
	return 1
}
snap >"$tmp/before"
for s in TERM HUP; do
	start
	kill "-$s" "$pid"
	code=0; wait "$pid" || code=$?
	[ $code -eq 1 ] || fail "stopped by $s: exit $code, want 1"
	same "stopped by $s"
	hasnt '^base='
	grep -q "stopped by $s; the tree is at its head" "$tmp/err" || fail "stopped by $s: not said"
	gone "$(cat "$tmp/slow.pid")" || fail "stopped by $s: the test command lives on"
done

# Killed outright, nothing can run: the tree stays at the base, the next
# run refuses it, and the command at the top of the log puts it back.
start
kill -KILL "$pid"
wait "$pid" 2>/dev/null || true
kill -KILL "$(cat "$tmp/slow.pid")"
[ -n "$(git status --porcelain)" ] || fail "a killed run left a clean tree: nothing to restore"
code=0; bash "$tob" -- bash tests/mul_test.sh >/dev/null 2>"$tmp/err" || code=$?
[ $code -eq 2 ] || fail "after a killed run: exit $code, want 2"
grep -q 'log= file' "$tmp/err" || fail "after a killed run: no pointer to the log"
fix=$(sed -n 's/^#   \(git -C .*\)$/\1/p' "$(val log)")
[ -n "$fix" ] || fail "the log names no restore command"
eval "$fix"
same "the log's restore command after a kill"

# A command that leaves a file behind: no verdict, and a loud failure
# that names the restore command; the tree is back but for that file.
code=0; bash "$tob" --skip-head -- bash -c 'echo junk >junk.txt; exit 1' >"$tmp/out" 2>"$tmp/err" || code=$?
[ $code -eq 1 ] || fail "junk left: exit $code, want 1"
hasnt '^base='
grep -q 'NOT BACK AT ITS HEAD' "$tmp/err" || fail "junk left: not loud"
grep -q 'restore --source=' "$tmp/err" || fail "junk left: no restore command"
grep -q '?? junk.txt' "$tmp/err" || fail "junk left: not named"
rm junk.txt
same "junk left"
# The same at the head stops it before anything moves.
code=0; bash "$tob" -- bash -c 'echo junk >junk.txt' >"$tmp/out" 2>"$tmp/err" || code=$?
[ $code -eq 1 ] || fail "junk at the head: exit $code, want 1"
grep -q 'leaves the tree dirty (junk.txt)' "$tmp/err" || fail "junk at the head: no why"
rm junk.txt
same "junk at the head"

# A bare main repo: the main branch comes from the linked worktree that
# holds the install, not from the first one listed.
git clone -q --bare "$tmp/origin.git" "$tmp/bare.git"
git -C "$tmp/bare.git" config remote.origin.fetch '+refs/heads/*:refs/remotes/origin/*'
git -C "$tmp/bare.git" fetch -q origin
git -C "$tmp/bare.git" worktree add -q "$tmp/bare-other" -b other origin/main
git -C "$tmp/bare.git" worktree add -q "$tmp/bare-main" main
mkdir -p "$tmp/bare-main/.maestro/src"
git -C "$tmp/bare.git" worktree add -q "$tmp/bare-wt" -b change "$F"
(
	cd "$tmp/bare-other"
	printf '## Main branch\n\nnope\n' >.maestro/project/project-setup.md
	git commit -qam nope
)
wt=$tmp/bare-wt
cd "$wt"
echo 'echo two' >src/two.sh
# shellcheck disable=SC2016
echo 'cd "$(dirname "$0")/.." && [ "$(sh src/two.sh)" = two ]' >tests/two_test.sh
git add -A && git commit -qm two
t 0 "$wt" -- bash {}
for l in "base_sha=$F" tests=1 "test=tests/two_test.sh red" base=red; do has "$l"; done

# A file that turns into a folder of the same name can't be swapped: it
# says so and moves nothing.
git rm -q src/calc.sh && mkdir src/calc.sh && echo 'echo 5' >src/calc.sh/add.sh
git add -A && git commit -qm swap
t 1 "$wt" --skip-head -- bash {}
grep -q 'src/calc.sh is a file on one side' "$tmp/err" || fail "a file turned folder: no why"
hasnt '^base='

echo "ok: tests-on-base reads red for a test that needs the change and green for one that passes without it, per file with {}, from the merge base or --base, and leaves the tree, index and HEAD as they were, also when the command dies or the run is stopped or killed"
