#!/usr/bin/env bash
# tests-on-base.sh: do the change's new tests fail without the change? A
# test that passes either way proves nothing. The engineer and the
# reviewer both have to know, so one call answers it the same way for both.
#
#   tests-on-base.sh [--base <sha>] [--skip-head] -- <test command...>
#     Run it at the change's head, in a worktree whose tree is clean. It
#     runs the command at the head, which must pass; puts every changed
#     file that is no test back to its base content, the tests left as
#     the head has them; runs the command again; and puts the tree back.
#     --base: the commit to compare with, else the merge base of HEAD and
#     origin/<main> as last fetched. --skip-head: the head run just
#     passed, don't repeat it. A command word that is exactly {} runs the
#     command once per changed test file, with {} as that file's path
#     from where you stand.
#
# Prints key=value lines; the command's own output goes to the log= file.
#   base=red    every run failed at the base: the tests need the change.
#   base=green  green= of runs= passed there; test= lines name them.
# Exit 0: red. 1: green, or any failure. 2: usage, or a dirty tree. 3: no
# changed test file (base=no-tests). 4: red at the head (head=red).
# No base= line means no verdict: the command could not start at the base
# (exit 126 or 127), the change turns a file into a folder of the same
# name, or the tree could not be checked after the restore.
#
# In place, not in a second worktree: a new worktree has no installed
# dependencies, so every command would fail there and read as red. It
# records nothing, and writes only to its own temp folder: a reviewer
# runs it itself. Needs bash 4.4+ and git 2.25+.
set -euo pipefail

me='tests-on-base'
die() { echo "$me: $2" >&2; exit "$1"; }
usage() { die 2 "usage: tests-on-base.sh [--base <sha>] [--skip-head] -- <test command...>"; }

# 4.4: "${a[@]}" of an empty array under set -u.
((BASH_VERSINFO[0] * 100 + BASH_VERSINFO[1] >= 404)) || die 1 "needs bash 4.4 or later (macOS: brew install bash)"

base='' skip=''
while [ $# -gt 0 ]; do
	case $1 in
	--base) [ $# -ge 2 ] || usage; base=$2; shift 2 ;;
	--skip-head) skip=1; shift ;;
	--) shift; break ;;
	*) usage ;;
	esac
done
[ $# -gt 0 ] || usage
cmd=("$@")
per=''
for w in "${cmd[@]}"; do
	if [ "$w" = '{}' ]; then per=1; fi
done

top=$(git rev-parse --show-toplevel 2>/dev/null) || die 2 "run it inside the change's worktree"
prefix=$(git rev-parse --show-prefix)
# Every path is one file, whatever it is named: no globs, no :magic.
g() { git -C "$top" --literal-pathspecs "$@"; }
# A signal waits for this git call to end, so a stop never lands in the
# middle of a checkout.
shielded() { (trap '' INT TERM HUP; "$@"); }

[ -z "$(g status --porcelain)" ] || die 2 "the tree is dirty: git status --porcelain prints lines. This changes files in place and must put them back exactly, so commit first. If a killed run of this script left them, the top of its log= file has the command that puts the tree back."
head=$(g rev-parse HEAD)
ref=$(g symbolic-ref -q HEAD || echo detached)
printf -v t0 '%(%s)T' -1

if [ -n "$base" ]; then
	B=$(g rev-parse -q --verify "$base^{commit}") || die 2 "--base $base is no commit here"
else
	# The main branch, from the main checkout's project-setup.md, never
	# this tree's, as review-prep.sh reads it. Where the main repo is
	# bare, the install sits in the linked worktree that holds
	# .maestro/src/.
	wts=$(git worktree list --porcelain)
	mainwt=$(sed -n '1s/^worktree //p' <<<"$wts")
	if [ "$(sed -n 2p <<<"$wts")" = bare ]; then
		while IFS= read -r w; do
			[ -d "$w/.maestro/src" ] && { mainwt=$w; break; }
		done < <(sed -n 's/^worktree //p' <<<"$wts")
	fi
	setup=$mainwt/.maestro/project/project-setup.md
	# \r, spaces, the heading's case, a leading bullet and backticks
	# don't count.
	main=
	[ -f "$setup" ] && main=$(awk '{sub(/\r$/, ""); gsub(/^[ \t]+|[ \t]+$/, "")}
		/^## / {h=substr($0, 4); gsub(/^[ \t]+|[ \t]+$/, "", h); on=(tolower(h)=="main branch"); next}
		on && $0 != "" && !/^#/ {sub(/^[-* ]+/, ""); gsub(/^[`* ]+|[`* ]+$/, ""); print; exit}' "$setup")
	[ -n "$main" ] || die 1 "no branch under ## Main branch in $setup: give --base"
	# No fetch here: the caller fetched, and hundreds fetching at once collide.
	g rev-parse -q --verify "refs/remotes/origin/$main^{commit}" >/dev/null ||
		die 1 "no origin/$main here: fetch, or give --base"
	B=$(g merge-base HEAD "refs/remotes/origin/$main") || die 1 "HEAD and origin/$main share no commit"
fi

# The test class of review-prep.sh's class(), and only that one.
is_test() {
	case /$1 in
	*/test/* | */tests/* | */__tests__/* | */spec/* | *_test.* | *.test.* | *.spec.* | */test_*.py) return 0 ;;
	esac
	return 1
}
# What tests read. It stays as the head has it and is never run: put
# back to the base, it would fail a test that never touches the change,
# and read as red.
is_fixture() {
	case /$1 in
	*/testdata/* | */fixtures/* | */__fixtures__/* | */__snapshots__/* | */__mocks__/* | */conftest.py) return 0 ;;
	esac
	return 1
}
# tests: the changed test files the head has; a deleted one stays gone
# and is not run. code: every other changed file. added: those the base
# lacks. inbase: those it has.
tests=() code=() added=() inbase=() gone=()
declare -A changed=()
while IFS= read -r -d '' st && IFS= read -r -d '' p; do
	# A line break in a name would print as a line of its own, and could
	# read as this script's verdict.
	[[ $p != *[[:cntrl:]]* ]] || die 1 "a changed path holds a control character: check these tests by hand"
	changed[$p]=1
	if is_fixture "$p"; then continue; fi
	if is_test "$p"; then
		[ "$st" = D ] || tests+=("$p")
		continue
	fi
	code+=("$p")
	if [ "$st" = A ]; then added+=("$p"); else inbase+=("$p"); fi
	[ "$st" != D ] || gone+=("$p")
done < <(g diff --no-renames --name-status -z "$B" "$head")
# A file on one side that is a folder on the other can't be swapped: git
# takes its path for all the folder holds, and the way back would fail.
for p in "${!changed[@]}"; do
	d=$p
	while [[ $d == */* ]]; do
		d=${d%/*}
		[ -z "${changed[$d]-}" ] || die 1 "$d is a file on one side of the change and a folder on the other: this can't swap it, so check these tests by hand"
	done
done

# A path only the base tracks comes back for the run and goes again
# after it. git status hides an ignored file, so one that sits there now,
# or a file or link where a folder of the path must go, would be written
# over and then removed.
for p in "${gone[@]}"; do
	if [ -e "$top/$p" ] || [ -L "$top/$p" ]; then
		die 2 "$p is not in the change's head, but something sits there, ignored or untracked: this would write over it and remove it. Move it away first"
	fi
	d=$p
	while [[ $d == */* ]]; do
		d=${d%/*}
		if [ -L "$top/$d" ] || { [ -e "$top/$d" ] && [ ! -d "$top/$d" ]; }; then
			die 2 "$d is a file or a link here, and a folder at the base: this would replace it. Move it away first"
		fi
	done
done
# A submodule's files aren't in this tree to swap.
! g diff --no-renames --raw "$B" "$head" | grep -Eq '^:(160000 [0-7]+|[0-7]+ 160000) ' ||
	die 1 "the change adds, removes or moves a submodule: this can't swap it, so check these tests by hand"

echo "base_sha=$B"
echo "head_sha=$head"
echo "tests=${#tests[@]}"
[ ${#tests[@]} -gt 0 ] || { echo base=no-tests; exit 3; }

tmp=$(mktemp -d)
log=$tmp/log
# NUL-ended lists, so a path with a space or a newline is still one path.
list() { local f=$1; shift; [ $# -eq 0 ] || printf '%s\0' "$@" >"$tmp/$f"; }
list code.paths "${code[@]}"
list added.paths "${added[@]}"
list inbase.paths "${inbase[@]}"
printf -v restore 'git -C %q --literal-pathspecs restore --source=%s --staged --worktree --pathspec-file-nul --pathspec-from-file=%q' \
	"$top" "$head" "$tmp/code.paths"
{
	echo "# tests-on-base: head $head, base $B"
	if [ ${#code[@]} -gt 0 ]; then
		echo "# A killed run leaves base code in the tree. This puts the head back:"
		echo "#   $restore"
	fi
} >"$log"
echo "log=$log"

# past <second>: wait until the clock has left it. Python's .pyc files
# and make trust a file whose size and whole-second mtime match the build
# they hold, so a file swapped twice inside one second could run as its
# other version, here or in the caller's next run.
past() {
	local now
	while printf -v now '%(%s)T' -1 && [ "$now" = "$1" ]; do sleep 0.2; done
}

# rel <path from the top>: the same file from where the caller stands.
rel() {
	local p=$1 pre=$prefix up='' d
	while [ -n "$pre" ]; do
		d=${pre%%/*}/
		pre=${pre#"$d"}
		if [ -z "$up" ] && [[ $p == "$d"* ]]; then p=${p#"$d"}; else up+=../; fi
	done
	p=$up$p
	# Never an option to the command.
	[[ $p != -* ]] || p=./$p
	printf '%s' "$p"
}

# run <stage> <test file, or ''>: the command's exit code in $rc, its
# output in the log. It runs as a job in a process group of its own, so
# a signal reaches this script at once and finish can stop the whole
# test run before it puts the files back under it.
run() {
	local w one=()
	for w in "${cmd[@]}"; do
		if [ "$w" = '{}' ]; then w=$(rel "$2"); fi
		one+=("$w")
	done
	printf '\n== %s:%s\n' "$1" "$(printf ' %q' "${one[@]}")" >>"$log"
	set -m
	"${one[@]}" </dev/null >>"$log" 2>&1 &
	set +m
	rc=0
	wait $! || rc=$?
	printf '== exit %s\n' "$rc" >>"$log"
}

# to_base: base code, head tests. The index moves first, in one atomic
# write, and the files after it. So from the first file changed, every
# path in code.paths is in the head or in the index, and the one command
# in $restore puts all of them back, wherever a kill lands: a file the
# change deleted is tracked while it is here, so git removes it again,
# even where .gitignore now hides it.
swapped='' swap_at=$t0
to_base() {
	local p d
	past "$t0"
	swapped=1
	[ ${#code[@]} -gt 0 ] || return 0
	shielded g restore --source="$B" --staged --pathspec-file-nul --pathspec-from-file="$tmp/code.paths"
	# What the change added: out of the index now, so plain files to
	# remove, with the folders that came with them, but never the one the
	# caller stands in.
	for p in "${added[@]}"; do
		rm -f -- "$top/$p"
		d=$p
		while [[ $d == */* ]]; do
			d=${d%/*}
			[[ $prefix == "$d"/* ]] && break
			rmdir -- "$top/$d" 2>/dev/null || break
		done
	done
	# The rest, from the index: what the change edited or deleted.
	[ ${#inbase[@]} -eq 0 ] ||
		shielded g restore --worktree --pathspec-file-nul --pathspec-from-file="$tmp/inbase.paths"
	printf -v swap_at '%(%s)T' -1
}

at_head() {
	[ -z "$(g status --porcelain 2>&1)" ] && [ "$(g rev-parse HEAD 2>/dev/null)" = "$head" ] &&
		[ "$(g symbolic-ref -q HEAD || echo detached)" = "$ref" ]
}
# back: the tree at the head again, checked, or a loud failure. Only the
# paths this script changed: anything else in the tree is not ours to
# overwrite. A failed git call is no verdict; at_head is.
back() {
	[ -n "$swapped" ] || return 0
	swapped=
	if [ ${#code[@]} -gt 0 ]; then
		past "$swap_at"
		shielded g restore --source="$head" --staged --worktree --pathspec-file-nul \
			--pathspec-from-file="$tmp/code.paths" >>"$log" 2>&1 || true
	fi
	at_head && return 0
	{
		echo "$me: THE TREE IS NOT BACK AT ITS HEAD $head ($ref)."
		echo "HEAD is $(g rev-parse HEAD 2>&1) ($(g symbolic-ref -q HEAD || echo detached)); git status --porcelain prints:"
		g status --porcelain 2>&1 | sed -n '1,20s/^/  /p' || true
		echo "This puts back every file this script changed:"
		echo "  $restore"
		echo "What git status still lists after it, the test command left. Log: $log"
	} >&2
	return 1
}

sig=''
finish() {
	# ec, not code: back reads the code array, and a local here would hide it.
	local ec=$? j
	# No slip in here may stop the restore.
	set +eu
	trap - EXIT
	trap '' INT TERM HUP
	# Stop the test run first, or it writes while the files go back.
	for j in $(jobs -p); do
		kill -TERM -- "-$j" 2>/dev/null
		for _ in {1..25}; do kill -0 -- "-$j" 2>/dev/null || break; sleep 0.2; done
		kill -KILL -- "-$j" 2>/dev/null
		wait "$j" 2>/dev/null
	done
	if back; then
		[ -z "$sig" ] || echo "$me: stopped by $sig; the tree is at its head" >&2
	else
		ec=1
	fi
	exit "$ec"
}
trap finish EXIT
trap 'sig=INT; exit 1' INT
trap 'sig=TERM; exit 1' TERM
trap 'sig=HUP; exit 1' HUP

files=('')
[ -z "$per" ] || files=("${tests[@]}")

# A red at the base means nothing if the head is red too.
if [ -z "$skip" ]; then
	for f in "${files[@]}"; do
		run head "$f"
		[ "$rc" -eq 0 ] && continue
		echo "$me: red at the head${f:+: $f} (exit $rc)" >&2
		echo head=red
		exit 4
	done
	[ -z "$(g status --porcelain)" ] ||
		die 1 "the test command leaves the tree dirty ($(g status --porcelain | sed -n '1{s/^...//;p;}')), so a restore after it could not be checked"
fi

to_base
green=0 norun=''
for f in "${files[@]}"; do
	run base "$f"
	case $rc in
	0) green=$((green + 1)) ;;
	# Not found, or not runnable: the run never started, and that is no
	# red. A typo, or a runner the change itself added, would pass any test.
	126 | 127) norun="exit $rc${f:+ on $f}"; continue ;;
	esac
	[ -z "$f" ] || echo "test=$f $([ "$rc" -eq 0 ] && echo green || echo red)"
done
# No verdict over a tree that is not back.
back || exit 1
[ -z "$norun" ] || die 1 "at the base the command could not start ($norun), which is no red: see $log"

echo "runs=${#files[@]}"
if [ "$green" -gt 0 ]; then
	echo "green=$green"
	echo base=green
	exit 1
fi
echo base=red
