#!/usr/bin/env bash
# QA has posted its verdict. Reserve the current ticket criteria before GitHub
# can merge the reviewed head. Keep the reservation on an uncertain response.
set -euo pipefail
[[ $# == 7 ]] || { echo 'usage: merge-reviewed.sh ticket pr head holder token run base' >&2; exit 2; }
id=$1 pr=$2 head=$3 holder=$4 token=$5 run=$6 base=$7
[[ $id =~ ^T-[0-9]+$ && $pr =~ ^[0-9]+$ && $head =~ ^[a-f0-9]{40}$ && $token =~ ^[0-9]+$ && -n $holder && -n $run && -n $base ]] || exit 2
branch=$(maestro-ticket branch "$id")
check_target() {
	gh pr view "$pr" --json headRefName,headRefOid,baseRefName,isCrossRepository,state |
		jq -e --arg branch "$branch" --arg head "$head" --arg base "$base" '.baseRefName == $base and .state == "OPEN" and (.isCrossRepository == false) and .headRefName == $branch and .headRefOid == $head' >/dev/null
}
# Legacy tickets may have a GO from before structured handoffs existed.
# An opted-in ticket must still have a current basis and QA pass at this head.
detail=$(maestro-ticket get "$id" --json)
direct=$(jq -er 'if .direct_qa == true then "yes" else "no" end' <<<"$detail")
check_basis() {
	maestro-ticket qa-basis "$id" --json |
		jq -e --arg head "$head" '.head == $head and .passed == true and (.route == "direct" or .route == "review")' >/dev/null
}
merge_now() {
	gh api --include --method PUT "repos/{owner}/{repo}/pulls/$pr/merge" -f sha="$head" -f merge_method=merge
}
criteria=$(maestro-ticket criteria status "$id")
merge_versioned() {
	common=$(git rev-parse --path-format=absolute --git-common-dir)
	mkdir -p "$common/maestro"
	exec 9>"$common/maestro/merge-${branch//\//_}.lock"
	flock -x 9
	check_target
	if [[ $direct == yes ]]; then check_basis; fi
	proof=$(jq -n --arg head "$head" --arg holder "$holder" --argjson token "$token" \
		--argjson ac "${MAESTRO_AC_REVISION:?missing starting AC revision}" \
		--argjson brief "${MAESTRO_BRIEF_REVISION:?missing starting brief revision}" \
		'{head:$head,holder:$holder,token:$token,ac:$ac,brief:$brief}')
	maestro-ticket criteria merge "$id" --actor qa-lead --idempotency-key "$run-merge" <<<"$proof" >/dev/null

	# The merge endpoint pins the SHA and responds synchronously. A network error
	# leaves the reservation intact. A definite refusal lets QA escalate at once.
	if response=$(merge_now); then
		return 0
	fi
	read -r protocol status _ <<<"$response" || true
	case "$protocol $status" in
		HTTP/*' 403'|HTTP/*' 405'|HTTP/*' 409'|HTTP/*' 422')
			if gh pr view "$pr" --json state,headRefName,isCrossRepository |
				jq -e --arg branch "$branch" '.state == "OPEN" and .headRefName == $branch and .isCrossRepository == false' >/dev/null; then
				maestro-ticket criteria merge-rejected "$id" --actor qa-lead --idempotency-key "$run-rejected" <<<"$proof" >/dev/null
			fi ;;
	esac
	return 1
}
if [[ $criteria == null ]]; then
	check_target
	if [[ $direct == yes ]]; then
		[[ $holder == qa-lead/* ]]
		maestro-ticket heartbeat "$id" --holder "$holder" --token "$token" >/dev/null
		check_basis
		merge_now >/dev/null
	else
		gh pr merge "$pr" --merge --match-head-commit "$head" >&2
	fi
else
	merge_versioned
fi
view=$(gh pr view "$pr" --json state,headRefOid,baseRefName,mergeCommit)
if [[ $criteria == null && $direct == no ]] && jq -e '.state == "OPEN"' <<<"$view" >/dev/null; then
	gh pr merge "$pr" --disable-auto >&2 || true
	gh pr view "$pr" --json autoMergeRequest -q '"auto-merge off: \(.autoMergeRequest == null)"' >&2
fi
jq -er --arg base "$base" --arg head "$head" \
	'select(.state == "MERGED" and .baseRefName == $base and .headRefOid == $head) | .mergeCommit.oid | select(type == "string" and test("^[0-9a-f]{40}$"))' <<<"$view"
