#!/usr/bin/env bash
# Test for review-prep.sh --qa and its wrapper,
# skills/maestro-qa-lead/scripts/qa-prep.sh, against a local origin, a
# stand-in gh that runs the script's own --jq filters on stored replies,
# and a stand-in maestro-ticket, as scripts/review_prep_test.sh does.
# Covers a first QA run: its run name, worktree with today's main merged
# in, evidence folder and files; a merge with no git identity; what
# review passed; QA's last verdict; retest mode only from a fail that
# tested at a sha in the history, and full mode for the rest; the file
# classes of the delta and of main's changes; a merge conflict; a PR
# with no ticket; a fork or merged PR stopped with nothing made; a
# failed prep that leaves nothing; the wrapper; and review mode, which
# prints no QA key.
# Run from anywhere: bash scripts/qa_prep_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
prep=$here/skills/maestro-code-review/scripts/review-prep.sh
wrap=$here/skills/maestro-qa-lead/scripts/qa-prep.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { cat "$tmp/out" 2>/dev/null || true; echo "FAIL: $*" >&2; exit 1; }
has() { grep -qxF -- "$1" "$tmp/out" || fail "want line '$1'"; }
val() { sed -n "s/^$1=//p" "$tmp/out"; }
qa() { bash "$prep" --qa "$1" >"$tmp/out" || fail "--qa $1 exited $?"; }

export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export TMPDIR=$tmp/t FX=$tmp/fx
mkdir -p "$TMPDIR" "$FX" "$tmp/bin"

# Stand-ins. gh reads $FX/<name>.json and applies --jq as gh does (raw).
cat >"$tmp/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
q=.
args=()
while [ $# -gt 0 ]; do
	case $1 in --jq) q=$2; shift 2 ;; --json) shift 2 ;; --paginate) shift ;; *) args+=("$1"); shift ;; esac
done
case "${args[0]} ${args[1]}" in
"api repos/{owner}/{repo}/pulls?head={owner}:"*)
	b=${args[1]#*head=\{owner\}:}; b=${b%%&*}; f=$FX/pulls-${b//\//_}.json ;;
"api repos/{owner}/{repo}/issues/"*)
	n=${args[1]#*issues/}; f=$FX/comments-${n%%/*}.json ;;
"pr view") f=$FX/pr-${args[2]}.json ;;
*) echo "gh stand-in: ${args[*]}" >&2; exit 9 ;;
esac
[ -f "$f" ] || { [ "${args[0]}" = api ] && f=$FX/empty.json; }
[ -f "$f" ] || { echo "no pull requests found" >&2; exit 1; }
jq -r "$q" "$f"
EOF
cat >"$tmp/bin/maestro-ticket" <<'EOF'
#!/usr/bin/env bash
if [ "$1" = branch ]; then echo "${BOARD_PREFIX:-maestro/}${2-}"; exit 0; fi
[ "$1 $3" = "get --json" ] || exit 9
[ -z "${MT_FAIL-}" ] || exit 1
cat "$FX/ticket-$2.json"
EOF
chmod +x "$tmp/bin/gh" "$tmp/bin/maestro-ticket"
export PATH=$tmp/bin:$PATH
echo '[]' >"$FX/empty.json"

# origin, and a main checkout with the setup file. The PR branch has two
# commits, K then H; main then moves on to G, with a new dependency.
git init -q --bare -b main "$tmp/origin.git"
git clone -q "$tmp/origin.git" "$tmp/work" 2>/dev/null
cd "$tmp/work"
mkdir -p .maestro/project src
printf '# Project setup\n\n## Main branch\n\nmain\n\n## Checks\n' >.maestro/project/project-setup.md
echo rules >AGENTS.md
echo one >src/a.go
git add -A && git commit -qm base && git push -q origin main
G0=$(git rev-parse HEAD)
git switch -qc maestro/T-7
echo two >>src/a.go && echo notes >README.md
git add -A && git commit -qm first
K=$(git rev-parse HEAD)
echo three >>src/a.go
git commit -qam second
H=$(git rev-parse HEAD)
git push -q origin maestro/T-7
git switch -q main
echo module >go.mod && echo b >src/b.go
git add -A && git commit -qm "main moves" && git push -q origin main
G=$(git rev-parse HEAD)
ev=$(git rev-parse --path-format=absolute --git-common-dir)/maestro/evidence
url=https://github.com/o/r/pull/7
x40() { printf "$1%.0s" {1..40}; }

pr() { # pr <n> <branch> <head> <fork> <file...>
	local n=$1 b=$2 h=$3 fork=$4
	shift 4
	jq -n --argjson n "$n" --arg b "$b" --arg h "$h" --argjson fork "$fork" --args \
		'{number: $n, url: "https://github.com/o/r/pull/\($n)", title: "PR \($n)", body: "", state: "OPEN", isDraft: false,
		  headRefName: $b, headRefOid: $h, baseRefName: "main", isCrossRepository: $fork, additions: 3, deletions: 0,
		  files: [$ARGS.positional[] | {changeType: "MODIFIED", path: .}]}' "$@" >"$FX/pr-$n.json"
}
comments() { # comments <id> <body...>: a ticket's get --json, trimmed as get trims
	local id=$1
	shift
	jq -n --args '{comments: [$ARGS.positional | to_entries[] |
		if .key == 0 then {id: 1, trimmed: true, first_line: .value} else {id: (.key + 1), body: .value} end]}' \
		"$@" >"$FX/ticket-$id.json"
}
echo '[{"number": 7}]' >"$FX/pulls-maestro_T-7.json"
pr 7 maestro/T-7 "$H" false README.md src/a.go
go="Review: GO at $H (PR $url), tier 1, gated on $G0"
comments T-7 "Handoff: PR 7 at $H" "$go"$'\nFor QA: try an empty input'

# A first QA run: full mode, review's GO at H, and main merged into a
# worktree named for the run, with its evidence folder made.
qa T-7
for l in ticket=T-7 pr=7 branch=maestro/T-7 main=main "H=$H" stop=none "G=$G" "B=$G0" merged=yes files=2 lines=3 \
	tier2_paths=0 docs_only=no "review_go=$H" qa_last=none qa_last_at=none qa_last_main=none K=none \
	delta_files=none delta_classes=none main_since_classes=unknown mode=full "mode_why=no earlier QA verdict"; do has "$l"; done
grep -q '^merges=' "$tmp/out" && fail "--qa printed review's merges="
run=$(val run) R=$(val R) C=$(val C) E=$(val E)
[[ $run =~ ^qa-T-7-[a-z]{6}$ ]] || fail "run named $run"
[[ $(val S) =~ ^[0-9]+$ ]] || fail "S is $(val S)"
case $R in "$TMPDIR"/*/"$run") ;; *) fail "worktree at $R" ;; esac
[ "$(git -C "$R" rev-parse HEAD^1)" = "$H" ] || fail "the worktree's first parent isn't H"
[ "$(git -C "$R" rev-parse HEAD^2)" = "$G" ] || fail "the worktree's second parent isn't G"
[ -f "$R/go.mod" ] || fail "the worktree lacks main's change"
grep -qx three "$R/src/a.go" || fail "the worktree lacks the PR's change"
[ -z "$(git -C "$R" status --porcelain)" ] || fail "the worktree isn't clean"
[ "$E" = "$ev/T-7/$run" ] && [ -d "$E" ] || fail "evidence folder $E"
grep -q '^+three' "$C/diff.patch" || fail "diff.patch lacks the change"
grep -q 'go.mod' "$C/diff.patch" && fail "diff.patch holds main's change"
grep -qxF "$(printf '2\t0\tsrc/a.go\tcode')" "$C/files.txt" || fail "files.txt: $(cat "$C/files.txt")"
for f in pr.json stat.txt log.txt; do [ -s "$C/$f" ] || fail "no $C/$f"; done
cmp -s "$tmp/out" "$C/prep.txt" || fail "prep.txt differs from the output"
# The merge stays in the worktree: no ref moved.
[ "$(git rev-parse origin/maestro/T-7)" = "$H" ] && [ "$(git ls-remote origin maestro/T-7 | cut -f1)" = "$H" ] ||
	fail "the PR branch moved"
# A second run shares no name, worktree or folder with the first.
qa maestro/T-7
[ "$(val run)" != "$run" ] && [ "$(val R)" != "$R" ] && [ "$(val E)" != "$E" ] && [ "$(val C)" != "$C" ] ||
	fail "two runs share a name or a folder"
[ -d "$E" ] && [ -d "$(val E)" ] || fail "one run's evidence folder is gone"

# With no git identity the merge still commits.
git config user.useConfigOnly true
env -u GIT_AUTHOR_NAME -u GIT_AUTHOR_EMAIL -u GIT_COMMITTER_NAME -u GIT_COMMITTER_EMAIL \
	bash "$prep" --qa 7 >"$tmp/out" || fail "no identity: exited $?"
has merged=yes
[ "$(git -C "$(val R)" rev-parse HEAD^2)" = "$G" ] || fail "no identity: G isn't merged"
git config --unset user.useConfigOnly

# What review passed: its latest Review: comment, and only a GO. A GO at
# another sha is that sha, which isn't H.
comments T-7 "$go" "Review: NO-GO at $H (PR $url)"
qa 7
has review_go=none
comments T-7 "Review: GO at $K (PR $url), tier 1, gated on $G0" "Handoff: fixed at $H"
qa 7
has "review_go=$K"
# Review's own later Kickback: takes its GO back, and a line that only
# quotes a GO is none; an indented first line still reads.
comments T-7 "Review: GO at $H (PR $url), tier 1, gated on $G0" "Kickback: 1 blockers at $H" "Handoff: fixed at $H"
qa 7
has review_go=none
comments T-7 "$go" "Review: report-only at $H; the last was Review: GO at $H"
qa 7
has review_go=none
comments T-7 "Handoff: PR 7 at $H" "   Review: GO at $H (PR $url), tier 1"
qa 7
has "review_go=$H"
comments T-7 "Review: GO at $K (PR $url), tier 1, gated on $G0" "Handoff: fixed at $H"
qa 7
has qa_last=none
has mode=full
comments T-7 "Handoff: PR 7 at $H" "Rereview: QA at $H: review passed none"
qa 7
has review_go=none

# A QA fail that tested K: retest, with the author's delta since K and
# the classes of what main changed since that verdict's G.
qfail="Kickback: QA fail at $K (PR $url), tier 1, on main at $G0"
comments T-7 "Review: GO at $K" "$qfail" "Handoff: fixed at $H" "$go"
qa 7
for l in "review_go=$H" qa_last=fail "qa_last_at=$K" "qa_last_main=$G0" "K=$K" delta_files=1 delta_lines=1 \
	delta_classes=code "main_since_classes=code deps" mode=retest "mode_why=from QA's fail at $K"; do has "$l"; done
grep -q '^+three' "$(val C)/delta.patch" || fail "delta.patch lacks the fix"
grep -q '^+two' "$(val C)/delta.patch" && fail "delta.patch holds what K already had"
grep -q 'delta.patch' <<<"$(val wrote)" || fail "wrote= lacks delta.patch"

# The latest QA verdict counts: a pass is no base, and names its main.
comments T-7 "$qfail" "$go" "QA: pass at $H (PR $url), tier 1, on main at $G"
qa 7
for l in qa_last=pass "qa_last_at=$H" "qa_last_main=$G" K=none main_since_classes=none mode=full; do has "$l"; done
# A fail that tested nothing, or one at a sha not in H's history: full.
comments T-7 "$go" "Kickback: QA fail at $K (PR $url): not tested: conflicts with main at $G0"
qa 7
for l in qa_last=not-tested "qa_last_at=$K" qa_last_main=none K=none delta_files=none mode=full; do has "$l"; done
comments T-7 "$go" "Kickback: QA fail at $(x40 2) (PR $url), tier 1, on main at $(x40 3)"
qa 7
for l in qa_last=fail "qa_last_at=$(x40 2)" "qa_last_main=$(x40 3)" K=none main_since_classes=unknown mode=full; do has "$l"; done
grep -q '^mode_why=.* is not in ' "$tmp/out" || fail "foreign K: no why"
# Review's own kickback is no QA verdict.
comments T-7 "Kickback: 1 blockers, 0 majors at $K" "$go"
qa 7
for l in qa_last=none K=none mode=full; do has "$l"; done

# A redone delta, over 400 lines, with a schema change in it: full, and
# the delta's classes still named.
git switch -q maestro/T-7
seq 1 500 >src/big.go && echo 'create table t (id int);' >db.sql
git add -A && git commit -qm redo && H2=$(git rev-parse HEAD)
git push -q origin maestro/T-7 && git switch -q main
pr 7 maestro/T-7 "$H2" false README.md src/a.go src/big.go db.sql
comments T-7 "$qfail" "Review: GO at $H2"
qa 7
for l in "H=$H2" "review_go=$H2" "K=$K" delta_files=3 tier2_paths=1 "delta_classes=code schema" mode=full; do has "$l"; done
grep -q '^mode_why=delta of 502 lines' "$tmp/out" || fail "redone: no why"

# A PR with no ticket: the run is named for the PR, and its verdicts come
# from comments by the owner, members or collaborators only.
git switch -qc feat "$G0"
echo feat >>src/a.go && git commit -qam feat && P=$(git rev-parse HEAD)
git push -q origin "$P:refs/pull/14/head" && git switch -q main && git branch -qD feat
pr 14 feat "$P" false src/a.go
jq -n --arg h "$P" --arg g "$G0" '[{author_association: "MEMBER", body: "Review: GO at \($h) (PR u), tier 1"},
	{author_association: "OWNER", body: "\nKickback: QA fail at \($h) (PR u), tier 1, on main at \($g)\n- 1 fail"},
	{author_association: "NONE", body: "Review: NO-GO at \($h)"},
	{author_association: "NONE", body: "QA: pass at \($h) (PR u), tier 1, on main at \($g)"}]' >"$FX/comments-14.json"
qa 14
for l in ticket=none stop=none "H=$P" "review_go=$P" qa_last=fail "qa_last_at=$P" "K=$P" delta_files=0 \
	delta_classes=none mode=retest merged=yes; do has "$l"; done
[[ $(val run) =~ ^qa-pr-14-[a-z]{6}$ ]] || fail "no-ticket run named $(val run)"
[ "$(val E)" = "$ev/pr-14/$(val run)" ] && [ -d "$(val E)" ] || fail "no-ticket evidence folder $(val E)"

# Main moved on in a way the PR can't merge: merged=conflict, exit 0,
# and the worktree back at H with no merge left open.
echo clash >src/a.go && git commit -qam clash && git push -q origin main
G2=$(git rev-parse HEAD)
comments T-7 "Review: GO at $H2"
qa 7
has merged=conflict
has "G=$G2"
R=$(val R)
[ "$(git -C "$R" rev-parse HEAD)" = "$H2" ] || fail "conflict: the worktree isn't at H"
[ -z "$(git -C "$R" status --porcelain)" ] || fail "conflict: the worktree isn't clean"
git -C "$R" rev-parse -q --verify MERGE_HEAD >/dev/null && fail "conflict: the merge is still open"
[ -d "$(val E)" ] || fail "conflict: no evidence folder"

# A fork PR named like a ticket, or a merged one: stop, with nothing
# fetched, checked out or made.
git clone -q "$tmp/origin.git" "$tmp/fork" 2>/dev/null
echo fork >"$tmp/fork/src/a.go"
git -C "$tmp/fork" commit -qam fork
F=$(git -C "$tmp/fork" rev-parse HEAD)
git -C "$tmp/fork" push -q origin "$F:refs/pull/12/head"
pr 12 maestro/T-7 "$F" true src/a.go
made() { echo "$(find "$TMPDIR" -mindepth 1 -maxdepth 1 | wc -l) $(find "$ev" -mindepth 2 -maxdepth 2 | wc -l) $(git worktree list | wc -l)"; }
before=$(made)
qa 12
for l in ticket=none fork=true "H=$F" stop=fork; do has "$l"; done
git cat-file -e "$F^{commit}" 2>/dev/null && fail "prep fetched a fork's head"
grep -qE '^(R|C|E|G)=' "$tmp/out" && fail "prep made something for a fork"
# A stop still names the run: it keys the note QA posts for it.
[[ $(val run) =~ ^qa-pr-12-[a-z]{6}$ ]] || fail "a stop names no run: $(val run)"
pr 13 maestro/T-7 "$H2" false src/a.go
jq '.state = "MERGED"' "$FX/pr-13.json" >"$FX/x" && mv "$FX/x" "$FX/pr-13.json"
qa 13
has stop=merged
jq '.state = "OPEN" | .baseRefName = "release"' "$FX/pr-13.json" >"$FX/x" && mv "$FX/x" "$FX/pr-13.json"
qa 13
has "stop=base release isn't main"
[ "$(made)" = "$before" ] || fail "a stopped prep left something: $(made), was $before"

# Bad targets, and a failed prep that leaves no worktree, temp folder or
# evidence folder.
code=0; bash "$prep" --qa >"$tmp/out" 2>&1 || code=$?
[ $code -eq 2 ] || fail "no target: exit $code, want 2"
code=0; bash "$prep" --qa nope >"$tmp/out" 2>&1 || code=$?
[ $code -eq 2 ] || fail "nope: exit $code, want 2"
code=0; bash "$prep" --qa T-99 >"$tmp/out" 2>&1 || code=$?
[ $code -eq 3 ] || fail "T-99: exit $code, want 3"
code=0; MT_FAIL=1 bash "$prep" --qa T-7 >"$tmp/out" 2>&1 || code=$?
[ $code -eq 1 ] || fail "failed get: exit $code, want 1"
[ "$(made)" = "$before" ] || fail "a failed prep left something: $(made), was $before"

# The wrapper finds review-prep.sh from its own path, here and in an
# install, where the two skills sit side by side; alone, it says why.
bash "$wrap" T-7 >"$tmp/out" || fail "wrapper exited $?"
[[ $(val run) =~ ^qa-T-7-[a-z]{6}$ ]] || fail "wrapper: run named $(val run)"
has merged=conflict
inst=$tmp/inst/.claude/skills
mkdir -p "$inst/maestro-qa-lead/scripts" "$inst/maestro-code-review/scripts" "$tmp/alone/maestro-qa-lead/scripts"
cp "$wrap" "$inst/maestro-qa-lead/scripts/" && cp "$prep" "$inst/maestro-code-review/scripts/"
bash "$inst/maestro-qa-lead/scripts/qa-prep.sh" 7 >"$tmp/out" || fail "installed wrapper exited $?"
has "H=$H2"
has mode=full
cp "$wrap" "$tmp/alone/maestro-qa-lead/scripts/"
code=0; bash "$tmp/alone/maestro-qa-lead/scripts/qa-prep.sh" 7 >"$tmp/out" 2>&1 || code=$?
[ $code -eq 1 ] || fail "wrapper alone: exit $code, want 1"
[ "$(wc -l <"$tmp/out")" -eq 1 ] || fail "wrapper alone: more than one line"
grep -q '^qa-prep: no .*review-prep.sh' "$tmp/out" || fail "wrapper alone: no reason"

# Review mode prints none of QA's keys and makes no evidence folder.
before=$(find "$ev" -mindepth 2 -maxdepth 2 | wc -l)
bash "$prep" 7 >"$tmp/out" || fail "review mode exited $?"
has merges=conflict
case $(val R) in */review-T-7-*) ;; *) fail "review worktree named $(val R)" ;; esac
grep -qE '^(run|E|G|merged|review_go|qa_last|qa_last_at|qa_last_main|delta_classes|main_since_classes)=' "$tmp/out" &&
	fail "review mode printed a QA key"
[ "$(find "$ev" -mindepth 2 -maxdepth 2 | wc -l)" -eq "$before" ] || fail "review mode made an evidence folder"

echo "ok: review-prep --qa names a run of its own, merges today's main into its worktree, makes the evidence folder, reads review's GO and QA's last verdict, picks retest mode only from a tested QA fail at a sha in the history, stops a fork's, and cleans up when it fails; qa-prep.sh wraps it"
