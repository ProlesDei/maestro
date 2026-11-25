#!/usr/bin/env bash
# QA has posted its verdict. Merge the tested head and check the base.
# Exit 0: merged on the tested base, M on stdout; 1: the PR's head, base,
# state or fork changed since QA; 2: usage; 3: the base moved since QA's
# pass; 4: merged onto an untested base, M on stdout; 5: not merged
# (queued, auto-merge or refused), with gh's reason and the auto-merge
# state; 6: gh or git couldn't read the state, so reread the PR. Each but
# 0 and 2 gives its reason in one stderr line.
set -euo pipefail
die() { echo "merge-reviewed: $2" >&2; exit "$1"; }
[[ $# == 4 ]] || die 2 'usage: merge-reviewed.sh pr head base tested_base'
pr=$1 head=$2 base=$3 tested_base=$4
[[ $pr =~ ^[0-9]+$ && $head =~ ^[a-f0-9]{40}$ && $tested_base =~ ^[a-f0-9]{40}$ && -n $base ]] || die 2 'bad argument'
# changed <state>: what differs from what QA tested, comma apart.
changed() {
	jq -r --arg head "$head" --arg base "$base" --arg state "$1" '[
		if .headRefOid != $head then "head \(.headRefOid)" else empty end,
		if .baseRefName != $base then "base \(.baseRefName)" else empty end,
		if .state != $state then "state \(.state)" else empty end,
		if .isCrossRepository then "fork" else empty end] | join(", ")' <<<"$view"
}
fields=headRefOid,baseRefName,isCrossRepository,state,mergeCommit,autoMergeRequest
view=$(gh pr view "$pr" --json "$fields") || die 6 "not merged: gh couldn't read PR $pr"
why=$(changed OPEN)
[[ -z $why ]] || die 1 "PR changed since QA: $why"
remote=$(git ls-remote origin "refs/heads/$base") || die 6 "not merged: git ls-remote origin failed"
remote=${remote%%$'\t'*}
[[ $remote == "$tested_base" ]] || die 3 "base $base moved since QA pass: ${remote:-gone}"
out=$(gh pr merge "$pr" --merge --match-head-commit "$head" 2>&1) || true
out=${out//$'\n'/ }
view=$(gh pr view "$pr" --json "$fields") || die 6 "merge state unknown: gh couldn't read PR $pr: $out"
if [[ $(jq -r .state <<<"$view") != MERGED ]]; then
	why=$(changed OPEN)
	[[ -z $why ]] || die 1 "PR changed since QA: $why: $out"
	# A queued or auto-merge PR would merge a later push no one tested.
	gh pr merge "$pr" --disable-auto >/dev/null 2>&1 || true
	view=$(gh pr view "$pr" --json "$fields") || view='{"autoMergeRequest":"unknown"}'
	die 5 "not merged: ${out:-gh said nothing}; auto-merge off: $(jq '.autoMergeRequest == null' <<<"$view")"
fi
why=$(changed MERGED)
[[ -z $why ]] || die 1 "PR changed since QA: $why"
merge=$(jq -r '.mergeCommit.oid // ""' <<<"$view")
[[ $merge =~ ^[0-9a-f]{40}$ ]] || die 6 "merge state unknown: PR $pr merged with no merge commit"
echo "$merge"
parent=$(git fetch -q origin "$merge" && git rev-parse --verify "$merge^1") || parent=unknown
[[ $parent != unknown ]] || die 6 "merged $merge; couldn't read its parent"
[[ $parent == "$tested_base" ]] || die 4 "merged $merge onto an untested base $parent"
