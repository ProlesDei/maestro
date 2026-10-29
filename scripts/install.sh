#!/usr/bin/env bash
# Install Maestro's footprint into a git repo (decisions 0880, 0881).
#
#   scripts/install.sh <target-repo>
#
# Copies every skills/<name>/ into <target>/.claude/skills/ (Claude Code)
# and <target>/.agents/skills/ (Codex, Grok), creates .maestro/project/
# and .maestro/local/, hides the skill folders and .maestro/local/ from git
# through .git/info/exclude, and creates the ticket store if maestro-ticket
# is on PATH. <target-repo> must be the top of a git work tree. If any skill
# folder already exists it refuses and writes nothing; if a later step
# fails it removes what it wrote.
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

[ $# -eq 1 ] || die "usage: scripts/install.sh <target-repo>"
src=$(cd -- "$(dirname -- "$0")/.." && pwd)/skills
[ -d "$1" ] || die "$1 is not a directory"
target=$(cd -- "$1" && pwd -P)
top=$(git -C "$target" rev-parse --show-toplevel) || die "$1 is not in a git work tree"
[ "$top" = "$target" ] || die "$1 is inside the repo at $top; install at the top of the repo"
exclude=$(git -C "$target" rev-parse --path-format=absolute --git-path info/exclude)
[[ $exclude == /* && $exclude != *$'\n'* ]] || die "needs git 2.31 or later"
for p in .claude .claude/skills .agents .agents/skills .maestro; do
	# Copies behind a symlink would land outside the repo, where exclude can't hide them.
	[ ! -L "$target/$p" ] || die "$target/$p is a symlink; install writes only inside the repo"
done

names=()
for dir in "$src"/*/; do
	[ -d "$dir" ] || continue
	name=$(basename "$dir")
	# The name goes into paths and exclude lines, so no glob or space characters.
	[[ $name =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] && [ -f "$dir/SKILL.md" ] ||
		die "skills/$name isn't a skill: it needs a lowercase-hyphen name and a SKILL.md"
	names+=("$name")
done
[ ${#names[@]} -gt 0 ] || die "no skills in $src"

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
	for name in "${names[@]}"; do
		mkdir "$target/$harness/skills/$name" # fails, never merges, if it appeared since the check
		made+=("$target/$harness/skills/$name")
		cp -R "$src/$name/." "$target/$harness/skills/$name/"
		chmod -R u+w "$target/$harness/skills/$name" # a read-only source stays removable
	done
done
mkd "$target/.maestro/project"
mkd "$target/.maestro/local"

mkdir -p "$(dirname "$exclude")"
[ ! -s "$exclude" ] || [ -z "$(tail -c1 "$exclude")" ] || echo >>"$exclude"
lines=(.maestro/local/)
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
echo "installed ${#names[@]} skills into $target"
