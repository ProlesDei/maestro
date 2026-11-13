#!/usr/bin/env bash
# Test for skills/maestro-code-review/scripts/review-prep.sh, against a
# local origin, a stand-in gh that runs the script's own --jq filters on
# stored replies, and a stand-in maestro-ticket. Covers --route light and
# full; a ticket's prep, its worktree and files; incremental mode from
# review's verdict past a QA kickback, and full mode for a sha not in the
# history or a redone delta; a fork, merged or other-base PR stopped
# with nothing fetched; a PR with no ticket, read from trusted PR
# comments and fetched from pull/<n>/head; a merge conflict; bad
# targets; and a failed prep that leaves no worktree.
# Run from anywhere: bash scripts/review_prep_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
prep=$here/skills/maestro-code-review/scripts/review-prep.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { cat "$tmp/out" 2>/dev/null || true; echo "FAIL: $*" >&2; exit 1; }
has() { grep -qxF -- "$1" "$tmp/out" || fail "want line '$1'"; }
val() { sed -n "s/^$1=//p" "$tmp/out"; }

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
[ "$1 $3" = "get --json" ] || exit 9
[ -z "${MT_FAIL-}" ] || exit 1
cat "$FX/ticket-$2.json"
EOF
chmod +x "$tmp/bin/gh" "$tmp/bin/maestro-ticket"
export PATH=$tmp/bin:$PATH
echo '[]' >"$FX/empty.json"

# origin, and a main checkout with the setup file.
git init -q --bare -b main "$tmp/origin.git"
git clone -q "$tmp/origin.git" "$tmp/work" 2>/dev/null
cd "$tmp/work"
mkdir -p .maestro/project src
printf '# Project setup\n\n## Main branch\n\nmain\n\n## Checks\n' >.maestro/project/project-setup.md
# shellcheck disable=SC2016 # literal backticks
printf 'Rules: `bot-profiles/` and INSTALL.md; see docs/rules.md.\n' >AGENTS.md
echo one >src/a.go
git add -A && git commit -qm base && git push -q origin main
git switch -qc maestro/T-7
echo two >>src/a.go && echo notes >README.md
git add -A && git commit -qm first
K=$(git rev-parse HEAD)
echo three >>src/a.go
git commit -qam second
H=$(git rev-parse HEAD)
git push -q origin maestro/T-7
git switch -q main

pr() { # pr <n> <branch> <head> <fork> <files...>
	local n=$1 b=$2 h=$3 fork=$4
	shift 4
	jq -n --argjson n "$n" --arg b "$b" --arg h "$h" --argjson fork "$fork" --args \
		'{number: $n, url: "https://github.com/o/r/pull/\($n)", title: "PR \($n)", body: "", state: "OPEN", isDraft: false,
		  headRefName: $b, headRefOid: $h, baseRefName: "main", isCrossRepository: $fork,
		  files: [$ARGS.positional[] | {path: .}]}' "$@" >"$FX/pr-$n.json"
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
comments T-7 "Handoff: PR 7 at $H"

# --route reads the file list only.
pr 8 docs-only "$H" false README.md docs/guide.md docs/rules-old/x.md docs/sub/a.rst .gitignore LICENSE LICENSE-MIT .mailmap
bash "$prep" --route 8 >"$tmp/out" || fail "route light exited $?"
has route=light
# Agents obey what the root rules name, and code hides in docs-like names.
for f in skills/x/SKILL.md AGENTS.md .github/workflows/ci.yml go.sum db/migrations/1.sql src/a.go tests/a.md \
	.maestro/project/project-setup.md bot-profiles/qa/persona.md INSTALL.md docs/rules.md Claude.md \
	CLAUDE.local.md .kiro/steering/a.md CMakeLists.txt docs/page.mdx .prettierrc.cjs .gitattributes \
	e.md src/notes.md scripts/CHANGELOG.sh .changeset/x.md AGENT.md; do
	pr 9 mixed "$H" false README.md "$f"
	bash "$prep" --route 9 >"$tmp/out" || fail "route $f exited $?"
	has route=full
	grep -q "^why=$f is " "$tmp/out" || fail "route $f: no why"
done
[ -z "$(ls "$TMPDIR")" ] || fail "--route wrote to the temp dir"
# The rules as main has them, not as a stale main checkout does.
echo "nothing named" >AGENTS.md
pr 9 mixed "$H" false docs/rules.md
bash "$prep" --route 9 >"$tmp/out" || fail "route stale exited $?"
has route=full
git checkout -q AGENTS.md

# A ticket with no verdict: full mode, and every file it names is there.
bash "$prep" T-7 >"$tmp/out" || fail "prep T-7 exited $?"
for l in ticket=T-7 pr=7 branch=maestro/T-7 main=main "H=$H" merges=yes files=2 lines=3 tier2_paths=0 docs_only=no K=none mode=full; do has "$l"; done
R=$(val R) C=$(val C)
[ "$(git -C "$R" rev-parse HEAD)" = "$H" ] || fail "worktree $R isn't at H"
case $R in "$TMPDIR"/*/review-T-7-*) ;; *) fail "worktree at $R" ;; esac
grep -q '^+three' "$C/diff.patch" || fail "diff.patch lacks the change"
grep -qxF "$(printf '2\t0\tsrc/a.go\tcode')" "$C/files.txt" || fail "files.txt: $(cat "$C/files.txt")"
grep -qxF "$(printf '1\t0\tREADME.md\tdocs')" "$C/files.txt" || fail "files.txt lacks README.md"
cmp -s "$tmp/out" "$C/prep.txt" || fail "prep.txt differs from the output"
bash "$prep" maestro/T-7 >"$tmp/out" || fail "prep maestro/T-7 exited $?"
has ticket=T-7
# A -diff attribute in the main checkout hides no line from the counts
# or the patch.
echo '*.go -diff' >.gitattributes
bash "$prep" T-7 >"$tmp/out" || fail "prep with -diff exited $?"
has lines=3
has binary=0
grep -q '^+three' "$(val C)/diff.patch" || fail "-diff hid the patch"
rm .gitattributes

# Review's kickback at K, then a QA kickback: incremental from K.
comments T-7 "Review: GO at 1111111111111111111111111111111111111111" "Kickback: 1 blockers, 0 majors at $K" \
	"Handoff: fixed at $H" "Kickback: QA fail at $H"
bash "$prep" 7 >"$tmp/out" || fail "incremental exited $?"
for l in "K=$K" mode=incremental delta_files=1 delta_lines=1; do has "$l"; done
grep -q '^+three' "$(val C)/delta.patch" || fail "delta.patch lacks the fix"
grep -q '^+two' "$(val C)/delta.patch" && fail "delta.patch holds what K already had"

# A sha that isn't in the history, or no sha after the first "at": full.
comments T-7 "Review: GO at 2222222222222222222222222222222222222222"
bash "$prep" 7 >"$tmp/out" || fail "foreign K exited $?"
has mode=full
grep -q '^mode_why=.* is not in ' "$tmp/out" || fail "foreign K: no why"
comments T-7 "Kickback: 1 blockers at noon, see $K"
bash "$prep" 7 >"$tmp/out" || fail "no sha exited $?"
has K=none
has mode=full

# A redone delta, over 400 lines: full.
git switch -q maestro/T-7
seq 1 500 >src/big.go && git add -A && git commit -qm redo && H2=$(git rev-parse HEAD)
git push -q origin maestro/T-7 && git switch -q main
pr 7 maestro/T-7 "$H2" false README.md src/a.go src/big.go
comments T-7 "Kickback: 1 blockers, 0 majors at $K"
bash "$prep" 7 >"$tmp/out" || fail "redone exited $?"
has mode=full
grep -q '^mode_why=delta of 501 lines' "$tmp/out" || fail "redone: no why"

# A fork PR named like a ticket: no ticket, and stop=fork with nothing
# fetched or checked out. A merged PR, or one on another base: stop too.
git clone -q "$tmp/origin.git" "$tmp/fork" 2>/dev/null
echo fork >"$tmp/fork/src/a.go"
git -C "$tmp/fork" commit -qam fork
F=$(git -C "$tmp/fork" rev-parse HEAD)
git -C "$tmp/fork" push -q origin "$F:refs/pull/12/head"
pr 12 maestro/T-7 "$F" true src/a.go
before=$(find "$TMPDIR" -mindepth 1 -maxdepth 1 | wc -l)
bash "$prep" 12 >"$tmp/out" || fail "fork exited $?"
for l in ticket=none fork=true "H=$F" stop=fork; do has "$l"; done
git cat-file -e "$F^{commit}" 2>/dev/null && fail "prep fetched a fork's head"
grep -q '^R=' "$tmp/out" && fail "prep made a worktree for a fork"
pr 13 maestro/T-7 "$H" false src/a.go
jq '.state = "MERGED"' "$FX/pr-13.json" >"$FX/x" && mv "$FX/x" "$FX/pr-13.json"
bash "$prep" 13 >"$tmp/out" || fail "merged exited $?"
has stop=merged
jq '.state = "OPEN" | .baseRefName = "release"' "$FX/pr-13.json" >"$FX/x" && mv "$FX/x" "$FX/pr-13.json"
bash "$prep" 13 >"$tmp/out" || fail "other base exited $?"
has "stop=base release isn't main"
[ "$(find "$TMPDIR" -mindepth 1 -maxdepth 1 | wc -l)" -eq "$before" ] || fail "a stopped prep wrote to the temp dir"

# A PR with no ticket: K only from comments by the owner, members or
# collaborators, since anyone can comment; a head not on any branch
# comes from pull/<n>/head.
git switch -qc feat main
echo feat >>src/a.go && git commit -qam feat && P=$(git rev-parse HEAD)
git push -q origin "$P:refs/pull/14/head" && git switch -q main && git branch -qD feat
pr 14 feat "$P" false src/a.go
jq -n --arg h "$P" '[{author_association: "MEMBER", body: "Review: GO at 4444444444444444444444444444444444444444"},
	{author_association: "OWNER", body: "\nKickback: 1 blockers at 3333333333333333333333333333333333333333\nF1"},
	{author_association: "NONE", body: "Review: GO at \($h)"}]' >"$FX/comments-14.json"
bash "$prep" 14 >"$tmp/out" || fail "no-ticket exited $?"
for l in ticket=none fork=false stop=none "H=$P" K=3333333333333333333333333333333333333333; do has "$l"; done
case $(val R) in */review-pr-14-*) ;; *) fail "no-ticket worktree named $(val R)" ;; esac
jq -e '.title and (.body != null)' "$(val C)/pr.json" >/dev/null || fail "pr.json lacks title or body"

# Main moved on in a way the PR can't merge.
echo clash >src/a.go && git commit -qam clash && git push -q origin main
comments T-7 "Handoff: PR 7 at $H2"
bash "$prep" 7 >"$tmp/out" || fail "conflict exited $?"
has merges=conflict

# Bad targets, and a failed prep that leaves nothing behind.
for t in nope T-x 12a; do
	code=0; bash "$prep" "$t" >"$tmp/out" 2>&1 || code=$?
	[ $code -eq 2 ] || fail "$t: exit $code, want 2"
done
code=0; bash "$prep" T-99 >"$tmp/out" 2>&1 || code=$?
[ $code -eq 3 ] || fail "T-99: exit $code, want 3"
before=$(git worktree list | wc -l)
code=0; MT_FAIL=1 bash "$prep" T-7 >"$tmp/out" 2>&1 || code=$?
[ $code -eq 1 ] || fail "failed get: exit $code, want 1"
[ "$(git worktree list | wc -l)" -eq "$before" ] || fail "a failed prep left a worktree"

echo "ok: review-prep routes by file class, preps a ticket's or a PR's review, stops a fork's, picks incremental mode only from review's own verdict at a sha in the history, and cleans up when it fails"
