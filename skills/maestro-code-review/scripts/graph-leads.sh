#!/usr/bin/env bash
# graph-leads.sh: what a code graph says about one change, for review and
# for an engineer's self-review, so neither spends reads finding it.
#
#   graph-leads.sh <worktree> <base sha> <out file>
#     Indexes the worktree's checkout with codebase-memory-mcp under a
#     name no other run uses, maps the change from <base> to the
#     worktree's HEAD onto the functions it touches, writes <out> and
#     deletes the index, whatever happens. <out> lists, each capped:
#     each changed function with its callers; each new function nothing
#     calls; each new function that shares most of its rare words with
#     one already there (a look-alike: a helper the change may have
#     copied); and each changed function over COMPLEX in cyclomatic
#     complexity. Every item is a lead to read, never a finding: the
#     graph misses calls made by name and can join two functions that
#     share one.
#     Prints graph=yes and the counts; graph=none: <why> when
#     codebase-memory-mcp isn't on PATH, writing nothing. Exit 0 then;
#     1 on any failure, with the reason on stderr.
#
# Needs bash 4.4+, git and jq.

set -euo pipefail
die() { echo "graph-leads: $1" >&2; exit 1; }

[ $# -eq 3 ] || die "usage: graph-leads.sh <worktree> <base sha> <out file>"
wt=$1 base=$2 out=$3
COMPLEX=15  # cyclomatic, as the graph counts it
CAP=30      # lines per list

command -v codebase-memory-mcp >/dev/null || { echo "graph=none: no codebase-memory-mcp on PATH"; exit 0; }
command -v jq >/dev/null || die "needs jq"
git -C "$wt" cat-file -e "$base^{commit}" 2>/dev/null || die "$base is no commit in $wt"

cli() { codebase-memory-mcp cli "$@" 2>/dev/null; }
# query <cypher>: the rows, as one JSON array.
query() {
	jq -cn --arg p "$project" --arg q "$1" '{project: $p, query: $q}' | cli query_graph | tail -n 1 |
		jq -ce '.rows // error("no rows")' || die "graph query failed: ${1:0:80}"
}

# Six letters from the OS name the index, so runs started in one second
# never share one; it is deleted on any exit.
abc=abcdefghijklmnopqrstuvwxyz rand=""
for b in $(od -An -N6 -tu1 /dev/urandom); do rand+=${abc:b % 26:1}; done
[ ${#rand} -eq 6 ] || die "no 6 random bytes from /dev/urandom"
project=""
tmp=$(mktemp -d)
cleanup() {
	[ -z "$project" ] || cli delete_project --project "$project" >/dev/null || true
	rm -rf -- "$tmp"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# full: the other modes skip folders such as scripts/ as if they held no code.
project=$(cli index_repository --repo-path "$(cd "$wt" && pwd)" --mode full --name "maestro-leads-$rand" |
	tail -n 1 | jq -re 'select(.status == "indexed") | .project') || { project=maestro-leads-$rand; die "indexing $wt failed"; }

# The new side of each hunk: path, first line, last line. A hunk that
# only deletes touches the line before it.
git -C "$wt" -c core.quotePath=false diff -U0 --no-renames --no-color "$base" HEAD |
	awk '/^\+\+\+ /{f = substr($0, 5); sub(/^b\//, "", f); if (f == "/dev/null") f = ""; next}
		f != "" && /^@@ /{split($3, a, ","); s = substr(a[1], 2) + 0; n = (2 in a) ? a[2] + 0 : 1
			if (n == 0) { if (s > 0) print f "\t" s "\t" s } else print f "\t" s "\t" s + n - 1}' >"$tmp/hunks.tsv"

query 'MATCH (f) WHERE f:Function OR f:Method RETURN f.qualified_name, f.file_path, f.name, f.start_line, f.end_line, f.complexity, f.is_test, f.is_entry_point, f.bt' |
	jq -c 'map({q: .[0], p: .[1], n: .[2], s: (.[3] | tonumber? // 0), e: (.[4] | tonumber? // 0), c: (.[5] | tonumber? // 0),
		test: (.[6] == "true" or .[6] == true), entry: (.[7] == "true" or .[7] == true),
		t: ((.[8] // "") | split(" ") | map(select(length > 2)) | unique)})' >"$tmp/fns.json"

# The functions a hunk overlaps; new when the base's copy of the file
# never says the name.
jq -c --rawfile h "$tmp/hunks.tsv" '
	($h | split("\n") | map(select(length > 0) | split("\t") | {p: .[0], s: (.[1] | tonumber), e: (.[2] | tonumber)})) as $hs
	| [.[] | select(.test | not) | . as $f | select(any($hs[]; .p == $f.p and .s <= $f.e and .e >= $f.s))]' \
	"$tmp/fns.json" >"$tmp/changed.json"
: >"$tmp/new.txt"
while IFS=$'\t' read -r q p n; do
	if ! git -C "$wt" show "$base:$p" 2>/dev/null | grep -qwF -- "$n"; then
		printf '%s\n' "$q" >>"$tmp/new.txt"
	fi
done < <(jq -r '.[] | [.q, .p, .n] | @tsv' "$tmp/changed.json")

qns=$(jq -c '[.[].q]' "$tmp/changed.json")
callers='[]' counts='[]'
if [ "$qns" != '[]' ]; then
	# USAGE too: a call through `from m import f` is one in this graph.
	callers=$(query "MATCH (c)-[:CALLS|USAGE]->(f) WHERE f.qualified_name IN $qns RETURN f.qualified_name, c.file_path, c.start_line, c.name")
	counts=$(query "MATCH (c)-[:CALLS|USAGE]->(f) WHERE f.qualified_name IN $qns RETURN f.qualified_name, count(c)")
fi

# A look-alike shares at least 3 of the new function's rare words (in at
# most 3% of functions, or 2 in a small repo), worth at least 45% of
# their weight (idf).
jq -r --slurpfile ch "$tmp/changed.json" --rawfile new "$tmp/new.txt" \
	--argjson callers "$callers" --argjson counts "$counts" --argjson complex "$COMPLEX" --argjson cap "$CAP" '
	. as $fs | ($fs | length) as $N | $ch[0] as $changed
	| ($new | split("\n") | map(select(length > 0))) as $newq
	| (reduce ($fs[].t[]) as $w ({}; .[$w] += 1)) as $df
	| def idf($w): (($N / $df[$w]) | log);
	  def capped($xs): ($xs[:$cap] + (if ($xs | length) > $cap then ["- and \(($xs | length) - $cap) more"] else [] end));
	  def isnew: . as $f | any($newq[]; . == $f.q);
	  def ncalls: . as $f | ([$counts[] | select(.[0] == $f.q) | .[1] | tonumber] | add // 0);
	  def callers_of: . as $f | [$callers[] | select(.[0] == $f.q)
		| if .[3] == .[1] then "\(.[1]) (imports it)" else "\(.[1]):\(.[2]) \(.[3])" end] | unique;
	  def alike: . as $a | ($a.t | map(select($df[.] <= ([2, $N * 0.03] | max)))) as $ra
		| ([$ra[] | idf(.)] | add // 0) as $total
		| [$fs[] | select(.q != $a.q and (.test | not)) | . as $b
			| ([$ra[] | . as $w | select(any($b.t[]; . == $w))]) as $sh
			| {b: "\($b.p):\($b.s) \($b.n)", k: ($sh | length), s: (if $total > 0 then ([$sh[] | idf(.)] | add // 0) / $total else 0 end)}
			| select(.k >= 3 and .s >= 0.45)] | sort_by(-.s) | .[:3];
	($changed | sort_by(.p, .s)) as $c
	| [$c[] | select(isnew)] as $n
	| [$n[] | select((.entry | not) and ncalls == 0)] as $orphans
	| [$n[] | . as $f | alike | select(length > 0) | "- \($f.p):\($f.s) \($f.n) ~ " + (map("\(.b) (\(.s * 100 | floor)%)") | join("; "))] as $likes
	| [$c[] | select(.c >= $complex) | "- \(.p):\(.s) \(.n): \(.c)"] as $hard
	| "# Graph leads: \($c | length) changed functions, \($n | length) new",
	  "",
	  "Leads to read, never findings by themselves: the graph misses calls made by name (a registry, getattr, a route table) and can join two functions that share a name.",
	  "",
	  "## Changed functions and their callers",
	  capped([$c[] | . as $f | callers_of as $cs
		| "- \($f.p):\($f.s)-\($f.e) \($f.n)\(if isnew then " (new)" else "" end): "
		  + (if ($cs | length) == 0 then "no caller found" else ($cs[:5] | join("; ")) + (if ($cs | length) > 5 then "; and \(($cs | length) - 5) more" else "" end) end)])[],
	  "",
	  "## New functions nothing calls",
	  (if ($orphans | length) == 0 then "none" else capped([$orphans[] | "- \(.p):\(.s) \(.n)"])[] end),
	  "",
	  "## Look-alikes of new functions",
	  (if ($likes | length) == 0 then "none" else capped($likes)[] end),
	  "",
	  "## Changed functions over complexity \($complex)",
	  (if ($hard | length) == 0 then "none" else capped($hard)[] end),
	  "@@counts \($c | length) \($n | length) \($orphans | length) \($likes | length) \($hard | length)"' \
	"$tmp/fns.json" >"$tmp/out.md" || die "jq failed on the graph's rows"

read -r _ nc nn no nl nh < <(grep '^@@counts ' "$tmp/out.md")
grep -v '^@@counts ' "$tmp/out.md" >"$out" || die "can't write $out"
echo "graph=yes changed=$nc new=$nn uncalled=$no lookalikes=$nl complex=$nh"
