#!/usr/bin/env bash
# design-pr.sh: hands a finished design up for the owner's look, in one
# call, so a session spends no calls or tokens on the fixed steps and
# writes no card by hand.
#
#   design-pr.sh <ticket-id> <run folder> --title <ticket title>
#     Checks the spec (spec.md in the run folder) has every field, two or
#     more directions, a pick among them, numbered criteria that each
#     name a width in px, and, unless its Motion: is just none, a
#     clip of the pick (<pick>[+<cue>]-<width>-motion.png, from
#     screens.sh --motion);
#     on a gap it prints problem= lines, exit 2, and writes nothing.
#     With Motion: none, no clip goes up.
#     Checks the title opens "Design:"; else stop=<why>, exit 3.
#     Then commits spec.md, the sheets (sheet-*.png), the pick's shots
#     (<pick>-*.png, the pick's letter in lowercase) and every clip
#     of a direction (<letter>[-+]*-motion.png, the pick's first), but
#     no strip (*-strip.png), to
#     maestro-design/<ticket-id>/ on branch maestro/<ticket-id>, even where
#     the repo ignores them, pushed with [skip ci] (Git LFS objects first,
#     where the repo tracks them with LFS); opens a draft PR for it, or
#     updates the open one; and writes the "Design spec:" note and the
#     owner's card, "Escalation: design ready: PR <url>: owner's call",
#     to design-spec.txt and card.txt in the run folder, for the session
#     to post on the ticket. Prints pr=, sha=, spec=, card=, ok=1.
#
# Run it inside the repo, any worktree. Needs git and gh. Exit 1: any
# other failure; a rerun with the same run folder pushes no new commit
# and opens no second PR.
set -euo pipefail
unset CDPATH

die() { echo "design-pr: $2" >&2; exit "$1"; }
((BASH_VERSINFO[0] * 100 + BASH_VERSINFO[1] >= 404)) || die 1 "needs bash 4.4 or later (macOS: brew install bash)"

[ $# -ge 2 ] || die 2 "usage: design-pr.sh <ticket-id> <run folder> --title <ticket title>"
id=$1 dir=$2
shift 2
title=''
while [ $# -gt 0 ]; do
	case $1 in
	--title) [ $# -ge 2 ] || die 2 "--title needs a value"; title=$2; shift 2 ;;
	*) die 2 "unknown argument $1" ;;
	esac
done
[[ $id =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || die 2 "the ticket id must be letters, digits, '.', '_' or '-', not $id"
[ -n "$title" ] || die 2 "--title is required"
[ -f "$dir/spec.md" ] || die 2 "no spec.md in $dir"
dir=$(cd "$dir" && pwd)
spec=$dir/spec.md

# The spec. Each field opens a line; a block runs to the next field.
probs=()
field() { grep -qE "^$1:" "$spec" || probs+=("no '$1:' line"); }
for f in Problem Breadboard Directions Pick States Motion Reuses Criteria "Taste calls"; do field "$f"; done
block() { # the lines after "<field>:" up to the next field
	awk -v f="$1" '{sub(/\r$/, "")}
		/^(Problem|Breadboard|Directions|Pick|States|Motion|Reuses|Criteria|Taste calls):/ {on=(index($0, f ":") == 1); next}
		on' "$spec"
}
one() { block "$1" | tr '\n' ' ' | sed 's/  */ /g; s/^ //; s/ $//'; }
value() { # the text after "<field>:" on its line, else the block under it, as one line
	local v
	v=$(awk -v f="$1" '{sub(/\r$/, "")} index($0, f ":") == 1 {print substr($0, length(f) + 2); exit}' "$spec" | sed 's/^ *//; s/ *$//')
	[ -n "$v" ] || v=$(one "$1")
	printf '%s' "$v"
}
dirs=$(block Directions | grep -E '^[A-Z]: ' | cut -c1 | sort -u || true)
[ "$(printf '%s' "$dirs" | grep -c . || true)" -ge 2 ] || probs+=("Directions: want two or more lines 'A: <one line>', each its own letter")
pickline=$(awk '{sub(/\r$/, "")} /^Pick: [A-Z]([^A-Za-z0-9]|$)/ {print; exit}' "$spec")
pick=${pickline:6:1}
if [ -z "$pick" ]; then
	probs+=("Pick: want 'Pick: <letter>: <why>'")
elif ! grep -qx "$pick" <<<"$dirs"; then
	probs+=("Pick: $pick is not one of the directions")
fi
crit=$(block Criteria | grep -E '^[0-9]+\. ' || true)
[ -n "$crit" ] || probs+=("Criteria: want numbered lines '1. <what QA sees> at <width>px'")
while IFS= read -r c; do
	[ -z "$c" ] || grep -qiE '(^|[^a-z])at [0-9]+px' <<<"$c" || probs+=("Criteria: '${c%% *}' names no width as 'at <n>px'")
done <<<"$crit"
ls "$dir"/sheet-*.png >/dev/null 2>&1 || probs+=("no sheet-*.png in the run folder")
# The pick's shots and every direction's clips, the pick's first: a clip
# of a page that is no direction (a probe, a dropped C) stays out. A
# strip is for the session that made it, not the owner.
lp=${pick,,}
stills=() clips=()
if [ -n "$pick" ]; then
	shopt -s nullglob
	for f in "$dir/$lp"-*.png; do
		case $f in *-motion.png | *-strip.png) ;; *) stills+=("$f") ;; esac
	done
	# A clip is <page>[+<cue>]-<width>-motion.png, and a direction's
	# pages are <letter>.html or <letter>-<place>.html.
	clips=("$dir/$lp"[-+]*-motion.png)
	n_pick=${#clips[@]}
	for f in "$dir"/*-motion.png; do
		b=${f##*/}
		[[ ${b:1:1} == [-+] ]] && [ "${b:0:1}" != "$lp" ] && grep -qx "${b:0:1}" <<<"${dirs,,}" && clips+=("$f")
	done
	shopt -u nullglob
	[ ${#stills[@]} -gt 0 ] || probs+=("no $lp-*.png shots of the pick in the run folder")
	motion=$(value Motion)
	if [[ ${motion,,} =~ ^none[[:space:][:punct:]]*$ ]]; then
		# A spec with no motion shows none: clips from exploring would
		# show the owner motion the design dropped.
		clips=()
	elif [ "$n_pick" = 0 ]; then
		screens=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/screens.sh
		probs+=("Motion: no clip of the pick (${lp}[+<cue>]-<width>-motion.png) in the run folder; make one with $screens --motion, or write 'Motion: none'")
	fi
fi
if [ ${#probs[@]} -gt 0 ]; then
	printf 'problem=%s\n' "${probs[@]}"
	exit 2
fi

stop() { echo "stop=$1"; exit 3; }
[[ $title == Design:* ]] || stop "$id is not a Design: ticket"

# The repo's web URL and default branch, main when none is set.
meta=$(gh repo view --json url,defaultBranchRef --jq '.url + " " + .defaultBranchRef.name') || die 1 "gh can't read this repo"
read -r web main <<<"$meta"
[[ $web =~ ^https://[^[:space:]]+$ ]] || die 1 "gh gave no repo URL: $web"
main=${main:-main}
branch=maestro/$id

tmp=$(mktemp -d)
wt=
cleanup() {
	[ -z "$wt" ] || git worktree remove --force "$wt" >/dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT

retry() {
	local i
	for i in 1 2 3; do
		"$@" && return 0
		[ "$i" = 3 ] || sleep $((RANDOM % 5 + 1))
	done
	return 1
}
retry git fetch -q origin "$main" || die 1 "can't fetch $main"
start=refs/remotes/origin/$main
if git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
	retry git fetch -q origin "+refs/heads/$branch:refs/remotes/origin/$branch" || die 1 "can't fetch $branch"
	start=refs/remotes/origin/$branch
fi
# The team's hooks expect their dependencies, which this worktree lacks,
# and LFS files aren't needed for a commit of images and a spec: run no
# hook and fetch no LFS object on the way.
nohook() { GIT_LFS_SKIP_SMUDGE=1 git -c core.hooksPath=/dev/null "$@"; }
wt=$tmp/wt
nohook worktree add -q --detach "$wt" "$start" || die 1 "can't add a worktree at $start"
d=maestro-design/$id
rm -rf "${wt:?}/$d"
mkdir -p "$wt/$d"
cp "$spec" "$dir"/sheet-*.png "${stills[@]}" "${clips[@]}" "$wt/$d/"
# The folder is Maestro's: a .gitignore or global exclude that ignores
# *.png must not drop the shots.
git -C "$wt" add -A -f -- "$d" || die 1 "git add of $d failed"
if ! git -C "$wt" diff --cached --quiet; then
	printf 'Design %s for the owner'"'"'s look [skip ci]\n\nShots and spec only; this branch never merges.\n' "$id" >"$tmp/msg"
	nohook -C "$wt" commit -q --no-verify -F "$tmp/msg" || die 1 "commit failed"
fi
sha=$(git -C "$wt" rev-parse HEAD)
want=$(cd "$wt/$d" && LC_ALL=C ls -A)
got=$(git -C "$wt" ls-tree --name-only "HEAD:$d")
[ "$want" = "$got" ] || die 1 "the commit at $sha lacks some of $d: $(LC_ALL=C comm -23 <(echo "$want") <(echo "$got") | tr '\n' ' ')"
# With hooks off, LFS's pre-push hook can't upload the shots a team
# tracks with LFS, and the host refuses a push that names objects it
# lacks: upload them as that hook would.
attrs=$(git -C "$wt" ls-tree -r --name-only HEAD -- "$d" | git -C "$wt" check-attr --stdin filter) || die 1 "git check-attr failed"
if command -v git-lfs >/dev/null && grep -q ': filter: lfs$' <<<"$attrs"; then
	retry git -C "$wt" lfs push origin "$sha" ||
		die 1 "can't upload the shots in $d to origin's Git LFS store, which this repo's .gitattributes sends them to"
fi
retry nohook -C "$wt" push -q --no-verify origin "HEAD:refs/heads/$branch" || die 1 "push to $branch failed"

# The draft PR, from this repo only: a fork's PR on the same branch
# name is never ours.
raw() { echo "$web/blob/$sha/$d/$1?raw=true"; }
{
	echo "Design for $id, for the owner's look. This PR never merges: the owner answers in chat, and the planner closes it."
	echo
	show() {
		local f b
		for f in "$@"; do
			b=$(basename "$f")
			echo "**${b%.png}**"
			echo
			echo "![${b%.png}]($(raw "$b"))"
			echo
		done
	}
	show "$dir"/sheet-*.png "${stills[@]}"
	if [ ${#clips[@]} -gt 0 ]; then
		echo "Motion: each clip loops."
		echo
		show "${clips[@]}"
	fi
	echo '```text'
	# shellcheck disable=SC2016 # literal backticks
	sed 's/`\{3,\}/` ` `/g' "$spec"
	echo '```'
} >"$tmp/body"
pr=$(gh api "repos/{owner}/{repo}/pulls?head={owner}:$branch&state=open&per_page=1" --jq '.[0].html_url // empty') || die 1 "gh can't list PRs"
[ -z "$pr" ] || [[ $pr =~ ^https://[^[:space:]]+$ ]] || die 1 "gh listed no PR URL: $pr"
if [ -n "$pr" ]; then
	gh pr edit "$pr" --body-file "$tmp/body" >/dev/null || die 1 "can't update $pr"
else
	if ! pr=$(gh pr create --draft --base "$main" --head "$branch" --title "$title" --body-file "$tmp/body" 2>"$tmp/err"); then
		grep -qi 'draft' "$tmp/err" || die 1 "can't open the PR: $(head -c 300 "$tmp/err")"
		pr=$(gh pr create --base "$main" --head "$branch" --title "$title (do not merge)" --body-file "$tmp/body") ||
			die 1 "can't open the PR"
	fi
	pr=$(tail -n1 <<<"$pr")
fi
[[ $pr =~ ^https:// ]] || die 1 "gh gave no PR URL: $pr"

# The card, from the spec.
why=${pickline:7}
why=${why#:}
why=${why# }
why=${why%.}
taste=$(value "Taste calls")
n=$(printf '%s\n' "$dirs" | grep -c .)
{
	echo "Escalation: design ready: PR $pr: owner's call"
	echo "What: $title. $n directions in code; the Designer picks $pick${why:+: $why}."
	echo "Why now: the goal's screen tickets wait on this answer; its other tickets are already moving."
	echo "Options:"
	block Directions | grep -E '^[A-Z]: ' | sed 's/^/- /'
	if [ -n "$taste" ]; then
		echo "Default: $pick. Taste calls, with the Designer's defaults: $taste"
	else
		echo "Default: $pick."
	fi
	echo "Question: Build direction $pick? Answer approved: $pick, which takes the defaults above, or changes: <what to change, or another direction's letter>. Shots: $pr"
	echo "Return-role: designer"
} >"$dir/card.txt"
{
	echo "Design spec: PR $pr at $sha"
	echo
	cat "$spec"
} >"$dir/design-spec.txt"

echo "pr=$pr"
echo "sha=$sha"
echo "spec=$dir/design-spec.txt"
echo "card=$dir/card.txt"
echo "ok=1"
