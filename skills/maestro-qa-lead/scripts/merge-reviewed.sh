#!/usr/bin/env bash
# QA has posted its verdict. Reserve the current ticket criteria before GitHub
# can merge the reviewed head. Keep the reservation on an uncertain response.
set -euo pipefail
[[ $# == 6 ]] || { echo 'usage: merge-reviewed.sh ticket pr head holder token run' >&2; exit 2; }
id=$1 pr=$2 head=$3 holder=$4 token=$5 run=$6
[[ $id =~ ^T-[0-9]+$ && $pr =~ ^[0-9]+$ && $head =~ ^[a-f0-9]{40}$ && $token =~ ^[0-9]+$ && -n $holder && -n $run ]] || exit 2
branch=$(maestro-ticket branch "$id")
gh pr view "$pr" --json headRefName,headRefOid,isCrossRepository,state |
	jq -e --arg branch "$branch" --arg head "$head" '.state == "OPEN" and (.isCrossRepository == false) and .headRefName == $branch and .headRefOid == $head' >/dev/null
criteria=$(maestro-ticket criteria status "$id")
if [[ $criteria == null ]]; then
	exec gh pr merge "$pr" --merge --match-head-commit "$head"
fi
common=$(git rev-parse --path-format=absolute --git-common-dir)
mkdir -p "$common/maestro"
exec 9>"$common/maestro/merge-${branch//\//_}.lock"
flock -x 9
proof=$(jq -n --arg head "$head" --arg holder "$holder" --argjson token "$token" \
	--argjson ac "${MAESTRO_AC_REVISION:?missing starting AC revision}" \
	--argjson brief "${MAESTRO_BRIEF_REVISION:?missing starting brief revision}" \
	'{head:$head,holder:$holder,token:$token,ac:$ac,brief:$brief}')
maestro-ticket criteria merge "$id" --actor qa-lead --idempotency-key "$run-merge" <<<"$proof" >/dev/null

# The merge endpoint pins the SHA and responds synchronously. A network error
# leaves the reservation intact. A definite refusal lets QA escalate at once.
if response=$(gh api --include --method PUT "repos/{owner}/{repo}/pulls/$pr/merge" -f sha="$head" -f merge_method=merge); then
	exit 0
fi
read -r protocol status _ <<<"$response" || true
case "$protocol $status" in
	HTTP/*' 405'|HTTP/*' 409'|HTTP/*' 422')
		if gh pr view "$pr" --json state,headRefName,isCrossRepository |
			jq -e --arg branch "$branch" '.state == "OPEN" and .headRefName == $branch and .isCrossRepository == false' >/dev/null; then
			maestro-ticket criteria merge-rejected "$id" --actor qa-lead --idempotency-key "$run-rejected" <<<"$proof" >/dev/null
		fi ;;
esac
exit 1
