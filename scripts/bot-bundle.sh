#!/usr/bin/env bash
# Write one paste-ready block per Grok Bot into <repo>/.maestro/bots/:
# the profile's persona, the team rules and where it works, refused over
# Grok's 20,000-character limit for a bot's instructions. A bot with
# starting memories also gets <profile>.memories.md, a message to send it
# once its instructions are in, and again each time they change.
#
#   scripts/bot-bundle.sh <repo>
#
# The bots are the bots: line of <repo>/.maestro/project/install.yaml, or
# every folder in bot-profiles/. A team's own memories for a bot live in
# <repo>/.maestro/project/bots/<profile>.memory.md and replace the
# profile's memory.md; only their bullets go in the message. Run
# scripts/install.sh apply first: it hides .maestro/bots/ from git.
set -euo pipefail
die() { echo "bot-bundle.sh: $*" >&2; exit 1; }
((BASH_VERSINFO[0] * 100 + BASH_VERSINFO[1] >= 404)) || die "needs bash 4.4 or later (macOS: brew install bash)"
# A failed read inside $(...) stops the run, not a short block.
shopt -s nullglob inherit_errexit
unset CDPATH
# shellcheck disable=SC2046 # one variable name per word
unset $(git rev-parse --local-env-vars)

[ $# -eq 1 ] || { echo "usage: scripts/bot-bundle.sh <repo>" >&2; exit 2; }
root=$(cd -- "$(dirname -- "$0")/.." && pwd)
profiles=$root/bot-profiles
[ -d "$1" ] || die "$1 is not a directory"
target=$(cd -- "$1" && pwd -P)
git -C "$target" rev-parse --git-dir >/dev/null || die "git can't read the repo at $target"
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

# block <bot>: the bot's instructions. The x keeps the final newlines, so
# the block compares exactly; strip it after the command substitution.
block() {
	body "$profiles/$1/persona.md"
	echo
	body "$profiles/team-rules.md"
	printf '\n## Where you work\n\n- Repo: %s\n- Profile: %s\n- Maestro: %s\n' "$target" "$1" "$sha"
	echo x
}
# bullets [file]: the bullets of a memory file, each with its wrapped lines.
bullets() { awk '/^- /{b=1; print; next} b && /^  /{print; next} {b=0}' "$@"; }
# memories <memory file> <old message> <old block>: the message that sets
# the bot's starting memories, or nothing. The bot marks each one
# Starting:, so the next message replaces the set. The list is the
# owner's word: each bullet takes the place of any memory on its topic,
# and the bot says which it replaced, so whoever set that one can set it
# again. A bot with none set gets a message only if it had some: from an
# earlier message, or from an older block's ## Starting memories.
memories() {
	local notes=''
	[ ! -f "$1" ] || notes=$(bullets "$1")
	[ -n "$notes" ] || [ -f "$2" ] || { [ -f "$3" ] && grep -qx '## Starting memories' "$3"; } || return 0
	# shellcheck disable=SC2016 # literal backticks
	printf 'Forget your old starting memories: each starts `Starting:`, or you saved it from a `## Starting memories` list before the mark.'
	if [ -n "$notes" ]; then
		printf " Then save each bullet below as one memory that starts \`Starting:\`. They are the owner's: each takes the place of any memory on its topic; tell whoever hands you jobs which you replaced.\n\n%s\n" "$notes"
	else
		printf ' Your team set none in their place.\n'
	fi
}
# state <file> <text> [block]: new, same or changed. A block's last line,
# its Maestro line, changes on every update, so it doesn't count: a block
# that differs only there is the same, and no one pastes it again.
state() {
	local was t=${2}x
	[ -f "$1" ] || { echo new; return; }
	was=$(cat "$1"; echo x)
	[ -z "${3:-}" ] || was=${was%- Maestro: *} t=${t%- Maestro: *}
	if [ "$was" = "$t" ]; then echo same; else echo changed; fi
}
files=(.md .memories.md) # each bot's, in .maestro/bots/

# Build and check every bot's files before writing any, so a refusal
# leaves .maestro/bots/ as it was.
blocks=() msgs=()
for b in "${bots[@]}"; do
	# The name goes into paths, so no / or ..
	{ [[ $b =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] && [ -f "$profiles/$b/persona.md" ]; } || die "no bot profile $b in $profiles"
	! linked ".maestro/project/bots/$b.memory.md" || die ".maestro/project/bots/$b.memory.md is a symlink; copy the real file"
	for f in "${files[@]}"; do
		! linked ".maestro/bots/$b$f" || die ".maestro/bots/$b$f is a symlink; remove it"
	done
	text=$(block "$b") text=${text%x}
	size=$(LC_ALL=C; echo ${#text}) # bytes: never fewer than characters
	[ "$size" -le 20000 ] || die "the $b block is $size bytes, over Grok's 20,000-character limit for a bot's instructions; Maestro's persona and team rules must leave room for the repo path"
	memory=$target/.maestro/project/bots/$b.memory.md
	[ -f "$memory" ] || memory=$profiles/$b/memory.md
	msg=$(memories "$memory" "$target/.maestro/bots/$b.memories.md" "$target/.maestro/bots/$b.md"; echo x)
	blocks+=("$text") msgs+=("${msg%x}")
done

mkdir -p "$target/.maestro/bots"
for i in "${!bots[@]}"; do
	b=${bots[$i]} text=${blocks[$i]} msg=${msgs[$i]}
	out=$target/.maestro/bots/$b.md mem=$target/.maestro/bots/$b.memories.md
	st=$(state "$out" "$text" block)
	[ "$st" = same ] || printf '%s' "$text" >"$out"
	echo "$b: $st, $(wc -c <"$out" | tr -d ' ') bytes in .maestro/bots/$b.md"
	# Memories go in a message apart from the block: Grok caps the block.
	[ -n "$msg" ] || continue
	st=$(state "$mem" "$msg")
	[ "$st" = same ] || printf '%s' "$msg" >"$mem"
	if [ "$st" = same ]; then echo "$b memories: same"
	else echo "$b memories: $st, send .maestro/bots/$b.memories.md to the bot as a message"
	fi
done
# The files of a bot no longer listed would be pasted by mistake.
for d in "$profiles"/*/; do
	p=$(basename "$d")
	[[ " ${bots[*]} " != *" $p "* ]] || continue
	for f in "${files[@]}"; do
		[ ! -f "$target/.maestro/bots/$p$f" ] || linked ".maestro/bots/$p$f" || {
			rm -f -- "$target/.maestro/bots/$p$f"
			echo "$p: removed .maestro/bots/$p$f: not in bots:"
		}
	done
done
