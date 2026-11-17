#!/usr/bin/env bash
# design-pr.sh: hands a finished design up for the owner's look, in one
# call, so a session spends no calls or tokens on the fixed steps and the
# bots forward a card instead of writing one.
#
#   design-pr.sh <T-n> <run folder> --holder <holder> --token <n>
#     Checks the spec (spec.md in the run folder) has every field, two or
#     more directions, a pick among them, numbered criteria that each
#     name a width in px, and, unless its Motion: is just none, a
#     clip of the pick (<pick>-*-motion.png, from screens.sh --motion);
#     on a gap it prints problem= lines, exit 2, and writes nothing.
#     With Motion: none, no clip goes up.
#     Checks the ticket is a "Design:" scout ticket in in_progress under
#     this holder's claim; else stop=<why>, exit 3.
#     Then commits spec.md, the sheets (sheet-*.png), the pick's shots
#     (<pick>-*.png, the pick's letter in lowercase) and every clip
#     of a direction (<letter>-*-motion.png, the pick's first), but no
#     strip (*-strip.png), to
#     .maestro/design/<T-n>/ on branch maestro/<T-n>, pushed with
#     [skip ci]; opens a draft PR for it, or updates the open one; posts
#     "Design spec:" and then "Escalation: design ready: PR <url>: owner's
#     call", holding the owner's card, on the ticket; and blocks the
#     ticket, releasing the claim. Prints pr=, sha=, status=blocked, ok=1.
#
# Run it inside the repo, any worktree. Needs git, gh, jq and
# maestro-ticket. Exit 1: any other failure; a rerun with the same run
# folder repeats no comment and no move.
set -euo pipefail

die() { echo "design-pr: $2" >&2; exit "$1"; }

[ $# -ge 2 ] || die 2 "usage: design-pr.sh <T-n> <run folder> --holder <holder> --token <n>"
id=$1 dir=$2
shift 2
holder='' token=''
while [ $# -gt 0 ]; do
	case $1 in
	--holder) [ $# -ge 2 ] || die 2 "--holder needs a value"; holder=$2; shift 2 ;;
	--token) [ $# -ge 2 ] || die 2 "--token needs a value"; token=$2; shift 2 ;;
	*) die 2 "unknown argument $1" ;;
	esac
done
[[ $id =~ ^T-[0-9]+$ ]] || die 2 "the ticket must be T-<n>, not $id"
[[ $holder =~ ^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._:-]*$ ]] || die 2 "--holder must be <bot-id>/<session-id>"
[[ $token =~ ^[0-9]+$ ]] || die 2 "--token must be a number"
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
	clips=("$dir/$lp"-*-motion.png)
	n_pick=${#clips[@]}
	for f in "$dir"/*-motion.png; do
		b=${f##*/}
		[ "${b:1:1}" = - ] && [ "${b:0:1}" != "$lp" ] && grep -qx "${b:0:1}" <<<"${dirs,,}" && clips+=("$f")
	done
	shopt -u nullglob
	[ ${#stills[@]} -gt 0 ] || probs+=("no $lp-*.png shots of the pick in the run folder")
	motion=$(value Motion)
	if [[ ${motion,,} =~ ^none[[:space:][:punct:]]*$ ]]; then
		# A spec with no motion shows none: clips from exploring would
		# show the owner motion the design dropped.
		clips=()
	elif [ "$n_pick" = 0 ]; then
		probs+=("Motion: no $lp-*-motion.png clip of the pick in the run folder; make one with screens.sh --motion, or write 'Motion: none'")
	fi
fi
if [ ${#probs[@]} -gt 0 ]; then
	printf 'problem=%s\n' "${probs[@]}"
	exit 2
fi

# The ticket: a Design: scout ticket we hold in in_progress.
t=$(maestro-ticket get "$id" --json) || die 1 "maestro-ticket get $id failed"
stop() { echo "stop=$1"; exit 3; }
title=$(jq -r '.title' <<<"$t")
[[ $title == Design:* ]] || stop "$id is not a Design: ticket"
[ "$(jq -r '.type' <<<"$t")" = scout ] || stop "$id is not a scout ticket"
[ "$(jq -r '.status' <<<"$t")" = in_progress ] || stop "$id is $(jq -r '.status' <<<"$t"), not in_progress"
[ "$(jq -r '.claim.holder // empty' <<<"$t")" = "$holder" ] || stop "$id is not claimed by $holder"

# The main branch, as review-prep.sh reads it; main when none is set.
wts=$(git worktree list --porcelain)
mainwt=$(sed -n '1s/^worktree //p' <<<"$wts")
if [ "$(sed -n 2p <<<"$wts")" = bare ]; then
	while IFS= read -r w; do
		[ -d "$w/.maestro/src" ] && { mainwt=$w; break; }
	done < <(sed -n 's/^worktree //p' <<<"$wts")
fi
setup=$mainwt/.maestro/project/project-setup.md
main=
[ -f "$setup" ] && main=$(awk '{sub(/\r$/, ""); gsub(/^[ \t]+|[ \t]+$/, "")}
	/^## / {h=substr($0, 4); gsub(/^[ \t]+|[ \t]+$/, "", h); on=(tolower(h)=="main branch"); next}
	on && $0 != "" && !/^#/ {sub(/^[-* ]+/, ""); gsub(/^[`* ]+|[`* ]+$/, ""); print; exit}' "$setup")
main=${main:-main}
branch=maestro/$id

tmp=$(mktemp -d)
wt=
cleanup() {
	[ -z "$wt" ] || git worktree remove --force "$wt" >/dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT

# Many sessions share this repo: a lock or ref error gets a random pause
# and two more tries, as the working principles ask.
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
d=.maestro/design/$id
rm -rf "${wt:?}/$d"
mkdir -p "$wt/$d"
cp "$spec" "$dir"/sheet-*.png "${stills[@]}" "${clips[@]}" "$wt/$d/"
git -C "$wt" add -A -- "$d"
if ! git -C "$wt" diff --cached --quiet; then
	printf 'Design %s for the owner'"'"'s look [skip ci]\n\nShots and spec only; this branch never merges.\n' "$id" >"$tmp/msg"
	nohook -C "$wt" commit -q --no-verify -F "$tmp/msg" || die 1 "commit failed"
fi
sha=$(git -C "$wt" rev-parse HEAD)
retry nohook -C "$wt" push -q --no-verify origin "HEAD:refs/heads/$branch" || die 1 "push to $branch failed"

# The draft PR, from this repo only: a fork's PR on the same branch
# name is never ours.
web=$(gh repo view --json url --jq .url) || die 1 "gh can't read this repo"
[[ $web =~ ^https://[^[:space:]]+$ ]] || die 1 "gh gave no repo URL: $web"
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
	# A private repo on a plan with no draft PRs refuses --draft: open a
	# plain one, titled so no person merges it. No bot would: the design
	# ticket never reaches review or QA.
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
} >"$tmp/esc"
{
	echo "Design spec: PR $pr at $sha"
	echo
	cat "$spec"
} >"$tmp/spec"

mt() { MAESTRO_TICKET_ACTOR=${MAESTRO_TICKET_ACTOR:-${holder%%/*}} maestro-ticket "$@"; }
mt comment "$id" --body-file "$tmp/spec" --holder "$holder" --token "$token" --idempotency-key "$holder-spec-$sha" >/dev/null ||
	die 1 "can't post the spec on $id"
mt comment "$id" --body-file "$tmp/esc" --holder "$holder" --token "$token" --idempotency-key "$holder-escalation-$sha" >/dev/null ||
	die 1 "can't post the escalation on $id"
mt transition "$id" --from in_progress --to blocked --holder "$holder" --token "$token" --release \
	--idempotency-key "$holder-blocked-$sha" >/dev/null || die 1 "can't block $id"

echo "pr=$pr"
echo "sha=$sha"
echo "status=blocked"
echo "ok=1"
