#!/usr/bin/env bash
# Write one paste-ready block per Grok Bot into <repo>/.maestro/bots/:
# the profile's persona, the team rules, its starting memories and where
# it works.
#
#   scripts/bot-bundle.sh <repo>
#
# The bots are the bots: line of <repo>/.maestro/project/install.yaml, or
# every folder in bot-profiles/. A team's own memories for a bot live in
# <repo>/.maestro/project/bots/<profile>.memory.md and replace the
# profile's memory.md; only their bullets go in the block. Run
# scripts/install.sh apply first: it hides .maestro/bots/ from git.
set -euo pipefail
shopt -s nullglob
unset CDPATH
# shellcheck disable=SC2046 # one variable name per word
unset $(git rev-parse --local-env-vars)

die() { echo "bot-bundle.sh: $*" >&2; exit 1; }
[ $# -eq 1 ] || { echo "usage: scripts/bot-bundle.sh <repo>" >&2; exit 2; }
root=$(cd -- "$(dirname -- "$0")/.." && pwd)
profiles=$root/bot-profiles
[ -d "$1" ] || die "$1 is not a directory"
target=$(cd -- "$1" && pwd -P)
git -C "$target" check-ignore -q .maestro/bots/x || die ".maestro/bots/ isn't hidden from git: run scripts/install.sh apply first"
choices=$target/.maestro/project/install.yaml
[ -f "$choices" ] || die "no $choices"

bots=() sha=
while IFS= read -r line || [ -n "$line" ]; do
	line=${line%$'\r'} # a CRLF checkout
	case $line in
	bots:*) read -ra bots <<<"${line#bots:}" ;;
	maestro-sha:*) sha=${line#maestro-sha:} sha=${sha//[[:space:]]/} ;;
	esac
done <"$choices"
# The blocks must be the pinned commit's, as install.sh's copies are.
[[ $sha =~ ^[0-9a-f]{7,40}$ ]] || die "install.yaml: maestro-sha wants a commit sha"
head=$(git -C "$root" rev-parse HEAD 2>/dev/null) || die "$root isn't a git checkout"
[[ $head == "$sha"* ]] || die "Maestro at $root is on ${head:0:12}, but install.yaml pins $sha"
[ -z "$(git -C "$root" status --porcelain -- bot-profiles)" ] ||
	die "Maestro at $root has uncommitted changes in bot-profiles/; commit them and pin that commit"
if [ ${#bots[@]} -eq 0 ]; then
	for d in "$profiles"/*/; do bots+=("$(basename "$d")"); done
fi

# body <file>: the file without its front matter.
body() { awk 'NR==1 && $0=="---" {fm=1; next} fm && $0=="---" {fm=0; next} !fm' "$1"; }
# bullets <file>: the bullets of a memory file, each with its wrapped lines.
bullets() { awk '/^- /{b=1; print; next} b && /^  /{print; next} {b=0}' "$1"; }

# linked <path from the repo's top>: true if the path, or a folder on the
# way to it, is a symlink. A PR can commit one, and a read or write
# through it lands outside .maestro/.
linked() {
	local p=$1
	while :; do
		[ ! -L "$target/$p" ] || return 0
		[[ $p == */* ]] || return 1
		p=${p%/*}
	done
}

mkdir -p "$target/.maestro/bots"
for b in "${bots[@]}"; do
	persona=$profiles/$b/persona.md
	# The name goes into paths, so no / or ..
	{ [[ $b =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] && [ -f "$persona" ]; } || die "no bot profile $b in $profiles"
	memory=$target/.maestro/project/bots/$b.memory.md
	! linked ".maestro/project/bots/$b.memory.md" || die ".maestro/project/bots/$b.memory.md is a symlink; copy the real file"
	[ -f "$memory" ] || memory=$profiles/$b/memory.md
	out=$target/.maestro/bots/$b.md
	! linked ".maestro/bots/$b.md" || die ".maestro/bots/$b.md is a symlink; remove it"
	# The x keeps the final newlines, so the block compares exactly.
	text=$(
		body "$persona"
		echo
		body "$profiles/team-rules.md"
		if [ -f "$memory" ] && [ -n "$(bullets "$memory")" ]; then
			printf '\n## Starting memories\n\nSave each bullet as one memory the first time you start.\n\n'
			bullets "$memory"
		fi
		printf '\n## Where you work\n\n- Repo: %s\n- Profile: %s\n- Maestro: %s\n' "$target" "$b" "$sha"
		echo x
	)
	text=${text%x}
	# The person pastes again only the blocks that changed.
	if [ ! -f "$out" ]; then state=new
	elif [ "$(cat "$out"; echo x)" = "${text}x" ]; then state=same
	else state=changed
	fi
	[ "$state" = same ] || printf '%s' "$text" >"$out"
	echo "$b: $state, $(wc -c <"$out" | tr -d ' ') chars in .maestro/bots/$b.md"
done
# A block for a bot no longer listed would be pasted by mistake.
for d in "$profiles"/*/; do
	p=$(basename "$d") out=$target/.maestro/bots/$p.md
	[[ " ${bots[*]} " == *" $p "* ]] || [ ! -f "$out" ] || linked ".maestro/bots/$p.md" || {
		rm -f -- "$out"
		echo "$p: removed .maestro/bots/$p.md: not in bots:"
	}
done
