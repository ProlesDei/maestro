#!/usr/bin/env bash
# The exact part of an install: INSTALL.md has an agent ask the team's
# choices and write them down; this script applies them.
#
#   scripts/install.sh apply [--replace <skill>]... <repo>
#   scripts/install.sh check <repo>
#   scripts/install.sh uninstall <repo>
#
# The choices live in <repo>/.maestro/project/install.yaml, one
# "key: value" per line; a line starting with # is a comment:
#   maestro-sha: <sha>              the commit this Maestro is on (required)
#   harnesses: claude codex grok    claude reads .claude/skills, codex and
#                                   grok .agents/skills (required)
#   skills-home: committed|hidden   tracked in git, or hidden from it (required)
#   bots: <profile> ...             the bots scripts/bot-bundle.sh builds
#
# apply copies each skill in skills/ beside this script into every harness
# folder, writes project-setup.md once, creates the ticket store and lists
# each file it placed, with its git blob hash, in the record:
# .maestro/project/installed.list when committed, else
# <git-common-dir>/maestro/installed.list. It never writes over a file it
# didn't place or one changed since; it lists each such clash and writes
# nothing. --replace <skill> lets it replace what clashed in that skill's
# folder, though a team's file beside Maestro's stays. Every skill installs;
# a team keeps its own under a name without maestro-. A
# file Maestro once shipped at that path counts as placed, so it takes
# over an install by the install.sh before the record. Run it
# again after an update, or after a run that failed partway: it replaces
# what it placed and removes what it no longer installs, unless the team
# changed it: that folder stays, theirs. A switch to hidden untracks the
# copies git tracks (git rm --cached) for the team to commit. It works in
# the main checkout only: a linked worktree shares its exclude file, ticket
# store and hidden record. Where the main checkout is a bare repo, it
# works in one linked worktree instead.
#
# check reports what's wrong with an install; uninstall removes the files
# the record lists, except those changed since.
#
# Exit codes: 0 ok, 1 a clash or a problem, 2 usage.
set -euo pipefail
shopt -s nullglob
unset CDPATH
# A GIT_DIR or GIT_WORK_TREE from the caller would point git at another repo.
unset $(git rev-parse --local-env-vars)

die() { echo "install.sh: $*" >&2; exit 1; }
usage() { echo "usage: scripts/install.sh apply [--replace <skill>]... <repo> | check <repo> | uninstall <repo>" >&2; exit 2; }
# 4.4: mapfile -d, and "${a[@]}" of an empty array under set -u.
((BASH_VERSINFO[0] * 100 + BASH_VERSINFO[1] >= 404)) || die "needs bash 4.4 or later (macOS: brew install bash)"

cmd=${1-}
[ $# -gt 0 ] && shift
replace=()
while [ "$cmd" = apply ] && [ "${1-}" = --replace ]; do
	[ $# -ge 2 ] || usage
	replace+=("$2")
	shift 2
done
case $cmd in apply | check | uninstall) ;; *) usage ;; esac
[ $# -eq 1 ] || usage

root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
[ -d "$1" ] || die "$1 is not a directory"
target=$(cd -- "$1" && pwd -P)
top=$(git -C "$target" rev-parse --show-toplevel 2>/dev/null) || die "$1 is not in a git work tree"
[ "$top" = "$target" ] || die "$1 is inside the repo at $top; install at the top of the repo"
common=$(git -C "$target" rev-parse --path-format=absolute --git-common-dir)
[[ $common == /* && $common != *$'\n'* ]] || die "needs git 2.31 or later"
# One install per repo: its worktrees share the exclude file, ticket store
# and hidden record. It goes in the main checkout, or in a linked worktree
# when the main one is bare.
wts=$(git -C "$target" worktree list --porcelain | sed -n 's/^worktree //p')
if [ "$(git -C "$target" rev-parse --path-format=absolute --git-dir)" != "$common" ] &&
	[ "$(git -C "$common" config --bool core.bare 2>/dev/null)" != true ]; then
	die "$1 is a linked worktree; run install.sh in the main checkout, $(head -n1 <<<"$wts")"
fi
while IFS= read -r w; do
	[ -z "$w" ] || [ "$w" = "$target" ] || [ ! -d "$w/.maestro/src" ] || die "Maestro is installed in $w, a worktree of this repo; run install.sh there"
done <<<"$wts"
exclude=$common/info/exclude
choices=$target/.maestro/project/install.yaml

# --- The choices ---
declare -A set=()
sha= harnesses=() home= bots=()
[ -f "$choices" ] || die "no $choices: INSTALL.md says what goes in it"
n=0
while IFS= read -r line || [ -n "$line" ]; do
	n=$((n + 1))
	[[ $line =~ ^[[:space:]]*(#|$) ]] && continue
	[[ $line =~ ^([a-z-]+):[[:space:]]*(.*[^[:space:]])?[[:space:]]*$ ]] || die "install.yaml:$n: want key: value"
	key=${BASH_REMATCH[1]} val=${BASH_REMATCH[2]-}
	[ -z "${set[$key]-}" ] || die "install.yaml:$n: $key twice"
	set[$key]=1
	case $key in
	maestro-sha) sha=$val ;;
	harnesses) read -ra harnesses <<<"$val" ;;
	skills-home) home=$val ;;
	skip) die "install.yaml:$n: skip: is gone: every skill installs; remove the line" ;;
	# Older installs wrote the default; any other tracker is gone.
	tickets) [ "$val" = maestro-ticket ] || die "install.yaml:$n: tickets: Maestro's bots run only on maestro-ticket; remove the line" ;;
	bots) read -ra bots <<<"$val" ;;
	*) die "install.yaml:$n: unknown key $key" ;;
	esac
done <"$choices"

[[ $sha =~ ^[0-9a-f]{7,40}$ ]] || die "install.yaml: maestro-sha wants a commit sha"
head=$(git -C "$root" rev-parse HEAD 2>/dev/null) || die "$root isn't a git checkout"
[[ $head == "$sha"* ]] || die "Maestro at $root is on ${head:0:12}, but install.yaml pins $sha"
# The copies must be the pinned commit's.
[ "$cmd" != apply ] || [ -z "$(git -C "$root" status --porcelain -- skills templates)" ] ||
	die "Maestro at $root has uncommitted changes in skills/ or templates/; commit them and pin that commit"
[[ $home == committed || $home == hidden ]] || die "install.yaml: skills-home wants committed or hidden"
[ ${#harnesses[@]} -gt 0 ] || die "install.yaml: harnesses wants claude, codex or grok"
dirs=()
for h in "${harnesses[@]}"; do
	case $h in
	claude) d=.claude ;;
	codex | grok) d=.agents ;;
	*) die "install.yaml: unknown harness $h" ;;
	esac
	[[ " ${dirs[*]-} " == *" $d "* ]] || dirs+=("$d")
done
for b in ${bots[@]+"${bots[@]}"}; do
	{ [[ $b =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] && [ -f "$root/bot-profiles/$b/persona.md" ]; } || die "install.yaml: no bot profile $b"
done

# skill <dir> <label>: die unless <dir> is a skill folder. The name goes
# into paths and exclude lines, so no glob or space characters.
skill() {
	{ [[ $(basename "$1") =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] && [ -d "$1" ] && [ ! -L "$1" ] && [ -f "$1/SKILL.md" ]; } ||
		die "$2 isn't a skill: it needs a lowercase-hyphen name and a SKILL.md"
	[ -z "$(find "$1" -type l -print -quit)" ] || die "$2 holds a symlink; copy real files"
}
declare -A from=()
for d in "$root"/skills/*/; do
	name=$(basename "$d")
	skill "$root/skills/$name" "skills/$name"
	from[$name]=$root/skills/$name
done
[ ${#from[@]} -gt 0 ] || die "no skills in $root/skills"
for s in ${replace[@]+"${replace[@]}"}; do
	[ -n "${from[$s]-}" ] || die "no skill $s in $root/skills"
done

records=("$target/.maestro/project/installed.list" "$common/maestro/installed.list")
if [ "$home" = committed ]; then record=${records[0]} other=${records[1]}; else record=${records[1]} other=${records[0]}; fi
# A record path is from the repo's top.
full() { echo "$target/$1"; }
# hashes <path>...: set hs to the git blob hash of each path, a source's
# full path or a record path, in order, or die, before anything is
# written, unless every path got one. Each goes through the repo's
# core.autocrlf and .gitattributes, so a CRLF source or a CRLF checkout
# hashes as its LF blob, from any directory.
hashes() {
	local out
	hs=()
	[ $# -gt 0 ] || return 0
	out=$(printf '%s\n' "$@" | git -C "$target" hash-object --stdin-paths) || die "git hash-object failed; nothing was changed"
	mapfile -t hs <<<"$out"
	[ ${#hs[@]} -eq $# ] || die "git hash-object gave ${#hs[@]} hashes for $# files; nothing was changed"
}

# old: the record. apply also reads the one an install in the other
# skills-home left, so a switch takes over its copies.
declare -A old=()
reads=("$record")
[ "$cmd" != apply ] || reads+=("$other")
# A record path names a file in a harness skill folder, with no . or ..
# part: a committed record is anyone's to edit, and apply and uninstall
# delete what it lists.
recpath='^\.(claude|agents)/skills/[a-z0-9]+(-[a-z0-9]+)*/[^/]'
for r in "${reads[@]}"; do
	[ -f "$r" ] || continue
	n=0
	while IFS=$'\t' read -r h p || [ -n "$h" ]; do
		n=$((n + 1))
		[[ $h == '#'* || -z $h ]] && continue
		p=${p%$'\r'} # a CRLF checkout of the committed record
		[[ $h =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ && $p =~ $recpath && /$p/ != */../* && /$p/ != */./* && $p != *//* && $p != */ ]] ||
			die "$r:$n: want a blob hash, a tab and a file under .claude/skills/<skill>/ or .agents/skills/<skill>/; nothing was changed"
		old[$p]=$h
	done <"$r"
done

# skill_of <path>: the skill folder name in a record path.
skill_of() { local p=${1#*/skills/}; echo "${p%%/*}"; }
# linked <record path>: true if the path, or a folder on the way to it, is
# a symlink, so a write or delete there would land outside the repo.
linked() {
	local p=$1
	while :; do
		[ ! -L "$target/$p" ] || return 0
		[[ $p == */* ]] || return 1
		p=${p%/*}
	done
}

# prune <record path>...: remove the empty folders left in each skill
# folder the paths are in, that folder included, then the harness folders
# if they're empty.
prune() {
	local p d
	local -A pruned=()
	for p in "$@"; do
		d=${p%/"${p#*/skills/*/}"}
		[ -z "${pruned[$d]-}" ] || continue
		pruned[$d]=1
		[ ! -d "$(full "$d")" ] || linked "$d" || find "$(full "$d")" -depth -type d -empty -delete
	done
	for d in .claude .agents; do rmdir -- "$target/$d/skills" "$target/$d" 2>/dev/null || true; done
}

# The exclude lines between these markers are this script's. apply also
# drops the legacy ones, below.
begin="# maestro install: begin" end="# maestro install: end"
legacy=
# exclude_lines <skill>...: the lines that hide Maestro's folders and, when
# hidden, these skills.
exclude_lines() {
	echo /.maestro/src/
	echo /.maestro/worktrees/
	echo /.maestro/bots/
	[ "$home" = hidden ] || return 0
	local n d
	for n in $(printf '%s\n' "$@" | sort -u); do
		for d in "${dirs[@]}"; do echo "/$d/skills/$n"; done
	done
}
# write_exclude <lines>: replace the marked block; no lines drops it. The
# temp copy keeps the file's mode.
write_exclude() {
	mkdir -p "$(dirname "$exclude")"
	touch "$exclude"
	local tmp=$scratch/exclude
	cp -p -- "$exclude" "$tmp"
	# legacy holds newlines, which macOS awk refuses in a -v value.
	legacy=$legacy awk -v b="$begin" -v e="$end" 'BEGIN{n=split(ENVIRON["legacy"], a, "\n"); for (i=1; i<=n; i++) if (a[i] != "") old[a[i]]=1}
		$0==b{skip=1;next} $0==e{skip=0;next} !skip && !($0 in old)' "$exclude" >"$tmp"
	if [ -n "$1" ]; then
		[ ! -s "$tmp" ] || [ -z "$(tail -c1 "$tmp")" ] || echo >>"$tmp"
		printf '%s\n%s\n%s\n' "$begin" "$1" "$end" >>"$tmp"
	fi
	mv "$tmp" "$exclude"
}

# Temp files go in one folder in the git dir, out of the work tree, and
# it goes when the script ends, however it ends.
if [ "$cmd" != check ]; then
	scratch=$(mktemp -d "$common/maestro-install.XXXXXX")
	trap 'rm -rf -- "$scratch"' EXIT
	trap 'exit 130' INT
	trap 'exit 143' TERM
fi

# --- check ---
if [ "$cmd" = check ]; then
	problems=0
	problem() { echo "problem: $*"; problems=$((problems + 1)); }
	warn() { echo "warn: $*"; }
	if [ "$root" = "$target/.maestro/src" ]; then
		for p in .claude .agents .maestro AGENTS.md CLAUDE.md; do
			[ ! -e "$root/$p" ] || problem ".maestro/src/$p exists: harnesses would load Maestro's own files; check out only what INSTALL.md lists"
		done
	fi
	if [ ! -f "$record" ]; then
		problem "no record at $record: run apply"
	else
		paths=() want=()
		for p in "${!old[@]}"; do
			if [ -f "$(full "$p")" ]; then paths+=("$p") want+=("${old[$p]}"); else problem "missing: $p"; fi
		done
		hashes ${paths[@]+"${paths[@]}"}
		for i in "${!hs[@]}"; do
			[ "${hs[i]}" = "${want[i]}" ] || problem "changed since install: ${paths[i]}"
		done
		# The copies match the record, so they're from the commit it names.
		was=$(sed -n '1s/^# Files scripts\/install\.sh placed at Maestro \([0-9a-f]*\):.*/\1/p' "$record")
		[ -z "$was" ] || [[ $head == "$was"* ]] || problem "the record is from Maestro $was, not the pinned $sha: run apply"
	fi
	for n in "${!from[@]}"; do
		for d in "${dirs[@]}"; do
			[ -f "$target/$d/skills/$n/SKILL.md" ] || problem "$d/skills/$n is missing"
		done
	done
	# Hidden skills must be ignored, committed ones not. A tracked folder
	# isn't ignored. One git call for every skill.
	declare -A ign=()
	out=$(for n in "${!from[@]}"; do for d in "${dirs[@]}"; do echo "$d/skills/$n"; done; done |
		git -C "$target" check-ignore --stdin) || [ $? -eq 1 ] || die "git check-ignore failed"
	while IFS= read -r p; do [ -z "$p" ] || ign[$p]=1; done <<<"$out"
	for n in "${!from[@]}"; do
		for d in "${dirs[@]}"; do
			if [ "$home" = hidden ]; then
				[ -n "${ign[$d/skills/$n]-}" ] || problem "$d/skills/$n isn't hidden from git"
			else
				[ -z "${ign[$d/skills/$n]-}" ] || problem "$d/skills/$n is hidden from git, but skills-home is committed"
			fi
		done
	done
	[ "$(git -C "$root" rev-parse --is-shallow-repository)" != true ] ||
		warn "Maestro at $root is a shallow clone: check can't tell if the tools are older than the pin; fetch its full history"
	for b in maestro-ticket; do
		bin=$(command -v "$b") || { problem "$b isn't on PATH"; continue; }
		command -v go >/dev/null || continue
		rev=$(go version -m "$bin" 2>/dev/null | awk '$1=="build" && $2 ~ /^vcs.revision=/ {sub(/^vcs.revision=/, "", $2); print $2}' || true)
		if [ -z "$rev" ]; then
			warn "$b has no build revision; build it from $root/tools/$b"
		elif [[ $rev != "$sha"* ]] && git -C "$root" merge-base --is-ancestor "$rev" "$sha" 2>/dev/null; then
			warn "$b is built from ${rev:0:12}, older than the pinned $sha; rebuild it from $root/tools/$b"
		fi
	done
	setup=$target/.maestro/project/project-setup.md
	if [ ! -f "$setup" ]; then
		problem "no .maestro/project/project-setup.md"
	else
		# Each line trimmed, \r included, after its heading in lower case and
		# as written, a tab apart.
		sections=$(awk '{sub(/\r$/, ""); gsub(/^[ \t]+|[ \t]+$/, "")}
			/^## / {h=substr($0, 4); gsub(/^[ \t]+|[ \t]+$/, "", h); s=tolower(h); next}
			$0 != "" && !/^#/ {print s "\t" h "\t" $0}' "$setup")
		# line: the setup line after the two tabs; it may hold tabs too.
		line='function line() { t=$0; sub(/^[^\t]*\t[^\t]*\t/, "", t); return t }'
		main=$(awk -F'\t' "$line"' $1=="main branch" {t=line(); sub(/^[-* ]+/, "", t); gsub(/^[`* ]+|[`* ]+$/, "", t); print t; exit}' <<<"$sections")
		[ -n "$main" ] || problem "project-setup.md: no branch under ## Main branch"
		[ -z "$main" ] || git -C "$target" rev-parse --verify --quiet "refs/heads/$main" >/dev/null ||
			git -C "$target" rev-parse --verify --quiet "refs/remotes/origin/$main" >/dev/null ||
			problem "project-setup.md: main branch $main doesn't exist"
		awk -F'\t' "$line"' $1=="checks" && line() ~ /^[-*][ \t]+[^:`]+:[ \t]*`[^`]+`$/ {f=1} END{exit !f}' <<<"$sections" ||
			warn "project-setup.md lists no checks: the engineers run none"
		# Any other bullet there isn't a check an engineer can run.
		while IFS= read -r l; do
			problem "project-setup.md: under ## Checks, want - <name>: \`<command>\`, not: $l"
		done < <(awk -F'\t' "$line"' $1=="checks" && line() ~ /^([-*+]|[0-9]+[.)])[ \t]/ && line() !~ /^[-*][ \t]+[^:`]+:[ \t]*`[^`]+`$/ {print line()}' <<<"$sections")
		# Older installs wrote a ## Tickets section; nothing reads it now.
		grep -qiE '^##[[:space:]]+tickets[[:space:]]*$' "$setup" &&
			warn "project-setup.md: ## Tickets is gone, since every team uses maestro-ticket; delete the section"
		awk -F'\t' "$line"' $1!="tickets" && line() ~ /^<.*>$/ {print $2}' <<<"$sections" | sort -u | while IFS= read -r s; do
			warn "project-setup.md: ## $s still holds the template's <...> text"
		done
	fi
	if [ $problems -gt 0 ]; then
		echo "$problems problems"
		exit 1
	fi
	echo "ok: $home install of ${#from[@]} skills at $sha"
	exit 0
fi

# --- uninstall ---
if [ "$cmd" = uninstall ]; then
	[ -f "$record" ] || die "no record at $record: nothing to remove"
	kept=() behind=() removed=0 paths=() want=()
	for p in "${!old[@]}"; do
		if linked "$p"; then
			behind+=("$p")
		elif [ -f "$(full "$p")" ]; then
			paths+=("$p") want+=("${old[$p]}")
		fi
	done
	hashes ${paths[@]+"${paths[@]}"}
	for i in "${!hs[@]}"; do
		if [ "${hs[i]}" = "${want[i]}" ]; then
			rm -f -- "$(full "${paths[i]}")"
			removed=$((removed + 1))
		else
			kept+=("${paths[i]}")
		fi
	done
	[ ${#old[@]} -eq 0 ] || prune "${!old[@]}"
	# .maestro/src/ and .maestro/bots/ stay, so they stay hidden.
	write_exclude "$(home=committed exclude_lines)"
	rm -f -- "$record"
	echo "removed $removed files"
	echo "left in place: .maestro/project/ (install.yaml, project-setup.md), .maestro/src/, .maestro/bots/ and the ticket store at $common/maestro/"
	[ ${#kept[@]} -eq 0 ] || printf 'kept, changed since install: %s\n' "${kept[@]}"
	[ ${#behind[@]} -eq 0 ] || printf 'kept, behind a symlink: %s\n' "${behind[@]}"
	[ ${#kept[@]} -eq 0 ] && [ ${#behind[@]} -eq 0 ] || exit 1
	exit 0
fi

# --- apply ---
for p in .claude .claude/skills .agents .agents/skills .maestro .maestro/project; do
	# A copy behind a symlink would land outside the repo, where exclude can't hide it.
	[ ! -L "$target/$p" ] || die "$target/$p is a symlink; install writes only inside the repo"
done

# srcfiles <skill>: its files, NUL-separated: for Maestro's, the ones git
# tracks, so a file git ignores (.DS_Store, a swap file) isn't the pinned
# commit's; for the team's ticket skill, all but a .git.
srcfiles() {
	if [ "${from[$1]}" = "$root/skills/$1" ]; then
		git -C "$root" ls-files -z -- "skills/$1" | while IFS= read -r -d '' f; do printf '%s\0' "$root/$f"; done
	else
		find "${from[$1]}" -name .git -prune -o -type f -print0
	fi
}
# The plan: every file to place, keyed by its record path.
declare -A src=() new=()
plan=()
for n in "${!from[@]}"; do
	while IFS= read -r -d '' f; do
		rel=${f#"${from[$n]}/"}
		for d in "${dirs[@]}"; do
			p=$d/skills/$n/$rel
			src[$p]=$f
			plan+=("$p")
		done
	done < <(srcfiles "$n")
done
srcs=()
for p in "${plan[@]}"; do srcs+=("${src[$p]}"); done
hashes "${srcs[@]}"
for i in "${!hs[@]}"; do new[${plan[i]}]=${hs[i]}; done
# pdirs: each folder inside a skill folder that a planned file goes in.
declare -A pdirs=()
for p in "${plan[@]}"; do
	q=${p%/*}
	while [[ $q == */skills/*/* ]] && [ -z "${pdirs[$q]-}" ]; do
		pdirs[$q]=1
		q=${q%/*}
	done
done

# Every file now in a folder this install writes to, or listed in the
# record, is in the plan, the record, or neither (the team's).
# read_shipped: read once, when needed, each skill file Maestro ever shipped,
# into shipped["<blob> <skill>/<path>"], and each skill name into names.
declare -A shipped=() names=()
shipped_read=
read_shipped() {
	[ -z "$shipped_read" ] || return 0
	shipped_read=1
	local out a b c h st path
	[ "$(git -C "$root" rev-parse --is-shallow-repository)" != true ] ||
		die "Maestro at $root is a shallow clone, so apply can't tell an older install's copies from the team's files; fetch its full history (git fetch --unshallow); nothing was changed"
	out=$(git -C "$root" log --format= --raw --no-abbrev --no-renames -- skills) || die "git log failed in $root; nothing was changed"
	while read -r a b c h st path; do
		[ -n "$path" ] || continue
		path=${path#skills/}
		shipped["$h $path"]=1
		names[${path%%/*}]=1
	done <<<"$out"
}
# The install.sh before the record hid each skill it placed with a line
# like .claude/skills/<name>/ outside the marked block. With no record,
# such a line for a skill Maestro has shipped is its: apply drops it.
if [ ${#old[@]} -eq 0 ]; then
	lines=$(awk -v b="$begin" -v e="$end" '$0==b{s=1;next} $0==e{s=0;next} !s && /^\.(claude|agents)\/skills\/maestro-[a-z0-9]+(-[a-z0-9]+)*\/$/' "$exclude" 2>/dev/null || true)
	[ -z "$lines" ] || read_shipped
	for l in $lines; do [ -z "${names[$(skill_of "$l")]-}" ] || legacy+=$l$'\n'; done
fi
# The folders to scan: each this install writes to, and each one the
# record or a legacy line names, since the team may have changed it.
declare -A writing=() folders=() seen=()
for n in "${!from[@]}"; do for d in "${dirs[@]}"; do writing[$d/skills/$n]=1 folders[$d/skills/$n]=1; done; done
for p in "${!old[@]}"; do folders[${p%/"${p#*/skills/*/}"}]=1; done
for l in $legacy; do folders[${l%/}]=1; done
for dir in "${!folders[@]}"; do
	if [ -L "$target/$dir" ]; then
		seen[$dir]=link
	elif [ -d "$target/$dir" ]; then
		while IFS= read -r -d '' f; do
			seen["${f#"$target/"}"]=1
		done < <(find "$target/$dir" \( -type f -o -type l \) -print0)
	elif [ -e "$target/$dir" ]; then
		seen[$dir]=1 # a file where a skill folder goes
	fi
done
# find lists files and links, so a folder at a recorded path isn't seen:
# the files in it are. Nor is a file behind a linked folder: the link is,
# and when it goes, the plan writes the file again.
cur_paths=()
for p in "${!seen[@]}"; do [ "${seen[$p]}" = link ] || [ -L "$(full "$p")" ] || cur_paths+=("$p"); done
declare -A cur=()
hashes ${cur_paths[@]+"${cur_paths[@]}"}
for i in "${!hs[@]}"; do cur[${cur_paths[i]}]=${hs[i]}; done

# A file the record doesn't list, but that Maestro once shipped at that
# path with that content, is an unchanged copy: one from the install.sh
# before the record, or from a run that stopped before it wrote the record.
for p in "${!cur[@]}"; do
	[ -n "${old[$p]-}" ] || read_shipped
	[ -n "${old[$p]-}" ] || [ -z "${shipped["${cur[$p]} ${p#*/skills/}"]-}" ] || old[$p]=${cur[$p]}
done

# A skill folder this install no longer writes to (the skill dropped by
# Maestro, or its harness gone) that the team changed is
# theirs now: it stays as it is and leaves the record. One they didn't
# change goes.
declare -A theirs=()
for p in "${!seen[@]}"; do
	f=${p%/"${p#*/skills/*/}"}
	[ -z "${writing[$f]-}" ] || continue
	# A file the record doesn't list, such as a .DS_Store, stays but doesn't
	# keep Maestro's copies beside it.
	if [ "${seen[$p]}" = link ] || [ -L "$(full "$p")" ]; then
		theirs[$f]=1
	elif [ -n "${old[$p]-}" ] && [ "${cur[$p]}" != "${old[$p]}" ]; then
		theirs[$f]=1
	fi
done

replacing() { [[ " ${replace[*]-} " == *" $(skill_of "$1") "* ]]; }
# in_way <path>: true if the path is a skill folder or a folder a planned
# file goes in, or a planned file is on the way to it, so it blocks a copy.
in_way() {
	local q=$1
	[ -z "${writing[$q]-}" ] && [ -z "${pdirs[$q]-}" ] || return 0
	while [[ $q == */* ]]; do
		q=${q%/*}
		[ -z "${new[$q]-}" ] || return 0
	done
	return 1
}
clashes=() writes=() deletes=()
for p in "${!seen[@]}"; do
	if [ -n "${theirs[${p%/"${p#*/skills/*/}"}]-}" ]; then
		continue
	elif [ "${seen[$p]}" = link ] || [ -L "$(full "$p")" ]; then
		if replacing "$p"; then deletes+=("$p"); else clashes+=("$p is a symlink"); fi
	elif [ -n "${new[$p]-}" ]; then
		if [ "${cur[$p]}" = "${new[$p]}" ]; then :
		elif [ "${cur[$p]}" = "${old[$p]-}" ] || replacing "$p"; then writes+=("$p")
		elif [ -n "${old[$p]-}" ]; then clashes+=("$p changed since install")
		else clashes+=("$p isn't Maestro's"); fi
	elif [ "${cur[$p]}" = "${old[$p]-}" ]; then deletes+=("$p")
	# The team's file beside Maestro's, such as a .DS_Store, stays, even
	# with --replace: no clash named it.
	elif [ -z "${old[$p]-}" ] && ! in_way "$p"; then :
	elif replacing "$p"; then deletes+=("$p")
	elif [ -n "${old[$p]-}" ]; then clashes+=("$p changed since install, and Maestro no longer installs it")
	else clashes+=("$p isn't Maestro's")
	fi
done
for p in "${plan[@]}"; do [ -n "${seen[$p]-}" ] || writes+=("$p"); done
if [ ${#clashes[@]} -gt 0 ]; then
	printf 'clash: %s\n' "${clashes[@]}" | sort >&2
	die "nothing was written; move the team's changes to a skill named without maestro-, then replace them with --replace <skill>"
fi

# The store lives in the git common dir, outside the work tree; doctor
# creates it and checks the repo can hold one, before anything is written.
command -v maestro-ticket >/dev/null || die "maestro-ticket isn't on PATH: build it from $root/tools/maestro-ticket"
(cd "$target" && maestro-ticket doctor >/dev/null) || die "maestro-ticket doctor failed in $target; nothing was written"

# Hide the old and new skills before the first copy, so a run that stops
# partway shows nothing in git status. Each file lands whole (a temp file,
# then a rename), so a rerun finds it either as it was or as planned.
olds=()
for p in "${!old[@]}"; do olds+=("$(skill_of "$p")"); done
for l in $legacy; do olds+=("$(skill_of "$l")"); done
write_exclude "$(exclude_lines "${!from[@]}" ${olds[@]+"${olds[@]}"})"
for p in ${deletes[@]+"${deletes[@]}"}; do rm -rf -- "$(full "$p")"; done
i=0
for p in ${writes[@]+"${writes[@]}"}; do
	f=$(full "$p")
	[ ! -L "$(dirname "$f")" ] || rm -f -- "$(dirname "$f")"
	# A folder where a file goes holds only folders now: any file in it
	# clashed or was deleted above.
	[ ! -d "$f" ] || [ -L "$f" ] || find "$f" -depth -type d -empty -delete
	[ ! -d "$f" ] || [ -L "$f" ] || die "$p is a folder that isn't empty; run apply again"
	mkdir -p "$(dirname "$f")"
	i=$((i + 1)) tmp=$scratch/$i
	cp -p -- "${src[$p]}" "$tmp" || { rm -f -- "$tmp"; die "couldn't copy ${src[$p]} to $f; run apply again"; }
	chmod u+w "$tmp" # a read-only source stays removable
	mv -f -- "$tmp" "$f"
done
prune ${deletes[@]+"${deletes[@]}"}
mkdir -p "$target/.maestro/project"
conf=$target/.maestro/project/project-setup.md
[ -e "$conf" ] || [ -L "$conf" ] || cp "$root/templates/project-setup.md" "$conf"
write_exclude "$(exclude_lines "${!from[@]}")"
mkdir -p "$(dirname "$record")"
tmp=$scratch/record
{
	echo "# Files scripts/install.sh placed at Maestro $sha: git blob hash, tab, path."
	for p in "${plan[@]}"; do printf '%s\t%s\n' "${new[$p]}" "$p"; done | sort -k2
} >"$tmp"
mv "$tmp" "$record"
# Exclude lines don't hide a file git tracks, so a switch to hidden
# untracks Maestro's copies and the committed record; the team commits it.
untracked=0
if [ "$home" = hidden ]; then
	mine=("${plan[@]}" "${other#"$target/"}")
	for p in "${!old[@]}"; do [ -n "${theirs[${p%/"${p#*/skills/*/}"}]-}" ] || mine+=("$p"); done
	mapfile -d '' -t tracked < <(git -C "$target" --literal-pathspecs ls-files -z -- "${mine[@]}")
	if [ ${#tracked[@]} -gt 0 ]; then
		# -f: a staged edit to a copy would make git refuse; the file stays.
		git -C "$target" --literal-pathspecs rm -q --cached -f -- "${tracked[@]}"
		untracked=${#tracked[@]}
	fi
fi
rm -f -- "$other"

echo "ticket store ready"
echo "installed ${#from[@]} skills into ${dirs[*]} ($home): ${#writes[@]} files written, ${#deletes[@]} removed"
echo "record: $record"
# It's the team's own skill now, so git sees it like the rest of theirs.
visible=
[ "$home" != hidden ] || visible=" and no longer hidden from git"
for f in "${!theirs[@]}"; do echo "kept as the team's, out of the record$visible: $f"; done
[ $untracked -eq 0 ] || echo "untracked $untracked files git tracked: commit that to hide them"
