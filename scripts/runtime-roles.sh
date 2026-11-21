#!/usr/bin/env bash
# Emit MongoDB collection privileges for one database and one runtime identity.
set -euo pipefail
[[ $# == 2 && $1 =~ ^[A-Za-z0-9_-]+$ && $1 != admin && $1 != local && $1 != config ]] || { echo 'usage: runtime-roles.sh database worker|chief' >&2; exit 2; }
case $2 in
worker)
	jq -n --arg db "$1" 'def p($c;$a): {resource:{db:$db,collection:$c},actions:$a};
		[p("";["listCollections"]),p("events";["find","insert","createCollection","createIndex","listIndexes"]),p("goal_revisions";["find"]),p("planner_deliveries";["find","update","createIndex"])] +
		(["meta","tickets","comments","idempotency","runs","launches","etas","marks"] |
		map(p(.;["find","insert","update","remove","createCollection","createIndex","listIndexes"]))) +
		(["goal_merges","goal_inbox"] | map(p(.;["find","update","insert","createIndex"])))'
	;;
chief)
	jq -n --arg db "$1" 'def p($c;$a): {resource:{db:$db,collection:$c},actions:$a};
		[p("meta";["find"]),p("goal_revisions";["find","insert","update"]),
		 p("goal_merges";["find","insert","update"]),p("planner_deliveries";["find","insert"]),
		 p("goal_inbox";["find","update"])] +
		(["chief_sessions","chief_drafts","goal_briefs","goal_decisions","owner_rules"] |
		map(p(.;["find","insert","update","createIndex"])))'
	;;
*) exit 2 ;;
esac
