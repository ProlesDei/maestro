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
if [[ $criteria != null ]]; then
	jq -n --arg head "$head" --arg holder "$holder" --argjson token "$token" \
		--argjson ac "${MAESTRO_AC_REVISION:?missing starting AC revision}" \
		--argjson brief "${MAESTRO_BRIEF_REVISION:?missing starting brief revision}" \
		'{head:$head,holder:$holder,token:$token,ac:$ac,brief:$brief}' |
		maestro-ticket criteria merge "$id" --actor qa-lead --idempotency-key "$run-merge" >/dev/null
fi
gh pr merge "$pr" --merge --match-head-commit "$head"
