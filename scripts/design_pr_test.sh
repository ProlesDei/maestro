#!/usr/bin/env bash
# Test for skills/maestro-designer/scripts/design-pr.sh, against a local
# origin, a stand-in gh that records each PR call, and a stand-in
# maestro-ticket that serves a stored ticket and records each write.
# Covers bad arguments; each spec gap, refused before any write; a ticket
# that isn't ours to hand up; the first hand-up (branch, files, [skip ci],
# draft PR on the main branch the setup names, spec and card comments in
# order, the block); a rework that updates the PR and swaps the shots;
# and a rerun that repeats nothing.
# Run from anywhere: bash scripts/design_pr_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
dpr=$here/skills/maestro-designer/scripts/design-pr.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { cat "$tmp/out" 2>/dev/null || true; echo "FAIL: $*" >&2; exit 1; }
has() { grep -qxF -- "$1" "$tmp/out" || fail "want line '$1'"; }

export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export TMPDIR=$tmp/t FX=$tmp/fx
mkdir -p "$TMPDIR" "$FX" "$tmp/bin"

# gh: repo view, the PR list (from $FX/open-pr), pr create and pr edit.
cat >"$tmp/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo "gh $*" >>"$FX/log"
case "$1 $2" in
"repo view") echo o/r ;;
"api repos/{owner}/{repo}/pulls?head={owner}:"*) cat "$FX/open-pr" 2>/dev/null || true ;;
"pr create")
	while [ $# -gt 0 ]; do [ "$1" = --body-file ] && cp "$2" "$FX/body"; shift; done
	echo "Creating draft pull request"; echo https://github.com/o/r/pull/9 ;;
"pr edit")
	while [ $# -gt 0 ]; do [ "$1" = --body-file ] && cp "$2" "$FX/body"; shift; done ;;
*) echo "gh stand-in: $*" >&2; exit 9 ;;
esac
EOF
# maestro-ticket: get serves $FX/ticket.json; comment and transition log.
cat >"$tmp/bin/maestro-ticket" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case $1 in
get) cat "$FX/ticket.json" ;;
comment)
	n=$(( $(cat "$FX/n" 2>/dev/null || echo 0) + 1 )); echo $n >"$FX/n"
	key=; while [ $# -gt 0 ]; do case $1 in --body-file) cp "$2" "$FX/comment-$n";; --idempotency-key) key=$2;; esac; shift; done
	echo "comment $n $key actor=$MAESTRO_TICKET_ACTOR" >>"$FX/log" ;;
transition) echo "transition ${*:2}" >>"$FX/log" ;;
*) exit 9 ;;
esac
EOF
chmod +x "$tmp/bin/gh" "$tmp/bin/maestro-ticket"
export PATH=$tmp/bin:$PATH

# origin, and a main checkout whose setup names trunk as the main branch.
git init -q --bare -b trunk "$tmp/origin.git"
git clone -q "$tmp/origin.git" "$tmp/work" 2>/dev/null
cd "$tmp/work"
mkdir -p .maestro/project
# shellcheck disable=SC2016 # literal backticks
printf '# Project setup\n\n## Main branch\n\n%s\n' '`trunk`' >.maestro/project/project-setup.md
echo app >app.txt
git add -A && git commit -qm init && git push -q origin HEAD:trunk

ticket() { # <status> <holder> [title] [type]
	jq -n --arg s "$1" --arg h "$2" --arg t "${3:-Design: pricing page}" --arg y "${4:-scout}" \
		'{id: "T-7", title: $t, type: $y, status: $s, role: "designer", claim: (if $h == "" then null else {holder: $h, token: 3} end)}' >"$FX/ticket.json"
}
H=designer/ab12cd
spec() { # writes a good spec to $1/spec.md, picking $2
	cat >"$1/spec.md" <<EOF
Problem: buyers can't compare plans before they sign up.
Breadboard:
- Pricing (/pricing): three plans; Start buttons go to /signup?plan=<name>
Directions:
A: three cards in a row, Pro raised.
B: a table with one row per feature.
Pick: $2: easiest to compare.
States: empty: none; loading: none; error: none; long names wrap; phone: cards stack.
Reuses: Card, Button | New: none
Criteria:
1. At 375px the three plans stack, Pro first.
2. At 1280px every plan shows its price and Start button.
Taste calls: Pro gets the brand color; default yes.
EOF
}
run_dir=$tmp/run
mkdir -p "$run_dir"
spec "$run_dir" B
for f in sheet-375 sheet-1280 a-375 a-1280 b-375 b-1280; do echo "$f" >"$run_dir/$f.png"; done
run() { bash "$dpr" "$@" >"$tmp/out" 2>&1; }
writes() { [ -s "$FX/log" ] && grep -qE '^(comment|transition|gh pr)' "$FX/log"; }

# Bad arguments.
for args in "T-7" "7 $run_dir --holder $H --token 3" "T-7 $run_dir --holder nobody --token 3" \
	"T-7 $run_dir --holder $H --token x" "T-7 $tmp/nope --holder $H --token 3" "T-7 $run_dir --holder $H --token 3 --bogus"; do
	# shellcheck disable=SC2086
	if run $args; then fail "accepted: $args"; else rc=$?; [ $rc = 2 ] || fail "exit $rc for: $args"; fi
done

# Each spec gap stops before any write.
ticket in_progress "$H"
gap() { # <want problem substring> <sed script on spec.md> [files to remove]
	rm -rf "$tmp/g" && cp -R "$run_dir" "$tmp/g"
	sed -i "$2" "$tmp/g/spec.md"
	# shellcheck disable=SC2086 # $3 is a glob
	[ -z "${3-}" ] || rm -f "$tmp/g"/$3
	: >"$FX/log"
	if run T-7 "$tmp/g" --holder "$H" --token 3; then fail "passed a spec with: $1"; else rc=$?; [ $rc = 2 ] || fail "exit $rc for: $1"; fi
	grep -q "^problem=.*$1" "$tmp/out" || fail "no problem line for: $1"
	! writes || fail "wrote with a spec gap: $1"
	[ -z "$(git ls-remote --heads origin maestro/T-7)" ] || fail "pushed with a spec gap: $1"
}
gap "no 'Problem:' line" '/^Problem:/d'
gap "no 'Taste calls:' line" '/^Taste calls:/d'
gap "two or more lines" '/^B: /d'
gap "is not one of the directions" 's/^Pick: B/Pick: C/'
gap "want 'Pick: <letter>" 's/^Pick: B.*/Pick: b/'
gap "'2.' names no width" 's/^2\. At 1280px/2. On desktop/'
gap "want numbered lines" '/^[0-9]\. /d'
gap "no sheet" '' 'sheet-*.png'
gap "no b-\*.png shots" '' 'b-*.png'

# A ticket that isn't ours to hand up.
nope() { # <want stop substring>
	: >"$FX/log"
	if run T-7 "$run_dir" --holder "$H" --token 3; then fail "handed up: $1"; else rc=$?; [ $rc = 3 ] || fail "exit $rc for: $1"; fi
	grep -q "^stop=.*$1" "$tmp/out" || fail "no stop line for: $1"
	! writes || fail "wrote for: $1"
}
ticket in_progress "$H" "Pricing page"; nope "not a Design: ticket"
ticket in_progress "$H" "Design: pricing page" ship; nope "not a scout ticket"
ticket blocked ""; nope "is blocked, not in_progress"
ticket in_progress designer/other; nope "not claimed by $H"
[ -z "$(git ls-remote --heads origin maestro/T-7)" ] || fail "pushed for a ticket not ours"

# The first hand-up.
ticket in_progress "$H"
: >"$FX/log"
run T-7 "$run_dir" --holder "$H" --token 3 || fail "the hand-up failed"
has "status=blocked"
has "ok=1"
has "pr=https://github.com/o/r/pull/9"
sha=$(sed -n 's/^sha=//p' "$tmp/out")
[ "$(git ls-remote --heads origin maestro/T-7 | cut -f1)" = "$sha" ] || fail "maestro/T-7 isn't at $sha"
files=$(git ls-tree -r --name-only "$sha" -- .maestro/design/T-7 | tr '\n' ' ')
[ "$files" = ".maestro/design/T-7/b-1280.png .maestro/design/T-7/b-375.png .maestro/design/T-7/sheet-1280.png .maestro/design/T-7/sheet-375.png .maestro/design/T-7/spec.md " ] ||
	fail "branch files: $files"
git log -1 --format=%s "$sha" | grep -qF "[skip ci]" || fail "the commit doesn't skip CI"
git merge-base --is-ancestor "$(git rev-parse origin/trunk)" "$sha" || fail "the branch didn't start from trunk"
grep -qE "^gh pr create --draft --base trunk --head maestro/T-7 --title Design: pricing page --body-file " "$FX/log" || fail "no draft PR on trunk: $(grep 'pr create' "$FX/log")"
grep -qF "![sheet-375](https://github.com/o/r/blob/$sha/.maestro/design/T-7/sheet-375.png?raw=true)" "$FX/body" || fail "the PR body doesn't show the sheet"
grep -qF "![b-1280](https://github.com/o/r/blob/$sha/.maestro/design/T-7/b-1280.png?raw=true)" "$FX/body" || fail "the PR body doesn't show the pick"
! grep -q "a-375" "$FX/body" || fail "the PR body shows a direction that wasn't picked"
grep -qxF "comment 1 $H-spec-$sha actor=designer" "$FX/log" || fail "spec comment: $(grep comment "$FX/log")"
grep -qxF "comment 2 $H-escalation-$sha actor=designer" "$FX/log" || fail "escalation comment: $(grep comment "$FX/log")"
head -1 "$FX/comment-1" | grep -qxF "Design spec: PR https://github.com/o/r/pull/9 at $sha" || fail "spec comment head"
grep -qxF "Criteria:" "$FX/comment-1" || fail "the spec comment lacks the spec"
e=$FX/comment-2
head -1 "$e" | grep -qxF "Escalation: design ready: PR https://github.com/o/r/pull/9: owner's call" || fail "escalation head: $(head -1 "$e")"
tail -1 "$e" | grep -qxF "Return-role: designer" || fail "escalation tail: $(tail -1 "$e")"
grep -qxF "What: Design: pricing page. 2 directions in code; the Designer picks B: easiest to compare." "$e" || fail "card What: $(grep ^What "$e")"
grep -qxF "Options: A: three cards in a row, Pro raised.; B: a table with one row per feature." "$e" || fail "card Options: $(grep ^Options "$e")"
grep -qxF "Default: B. Taste calls, with the Designer's defaults: Pro gets the brand color; default yes." "$e" || fail "card Default: $(grep ^Default "$e")"
grep -q "^Question: Build direction B? Answer approved: <letter>, or changes: " "$e" || fail "card Question"
grep -qxF "transition T-7 --from in_progress --to blocked --holder $H --token 3 --release --idempotency-key $H-blocked-$sha" "$FX/log" ||
	fail "no block: $(grep transition "$FX/log")"
[ "$(git worktree list | wc -l)" = 1 ] || fail "a worktree was left: $(git worktree list)"

# A PR lookup that answers something other than a URL stops before any
# PR call or ticket write.
ticket in_progress "$H"
echo '[]' >"$FX/open-pr"
: >"$FX/log"
if run T-7 "$run_dir" --holder "$H" --token 3; then fail "used a PR lookup that gave no URL"; else rc=$?; [ $rc = 1 ] || fail "exit $rc on a bad PR lookup"; fi
! grep -qE '^(gh pr|comment|transition)' "$FX/log" || fail "wrote after a bad PR lookup"

# A rework: the owner asked for A. The open PR is updated, not reopened,
# and the branch moves on with A's shots in place of B's.
echo https://github.com/o/r/pull/9 >"$FX/open-pr"
spec "$run_dir" A
: >"$FX/log"; rm -f "$FX/n"
run T-7 "$run_dir" --holder "$H" --token 3 || fail "the rework failed"
sha2=$(sed -n 's/^sha=//p' "$tmp/out")
[ "$sha2" != "$sha" ] || fail "the rework made no commit"
git merge-base --is-ancestor "$sha" "$sha2" || fail "the rework didn't build on the branch"
files=$(git ls-tree -r --name-only "$sha2" -- .maestro/design/T-7 | tr '\n' ' ')
[ "$files" = ".maestro/design/T-7/a-1280.png .maestro/design/T-7/a-375.png .maestro/design/T-7/sheet-1280.png .maestro/design/T-7/sheet-375.png .maestro/design/T-7/spec.md " ] ||
	fail "rework files: $files"
grep -qE "^gh pr edit https://github.com/o/r/pull/9 --body-file " "$FX/log" || fail "the PR wasn't updated"
! grep -q "^gh pr create" "$FX/log" || fail "a second PR was opened"
grep -qxF "comment 2 $H-escalation-$sha2 actor=designer" "$FX/log" || fail "rework escalation key"

# A rerun of the same folder makes no new commit and reuses each key.
: >"$FX/log"; rm -f "$FX/n"
run T-7 "$run_dir" --holder "$H" --token 3 || fail "the rerun failed"
has "sha=$sha2"
grep -qxF "comment 1 $H-spec-$sha2 actor=designer" "$FX/log" || fail "the rerun changed the spec key"

echo "design_pr_test: ok"
