#!/usr/bin/env bash
# QA has posted its verdict. Merge the tested head and check the base.
set -euo pipefail
[[ $# == 4 ]] || { echo 'usage: merge-reviewed.sh pr head base tested_base' >&2; exit 2; }
pr=$1 head=$2 base=$3 tested_base=$4
[[ $pr =~ ^[0-9]+$ && $head =~ ^[a-f0-9]{40}$ && $tested_base =~ ^[a-f0-9]{40}$ && -n $base ]] || exit 2
gh pr view "$pr" --json headRefOid,baseRefName,isCrossRepository,state |
	jq -e --arg head "$head" --arg base "$base" '.baseRefName == $base and .state == "OPEN" and (.isCrossRepository == false) and .headRefOid == $head' >/dev/null
remote=$(git ls-remote --exit-code origin "refs/heads/$base")
[[ ${remote%%$'\t'*} == "$tested_base" ]] || { echo 'base changed since QA pass' >&2; exit 1; }
gh pr merge "$pr" --merge --match-head-commit "$head" >&2
view=$(gh pr view "$pr" --json state,headRefOid,baseRefName,mergeCommit)
if jq -e '.state == "OPEN"' <<<"$view" >/dev/null; then
	gh pr merge "$pr" --disable-auto >&2 || true
	gh pr view "$pr" --json autoMergeRequest -q '"auto-merge off: \(.autoMergeRequest == null)"' >&2
fi
merge=$(jq -er --arg base "$base" --arg head "$head" \
	'select(.state == "MERGED" and .baseRefName == $base and .headRefOid == $head) | .mergeCommit.oid | select(type == "string" and test("^[0-9a-f]{40}$"))' <<<"$view")
echo "$merge"
parent=$(git fetch -q origin "$merge" && git rev-parse --verify "$merge^1") || parent=unknown
if [[ $parent != "$tested_base" ]]; then
	echo "merged $merge onto an untested base $parent" >&2
	exit 4
fi
