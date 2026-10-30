#!/usr/bin/env bash
# Install Maestro's footprint into a git repo.
#
#   scripts/install.sh [--tickets <dir>] <target-repo>
#
# Copies every skills/<name>/ into <target>/.claude/skills/ (Claude Code)
# and <target>/.agents/skills/ (Codex, Grok), writes
# .maestro/project/project-setup.md from templates/ if it's missing, hides
# the skill folders from git through .git/info/exclude, and creates the
# ticket store if
# maestro-ticket is on PATH. <target-repo> must be the top of a git work
# tree. If any skill folder already exists it refuses and writes nothing;
# if a later step fails it removes what it wrote.
#
# --tickets <dir>: a team's own ticket method. <dir> holds
# maestro-create-ticket/ with a SKILL.md, installed in place of Maestro's
# folder of the same name, the name the personas call. Any other entry in
# <dir> is refused; dot files are skipped.
#
# Uninstall, from the target repo. The first line removes the skills; the
# second removes your project files and the ticket store, with every ticket:
#   for d in <maestro>/skills/*/; do n=$(basename "$d"); rm -rf ".claude/skills/$n" ".agents/skills/$n"; done
#   rm -rf .maestro "$(git rev-parse --git-common-dir)/maestro"
set -euo pipefail
unset CDPATH
# A GIT_DIR or GIT_WORK_TREE from the caller would point git at another repo.
unset $(git rev-parse --local-env-vars)

die() { echo "install.sh: $*" >&2; exit 1; }

usage="usage: scripts/install.sh [--tickets <dir>] <target-repo>"
tickets=
if [ "${1-}" = --tickets ]; then
	[ $# -ge 2 ] || die "$usage"
	[ -n "$2" ] || die "--tickets needs a directory"
	tickets=$2
	shift 2
fi
[ $# -eq 1 ] || die "$usage"
root=$(cd -- "$(dirname -- "$0")/.." && pwd)
src=$root/skills
setup=$root/templates/project-setup.md
[ -f "$setup" ] || die "$setup is missing"
[ -d "$1" ] || die "$1 is not a directory"
target=$(cd -- "$1" && pwd -P)
top=$(git -C "$target" rev-parse --show-toplevel) || die "$1 is not in a git work tree"
[ "$top" = "$target" ] || die "$1 is inside the repo at $top; install at the top of the repo"
exclude=$(git -C "$target" rev-parse --path-format=absolute --git-path info/exclude)
[[ $exclude == /* && $exclude != *$'\n'* ]] || die "needs git 2.31 or later"
for p in .claude .claude/skills .agents .agents/skills .maestro .maestro/project; do
	# Copies behind a symlink would land outside the repo, where exclude can't hide them.
	[ ! -L "$target/$p" ] || die "$target/$p is a symlink; install writes only inside the repo"
done

# skill <dir> <label>: die unless <dir> is a skill folder. The name goes
# into paths and exclude lines, so no glob or space characters.
skill() {
	{ [[ $(basename "$1") =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] && [ -d "$1" ] && [ ! -L "$1" ] && [ -f "$1/SKILL.md" ]; } ||
		die "$2 isn't a skill: it needs a lowercase-hyphen name and a SKILL.md"
}

# from[i] is the folder names[i] is copied from: skills/, or --tickets.
names=() from=()
for dir in "$src"/*/; do
	[ -d "$dir" ] || continue
	name=$(basename "$dir")
	skill "$src/$name" "skills/$name"
	names+=("$name")
	from+=("$src/$name")
done
[ ${#names[@]} -gt 0 ] || die "no skills in $src"

if [ -n "$tickets" ]; then
	[ -d "$tickets" ] || die "--tickets $tickets is not a directory"
	tickets=$(cd -- "$tickets" && pwd)
	[ -e "$tickets/maestro-create-ticket" ] || die "--tickets $tickets has no maestro-create-ticket/"
	for entry in "$tickets"/*; do
		name=$(basename "$entry")
		case $name in
		maestro-create-ticket) skill "$entry" "--tickets $entry" ;;
		*) die "--tickets $tickets may hold only maestro-create-ticket/, not $name" ;;
		esac
		for i in "${!names[@]}"; do
			[ "${names[i]}" != "$name" ] || from[i]=$entry
		done
	done
fi

clash=0
for name in "${names[@]}"; do
	for harness in .claude .agents; do
		if [ -e "$target/$harness/skills/$name" ] || [ -L "$target/$harness/skills/$name" ]; then
			echo "install.sh: $target/$harness/skills/$name already exists" >&2
			clash=1
		fi
	done
done
[ $clash -eq 0 ] || die "refusing to install; nothing was written"

# The store lives in the git common dir, outside the work tree; doctor
# creates it and checks the repo can hold one, before anything is written.
store=
if command -v maestro-ticket >/dev/null; then
	(cd "$target" && maestro-ticket doctor) || die "maestro-ticket doctor failed in $target; nothing was written"
	store=ready
fi

# What this run creates, undone if it stops before the end: the skill
# folders are removed, and the folders around them only if they're empty,
# so a file someone else wrote there meanwhile stays.
made=() dirs=()
undo() {
	local i
	if [ ${#made[@]} -gt 0 ]; then
		chmod -R u+w -- "${made[@]}" 2>/dev/null || true
		rm -rf -- "${made[@]}" || true
	fi
	for ((i = ${#dirs[@]} - 1; i >= 0; i--)); do
		rmdir -- "${dirs[i]}" 2>/dev/null || true
	done
}
trap 'status=$?; [ $status -eq 0 ] || undo; exit $status' EXIT
trap 'exit 1' INT TERM HUP
mkd() { # mkdir -p, remembering each directory it creates
	local d=$1 new=()
	while [ ! -e "$d" ] && [ ! -L "$d" ]; do
		new=("$d" ${new[@]+"${new[@]}"})
		d=$(dirname "$d")
	done
	dirs+=(${new[@]+"${new[@]}"})
	mkdir -p "$1"
}

for harness in .claude .agents; do
	mkd "$target/$harness/skills"
	for i in "${!names[@]}"; do
		dest=$target/$harness/skills/${names[i]}
		mkdir "$dest" # fails, never merges, if it appeared since the check
		made+=("$dest")
		cp -R "${from[i]}/." "$dest/"
		chmod -R u+w "$dest" # a read-only source stays removable
	done
done
mkd "$target/.maestro/project"
# The project's file once written: never overwritten.
conf=$target/.maestro/project/project-setup.md
if [ ! -e "$conf" ] && [ ! -L "$conf" ]; then
	made+=("$conf")
	cp "$setup" "$conf"
fi

mkdir -p "$(dirname "$exclude")"
[ ! -s "$exclude" ] || [ -z "$(tail -c1 "$exclude")" ] || echo >>"$exclude"
lines=()
for name in "${names[@]}"; do
	lines+=(".claude/skills/$name/" ".agents/skills/$name/")
done
for line in "${lines[@]}"; do
	grep -qxF -- "$line" "$exclude" 2>/dev/null || echo "$line" >>"$exclude"
done

if [ -n "$store" ]; then
	echo "ticket store ready"
else
	echo "maestro-ticket isn't on PATH: run 'go install ./cmd/maestro-ticket' in tools/maestro-ticket/, then 'maestro-ticket doctor' in $target"
fi
[ -z "$tickets" ] || echo "ticket method from $tickets"
echo "installed ${#names[@]} skills into $target"
echo "add to your shell profile: export MAESTRO_PLAYBOOKS=$root/playbooks"
