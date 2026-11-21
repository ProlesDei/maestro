#!/usr/bin/env bash
# Emit the MongoDB collection privileges of Maestro's one custom role for
# one database: what maestro-ticket needs for the board, the chief of
# staff's collections included.
set -euo pipefail
[[ $# == 1 && $1 =~ ^[A-Za-z0-9_-]+$ && $1 != admin && $1 != local && $1 != config ]] || { echo 'usage: runtime-roles.sh database' >&2; exit 2; }
jq -n --arg db "$1" 'def p($c;$a): {resource:{db:$db,collection:$c},actions:$a};
	[p("";["listCollections"]),p("events";["find","insert","createCollection","createIndex","listIndexes"]),
	 p("goal_revisions";["find","insert","update"]),p("planner_deliveries";["find","insert","update","createIndex"])] +
	(["meta","tickets","comments","idempotency","runs","launches","etas","marks"] |
	map(p(.;["find","insert","update","remove","createCollection","createIndex","listIndexes"]))) +
	(["goal_merges","goal_inbox"] | map(p(.;["find","update","insert","createIndex"]))) +
	(["chief_sessions","chief_drafts","goal_briefs","goal_decisions","owner_rules"] |
	map(p(.;["find","insert","update","createIndex"])))'
