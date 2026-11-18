#!/usr/bin/env bash
# graph-leads.sh: what a code graph says about one change, for review and
# for an engineer's self-review, so neither spends reads finding it.
#
#   graph-leads.sh <worktree> <base> <out file>
#     Indexes the worktree's checkout with codebase-memory-mcp under a
#     name no other run uses, maps the change from <base> (a commit or a
#     tree, such as review's delta base T) to the worktree's HEAD onto
#     the functions it touches, writes <out> and deletes the index,
#     whatever happens. <out> lists, each capped: each changed function
#     with its callers; each new function nothing calls; each new
#     function that shares most of its rare words with one already there
#     (a look-alike: a helper the change may have copied); and each
#     changed function over COMPLEX in cyclomatic complexity. Every item
#     is a lead to read, never a finding: the graph misses calls made by
#     name and can join two functions that share one.
#     Prints graph=yes and the counts; graph=none: <why> when
#     codebase-memory-mcp isn't on PATH, writing nothing. Exit 0 then;
#     1 on any failure, with the reason on stderr. Refuses a worktree
#     with uncommitted changes to tracked files: the index reads the
#     disk, the diff reads HEAD.
#
# Needs bash 4.4+, git 2.40+ and jq. GRAPH_LEADS_TIMEOUT caps the index
# in seconds (default 300), where coreutils' timeout is on PATH.

set -euo pipefail
die() { echo "graph-leads: $1" >&2; exit 1; }

[ $# -eq 3 ] || die "usage: graph-leads.sh <worktree> <base> <out file>"
wt=$1 base=$2 out=$3
COMPLEX=15  # cyclomatic, as the graph counts it
CAP=30      # lines per list
ROWS=100000 # query_graph's row ceiling

command -v codebase-memory-mcp >/dev/null || { echo "graph=none: no codebase-memory-mcp on PATH"; exit 0; }
command -v jq >/dev/null || die "needs jq"
git -C "$wt" cat-file -e "$base^{tree}" 2>/dev/null || die "$base is no commit or tree in $wt"
git -C "$wt" diff --quiet HEAD -- || die "$wt has uncommitted changes: commit them, so the index and the diff read the same code"

cli() { codebase-memory-mcp cli "$@" 2>/dev/null; }
# query <cypher> <file>: the rows, as one JSON array, in <file>. Both go
# by stdin and files, never as one argument, which Linux caps at 128KiB.
query() {
	printf '%s' "$1" | jq -Rsc --arg p "$project" '{project: $p, query: .}' | cli query_graph | tail -n 1 |
		jq -ce '.rows // error("no rows")' >"$2" || die "graph query failed: ${1:0:80}"
}
# Paths as written; attributes from an empty tree and no global file, and
# no external diff, so the change's own .gitattributes or the user's
# config hides no hunk, as review-prep's diff does.
attrs=()
empty=$(git -C "$wt" hash-object -t tree /dev/null)
git -C "$wt" --attr-source="$empty" rev-parse --git-dir >/dev/null 2>&1 && attrs=(--attr-source="$empty")
gd() {
	git -C "$wt" -c core.quotePath=false -c core.attributesFile=/dev/null -c diff.noprefix=false "${attrs[@]}" \
		diff --no-ext-diff --no-color --src-prefix=a/ --dst-prefix=b/ "$@"
}

# Six letters from the OS name the index, so runs started in one second
# never share one. The name is set before indexing starts, so a run
# killed while it indexes still deletes it.
abc=abcdefghijklmnopqrstuvwxyz rand=""
for b in $(od -An -N6 -tu1 /dev/urandom); do rand+=${abc:b % 26:1}; done
[ ${#rand} -eq 6 ] || die "no 6 random bytes from /dev/urandom"
project=maestro-leads-$rand
tmp=$(mktemp -d)
cleanup() {
	cli delete_project --project "$project" >/dev/null || true
	rm -rf -- "$tmp"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

limit=()
command -v timeout >/dev/null && limit=(timeout "${GRAPH_LEADS_TIMEOUT:-300}")
# full: the other modes skip folders such as scripts/ as if they held no code.
got=$("${limit[@]}" codebase-memory-mcp cli index_repository --repo-path "$(cd "$wt" && pwd)" --mode full \
	--name "$project" 2>/dev/null | tail -n 1) || die "indexing $wt failed or ran past its time limit"
# Into its own name first: a failed jq would blank $project, and cleanup
# would then delete no index.
indexed=$(jq -re 'select(.status == "indexed") | .project' <<<"$got") || die "indexing $wt failed"
project=$indexed

# The new side of each hunk: path, first line, last line. A hunk that
# only deletes touches the line before it. A file name comes only from a
# header, between "diff --git" and the first @@, never from an added line
# that starts "++ ". Renames are followed, so a moved file's functions
# aren't new; old[] maps each new path back to its base path.
declare -A old=()
while IFS= read -r -d '' st; do
	case $st in
	R* | C*) IFS= read -r -d '' a && IFS= read -r -d '' b && old[$b]=$a ;;
	*) IFS= read -r -d '' _ ;;
	esac
done < <(gd --name-status -z -M "$base" HEAD)
gd -U0 -M "$base" HEAD |
	awk '/^diff --git /{h = 1; f = ""; next}
		h && /^\+\+\+ /{f = substr($0, 5); sub(/^b\//, "", f); if (f == "/dev/null") f = ""; next}
		/^@@ /{h = 0; if (f == "") next; split($3, a, ","); s = substr(a[1], 2) + 0; n = (2 in a) ? a[2] + 0 : 1
			if (n == 0) { if (s > 0) print f "\t" s "\t" s } else print f "\t" s "\t" s + n - 1}' >"$tmp/hunks.tsv"

query 'MATCH (f) WHERE f:Function OR f:Method RETURN f.qualified_name, f.file_path, f.name, f.start_line, f.end_line, f.complexity, f.is_test, f.is_entry_point, f.bt' "$tmp/rows.json"
[ "$(jq length "$tmp/rows.json")" -lt "$ROWS" ] || die "the graph holds $ROWS functions or more, past what one query returns"
jq -c 'to_entries | map(.key as $i | .value | {i: $i, q: .[0], p: .[1], n: .[2], s: (.[3] | tonumber? // 0), e: (.[4] | tonumber? // 0),
	c: (.[5] | tonumber? // 0), test: (.[6] == "true" or .[6] == true), entry: (.[7] == "true" or .[7] == true),
	t: ((.[8] // "") | split(" ") | map(select(length > 2)) | unique)})' "$tmp/rows.json" >"$tmp/fns.json"

# The functions a hunk overlaps; new when the base's copy of the file
# never says the name. The file goes to disk first: grep -q stops at its
# first match, and under pipefail the writer's SIGPIPE would read as
# not found.
jq -c --rawfile h "$tmp/hunks.tsv" '
	($h | split("\n") | map(select(length > 0) | split("\t") | {p: .[0], s: (.[1] | tonumber), e: (.[2] | tonumber)})
		| group_by(.p) | map({key: .[0].p, value: .}) | from_entries) as $hs
	| [.[] | select(.test | not) | . as $f | select(any(($hs[$f.p] // [])[]; .s <= $f.e and .e >= $f.s))]' \
	"$tmp/fns.json" >"$tmp/changed.json"
: >"$tmp/new.txt"
while IFS=$'\t' read -r q p n; do
	git -C "$wt" show "$base:${old[$p]:-$p}" >"$tmp/base-file" 2>/dev/null || : >"$tmp/base-file"
	grep -qwF -- "$n" "$tmp/base-file" || printf '%s\n' "$q" >>"$tmp/new.txt"
done < <(jq -r '.[] | [.q, .p, .n] | @tsv' "$tmp/changed.json")

echo '[]' >"$tmp/callers.json"
echo '[]' >"$tmp/counts.json"
if [ "$(jq length "$tmp/changed.json")" -gt 0 ]; then
	qns=$(jq -c '[.[].q]' "$tmp/changed.json")
	# USAGE too: a call through `from m import f` is one in this graph.
	query "MATCH (c)-[:CALLS|USAGE]->(f) WHERE f.qualified_name IN $qns RETURN f.qualified_name, c.file_path, c.start_line, c.name" "$tmp/callers.json"
	query "MATCH (c)-[:CALLS|USAGE]->(f) WHERE f.qualified_name IN $qns RETURN f.qualified_name, count(c)" "$tmp/counts.json"
fi

# A look-alike shares at least 3 of the new function's rare words (in at
# most 3% of functions, or 2 in a small repo), worth at least 45% of
# their weight (idf). Only functions that share a rare word are scored.
jq -r --slurpfile ch "$tmp/changed.json" --rawfile new "$tmp/new.txt" --slurpfile callers "$tmp/callers.json" \
	--slurpfile counts "$tmp/counts.json" --argjson complex "$COMPLEX" --argjson cap "$CAP" '
	. as $fs | ($fs | length) as $N | $ch[0] as $changed
	| ($new | split("\n") | map(select(length > 0) | {key: ., value: true}) | from_entries) as $isnew
	| ($counts[0] | map({key: .[0], value: (.[1] | tonumber)}) | from_entries) as $ncalls
	| ($callers[0] | group_by(.[0]) | map({key: .[0][0], value: (map(if .[3] == .[1] then "\(.[1]) (imports it)"
		else "\(.[1]):\(.[2]) \(.[3])" end) | unique)}) | from_entries) as $callmap
	| (reduce $fs[] as $f ({}; reduce $f.t[] as $w (.; .[$w] += [$f.i]))) as $inv
	| ([2, $N * 0.03] | max) as $rare
	| def idf($w): (($N / ($inv[$w] | length)) | log);
	  def capped($xs): ($xs[:$cap] + (if ($xs | length) > $cap then ["- and \(($xs | length) - $cap) more"] else [] end));
	  def alike: . as $a | ($a.t | map(select(($inv[.] | length) <= $rare))) as $ra
		| ([$ra[] | idf(.)] | add // 0) as $total
		| if $total == 0 then [] else
			(reduce $ra[] as $w ({}; reduce $inv[$w][] as $b (.; .["\($b)"] |= {k: ((.k // 0) + 1), s: ((.s // 0) + idf($w))})))
			| to_entries | map($fs[.key | tonumber] as $b | select($b.q != $a.q and ($b.test | not))
				| {b: "\($b.p):\($b.s) \($b.n)", k: .value.k, s: (.value.s / $total)} | select(.k >= 3 and .s >= 0.45))
			| sort_by(-.s) | .[:3] end;
	($changed | sort_by(.p, .s)) as $c
	| [$c[] | select($isnew[.q])] as $n
	| [$n[] | select((.entry | not) and (($ncalls[.q] // 0) == 0))] as $orphans
	| [$n[] | . as $f | alike | select(length > 0) | "- \($f.p):\($f.s) \($f.n) ~ " + (map("\(.b) (\(.s * 100 | floor)%)") | join("; "))] as $likes
	| [$c[] | select(.c >= $complex) | "- \(.p):\(.s) \(.n): \(.c)"] as $hard
	| "# Graph leads: \($c | length) changed functions, \($n | length) new",
	  "",
	  "Leads to read, never findings by themselves: the graph misses calls made by name (a registry, getattr, a route table) and can join two functions that share a name.",
	  "",
	  "## Changed functions and their callers",
	  capped([$c[] | . as $f | ($callmap[$f.q] // []) as $cs
		| "- \($f.p):\($f.s)-\($f.e) \($f.n)\(if $isnew[$f.q] then " (new)" else "" end): "
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
