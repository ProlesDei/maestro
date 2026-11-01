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
#   maestro-sha: <sha>                  the commit this Maestro is on (required)
#   harnesses: claude codex grok        claude reads .claude/skills, codex and
#                                       grok .agents/skills (required)
#   skills-home: committed|hidden|user  in the repo and tracked, in the repo and
#                                       hidden from git, or under ~ (required)
#   skip: <skill> ...                   skills not to install
#   tickets: maestro-ticket|<dir>       <dir>, from the repo's top, holds the
#                                       team's maestro-create-ticket/
#   bots: <profile> ...                 the bots scripts/bot-bundle.sh builds
#
# apply copies each skill in skills/ beside this script into every harness
# folder, writes project-setup.md once, creates the ticket store and lists
# each file it placed, with its git blob hash, in the record:
# .maestro/project/installed.list when committed, else
# <git-common-dir>/maestro/installed.list. It never writes over a file it
# didn't place or one changed since; it lists each such clash and writes
# nothing. --replace <skill> lets it replace that skill's folder. Run it
# again after an update: it replaces what it placed and removes what it no
# longer installs.
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
[ "${BASH_VERSINFO[0]}" -ge 4 ] || die "needs bash 4 or later (macOS: brew install bash)"

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

root=$(cd -- "$(dirname -- "$0")/.." && pwd)
[ -d "$1" ] || die "$1 is not a directory"
target=$(cd -- "$1" && pwd -P)
top=$(git -C "$target" rev-parse --show-toplevel 2>/dev/null) || die "$1 is not in a git work tree"
[ "$top" = "$target" ] || die "$1 is inside the repo at $top; install at the top of the repo"
common=$(git -C "$target" rev-parse --path-format=absolute --git-common-dir)
[[ $common == /* && $common != *$'\n'* ]] || die "needs git 2.31 or later"
exclude=$common/info/exclude
choices=$target/.maestro/project/install.yaml

# --- The choices ---
declare -A set=()
sha= harnesses=() home= skip=() tickets=maestro-ticket bots=()
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
	skip) read -ra skip <<<"$val" ;;
	tickets) tickets=$val ;;
	bots) read -ra bots <<<"$val" ;;
	*) die "install.yaml:$n: unknown key $key" ;;
	esac
done <"$choices"

[[ $sha =~ ^[0-9a-f]{7,40}$ ]] || die "install.yaml: maestro-sha wants a commit sha"
head=$(git -C "$root" rev-parse HEAD 2>/dev/null) || die "$root isn't a git checkout"
[[ $head == "$sha"* ]] || die "Maestro at $root is on ${head:0:12}, but install.yaml pins $sha"
case $home in
committed | hidden) base=$target ;;
user) base=$HOME ;;
*) die "install.yaml: skills-home wants committed, hidden or user" ;;
esac
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
	[ -f "$root/bot-profiles/$b/persona.md" ] || die "install.yaml: no bot profile $b"
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
for s in ${skip[@]+"${skip[@]}"} ${replace[@]+"${replace[@]}"}; do
	[ -n "${from[$s]-}" ] || die "no skill $s in $root/skills"
done
for s in ${skip[@]+"${skip[@]}"}; do unset "from[$s]"; done
if [ "$tickets" != maestro-ticket ]; then
	[[ $tickets == /* ]] || tickets=$target/$tickets
	[ -d "$tickets" ] || die "install.yaml: tickets $tickets is not a directory"
	for entry in "$tickets"/*; do
		[ "$(basename "$entry")" = maestro-create-ticket ] || die "tickets: $tickets may hold only maestro-create-ticket/, not $(basename "$entry")"
	done
	skill "$tickets/maestro-create-ticket" "tickets: $tickets/maestro-create-ticket"
	[ -z "${from[maestro-create-ticket]-}" ] || from[maestro-create-ticket]=$tickets/maestro-create-ticket
fi

records=("$target/.maestro/project/installed.list" "$common/maestro/installed.list")
if [ "$home" = committed ]; then record=${records[0]} other=${records[1]}; else record=${records[1]} other=${records[0]}; fi
# A record path is from the repo's top, or from ~ (~/...) for user installs.
full() { if [[ $1 == '~/'* ]]; then echo "$HOME/${1#'~/'}"; else echo "$target/$1"; fi; }
prefix=
[ "$home" != user ] || prefix='~/'
# hashes: git blob hash of each path on stdin, one per line.
hashes() { git hash-object --stdin-paths; }

# old: the record. apply also reads the one an install in another
# skills-home left, so a switch removes the old copies.
declare -A old=()
reads=("$record")
[ "$cmd" != apply ] || reads+=("$other")
for r in "${reads[@]}"; do
	[ -f "$r" ] || continue
	while IFS=$'\t' read -r h p; do
		[[ $h == '#'* || -z $h ]] || old[$p]=$h
	done <"$r"
done

# prune <record path>...: remove the empty folders left in each skill
# folder the paths are in, that folder included, then the repo's harness
# folders if they're empty.
prune() {
	local p d
	for p in "$@"; do
		d=${p%/"${p#*/skills/*/}"}
		[ ! -d "$(full "$d")" ] || find "$(full "$d")" -depth -type d -empty -delete
	done
	for d in .claude .agents; do rmdir -- "$target/$d/skills" "$target/$d" 2>/dev/null || true; done
}

# The exclude lines between these markers are this script's.
begin="# maestro install: begin" end="# maestro install: end"
exclude_lines() {
	echo /.maestro/src/
	echo /.maestro/worktrees/
	echo /.maestro/bots/
	[ "$home" = hidden ] || return 0
	local n d
	for n in $(printf '%s\n' "${!from[@]}" | sort); do
		for d in "${dirs[@]}"; do echo "/$d/skills/$n"; done
	done
}
# write_exclude <lines>: replace the marked block; no lines drops it.
write_exclude() {
	mkdir -p "$(dirname "$exclude")"
	touch "$exclude"
	local tmp
	tmp=$(mktemp "$exclude.XXXXXX")
	awk -v b="$begin" -v e="$end" '$0==b{skip=1;next} $0==e{skip=0;next} !skip' "$exclude" >"$tmp"
	if [ -n "$1" ]; then
		[ ! -s "$tmp" ] || [ -z "$(tail -c1 "$tmp")" ] || echo >>"$tmp"
		printf '%s\n%s\n%s\n' "$begin" "$1" "$end" >>"$tmp"
	fi
	mv "$tmp" "$exclude"
}

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
		if [ ${#paths[@]} -gt 0 ]; then
			i=0
			while read -r h; do
				[ "$h" = "${want[i]}" ] || problem "changed since install: ${paths[i]}"
				i=$((i + 1))
			done < <(for p in "${paths[@]}"; do full "$p"; done | hashes)
		fi
	fi
	# The skills prompt steps load must be in every harness folder.
	steps=$(sed -n '/^prompts:/,/^[a-z]/p' "$root/skills/use.yaml" | grep -o '\[[^]]*\]' | tr -d '[],' | tr ' ' '\n' | sort -u || true)
	for n in $steps; do
		for d in "${dirs[@]}"; do
			[ -f "$base/$d/skills/$n/SKILL.md" ] || problem "$prefix$d/skills/$n is missing: playbook steps load it"
		done
	done
	if [ "$home" = hidden ]; then
		for n in "${!from[@]}"; do
			for d in "${dirs[@]}"; do
				git -C "$target" check-ignore -q "$d/skills/$n" || problem "$d/skills/$n isn't hidden from git"
			done
		done
	fi
	for b in maestro-ticket maestro-play; do
		bin=$(command -v "$b") || { problem "$b isn't on PATH"; continue; }
		command -v go >/dev/null || continue
		rev=$(go version -m "$bin" 2>/dev/null | awk '$1=="build" && $2 ~ /^vcs.revision=/ {sub(/^vcs.revision=/, "", $2); print $2}' || true)
		if [ -z "$rev" ]; then
			warn "$b has no build revision; build it from $root/tools/$b"
		elif [[ $rev != "$sha"* ]] && git -C "$root" merge-base --is-ancestor "$rev" "$sha" 2>/dev/null; then
			warn "$b is built from ${rev:0:12}, older than the pinned $sha; rebuild it from $root/tools/$b"
		fi
	done
	if [ "$root" = "$target/.maestro/src" ] && command -v maestro-play >/dev/null; then
		[ -z "${MAESTRO_PLAYBOOKS-}" ] || [ "$MAESTRO_PLAYBOOKS" = "$root/playbooks" ] ||
			warn "MAESTRO_PLAYBOOKS=$MAESTRO_PLAYBOOKS overrides $root/playbooks"
		(cd "$target" && MAESTRO_PLAYBOOKS='' maestro-play lint >/dev/null 2>&1) || problem "maestro-play lint fails in $target"
	fi
	setup=$target/.maestro/project/project-setup.md
	if [ ! -f "$setup" ]; then
		problem "no .maestro/project/project-setup.md"
	else
		main=$(awk '/^## /{s=$0; next} s=="## Main branch" && NF {print; exit}' "$setup")
		[ -n "$main" ] || problem "project-setup.md: no branch under ## Main branch"
		[ -z "$main" ] || git -C "$target" rev-parse --verify --quiet "refs/heads/$main" >/dev/null ||
			git -C "$target" rev-parse --verify --quiet "refs/remotes/origin/$main" >/dev/null ||
			problem "project-setup.md: main branch $main doesn't exist"
		awk '/^## /{s=$0; next} s=="## Checks" && /^- /{f=1} END{exit !f}' "$setup" ||
			warn "project-setup.md lists no checks: the work playbook runs none"
		awk '/^## /{s=substr($0, 4); next} /^<.*>$/ {print s}' "$setup" | sort -u | while read -r s; do
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
	kept=() removed=0 paths=() want=()
	for p in "${!old[@]}"; do
		[ -f "$(full "$p")" ] && paths+=("$p") want+=("${old[$p]}")
	done
	i=0
	while read -r h; do
		f=$(full "${paths[i]}")
		if [ "$h" = "${want[i]}" ]; then
			rm -f -- "$f"
			removed=$((removed + 1))
		else
			kept+=("${paths[i]}")
		fi
		i=$((i + 1))
	done < <(for p in ${paths[@]+"${paths[@]}"}; do full "$p"; done | hashes)
	[ ${#old[@]} -eq 0 ] || prune "${!old[@]}"
	write_exclude ""
	rm -f -- "$record"
	echo "removed $removed files"
	echo "left in place: .maestro/project/ (install.yaml, project-setup.md), .maestro/src/ and the ticket store at $common/maestro/"
	if [ ${#kept[@]} -gt 0 ]; then
		printf 'kept, changed since install: %s\n' "${kept[@]}"
		exit 1
	fi
	exit 0
fi

# --- apply ---
for p in .claude .claude/skills .agents .agents/skills .maestro .maestro/project; do
	# A copy behind a symlink would land outside the repo, where exclude can't hide it.
	[ "$home" = user ] && [[ $p == .claude* || $p == .agents* ]] && continue
	[ ! -L "$target/$p" ] || die "$target/$p is a symlink; install writes only inside the repo"
done

# The plan: every file to place, keyed by its record path.
declare -A src=() new=()
plan=()
for n in "${!from[@]}"; do
	while IFS= read -r -d '' f; do
		rel=${f#"${from[$n]}/"}
		for d in "${dirs[@]}"; do
			p=$prefix$d/skills/$n/$rel
			src[$p]=$f
			plan+=("$p")
		done
	done < <(find "${from[$n]}" -type f ! -path '*/.git/*' -print0)
done
i=0
while read -r h; do
	new[${plan[i]}]=$h
	i=$((i + 1))
done < <(for p in "${plan[@]}"; do echo "${src[$p]}"; done | hashes)

# Every file now in a folder this install writes to, or listed in the
# record, is in the plan, the record, or neither (the team's).
declare -A seen=()
for n in "${!from[@]}"; do
	for d in "${dirs[@]}"; do
		dir=$base/$d/skills/$n
		if [ -L "$dir" ]; then
			seen["$prefix$d/skills/$n"]=link
		elif [ -d "$dir" ]; then
			while IFS= read -r -d '' f; do
				seen["$prefix${f#"$base/"}"]=1
			done < <(find "$dir" \( -type f -o -type l \) -print0)
		fi
	done
done
for p in "${!old[@]}"; do [ ! -e "$(full "$p")" ] && [ ! -L "$(full "$p")" ] || seen[$p]=1; done
cur_paths=()
for p in "${!seen[@]}"; do [ "${seen[$p]}" = link ] || [ -L "$(full "$p")" ] || cur_paths+=("$p"); done
declare -A cur=()
i=0
while read -r h; do
	cur[${cur_paths[i]}]=$h
	i=$((i + 1))
done < <(for p in ${cur_paths[@]+"${cur_paths[@]}"}; do full "$p"; done | hashes)

# skill_of <path>: the skill folder name in a record path.
skill_of() { local p=${1#'~/'}; p=${p#*/skills/}; echo "${p%%/*}"; }
replacing() { [[ " ${replace[*]-} " == *" $(skill_of "$1") "* ]]; }
clashes=() writes=() deletes=()
for p in "${!seen[@]}"; do
	if [ "${seen[$p]}" = link ] || [ -L "$(full "$p")" ]; then
		if replacing "$p"; then deletes+=("$p"); else clashes+=("$p is a symlink"); fi
	elif [ -n "${new[$p]-}" ]; then
		if [ "${cur[$p]}" = "${new[$p]}" ]; then :
		elif [ "${cur[$p]}" = "${old[$p]-}" ] || replacing "$p"; then writes+=("$p")
		elif [ -n "${old[$p]-}" ]; then clashes+=("$p changed since install")
		else clashes+=("$p isn't Maestro's"); fi
	elif [ "${cur[$p]}" = "${old[$p]-}" ] || replacing "$p"; then deletes+=("$p")
	elif [ -n "${old[$p]-}" ]; then clashes+=("$p changed since install, and Maestro no longer installs it")
	else clashes+=("$p isn't Maestro's"); fi
done
for p in "${plan[@]}"; do [ -n "${seen[$p]-}" ] || writes+=("$p"); done
if [ ${#clashes[@]} -gt 0 ]; then
	printf 'clash: %s\n' "${clashes[@]}" | sort >&2
	die "nothing was written; keep the team's files with skip: <skill> in install.yaml, or replace them with --replace <skill>"
fi

# The store lives in the git common dir, outside the work tree; doctor
# creates it and checks the repo can hold one, before anything is written.
if [ "$tickets" = maestro-ticket ]; then
	command -v maestro-ticket >/dev/null || die "maestro-ticket isn't on PATH: build it from $root/tools/maestro-ticket"
	(cd "$target" && maestro-ticket doctor >/dev/null) || die "maestro-ticket doctor failed in $target; nothing was written"
fi

for p in ${deletes[@]+"${deletes[@]}"}; do rm -rf -- "$(full "$p")"; done
for p in ${writes[@]+"${writes[@]}"}; do
	f=$(full "$p")
	[ ! -L "$(dirname "$f")" ] || rm -f -- "$(dirname "$f")"
	mkdir -p "$(dirname "$f")"
	rm -f -- "$f"
	cp -- "${src[$p]}" "$f"
	chmod u+w "$f" # a read-only source stays removable
done
prune ${deletes[@]+"${deletes[@]}"}
mkdir -p "$target/.maestro/project"
conf=$target/.maestro/project/project-setup.md
[ -e "$conf" ] || [ -L "$conf" ] || cp "$root/templates/project-setup.md" "$conf"
write_exclude "$(exclude_lines)"
mkdir -p "$(dirname "$record")"
tmp=$(mktemp "$record.XXXXXX")
{
	echo "# Files scripts/install.sh placed at Maestro $sha: git blob hash, tab, path."
	for p in "${plan[@]}"; do printf '%s\t%s\n' "${new[$p]}" "$p"; done | sort -k2
} >"$tmp"
mv "$tmp" "$record"
rm -f -- "$other"

[ "$tickets" = maestro-ticket ] && echo "ticket store ready" || echo "ticket method from $tickets"
echo "installed ${#from[@]} skills into ${dirs[*]} ($home): ${#writes[@]} files written, ${#deletes[@]} removed"
echo "record: $record"
