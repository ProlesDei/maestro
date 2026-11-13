#!/usr/bin/env bash
# shellcheck disable=SC2016,SC2015,SC2012,SC2035 # literal backticks, a && b || fail, ls of known names
# Acceptance test for scripts/install.sh and scripts/bot-bundle.sh, run
# against a copy of Maestro in its own git repo so an update can change it.
# apply installs hidden or committed, and a second run changes nothing; an
# update replaces and removes only what it placed, and leaves a team's
# file beside them; a team's file in the way, or one changed since
# install, clashes and changes nothing, until --replace; a dropped skill
# the team changed stays as theirs; a folder where a file goes; an
# install by the install.sh before the record is taken over, a copy a
# merge made too; a switch of
# skills-home takes over the copies, and a switch to hidden untracks them;
# a CRLF source or checkout, the record's too, still matches the record, from any
# directory; check finds what's wrong, also through a symlinked path, and
# reads project-setup.md, changes nothing, and names a store that is no
# SQLite file, a missing tool, git identity or origin, and committed skills
# not yet on main; a git too old for review refuses apply and check, not
# uninstall; a linked worktree refuses, and one that holds
# a stray .maestro/src/ says to delete it; a repo git doesn't trust shows
# git's reason; a bare repo works under safe.bareRepository=explicit; a
# bot block that differs only in its Maestro line is the same, one over
# Grok's limit refuses, and memories go in a message apart; uninstall
# runs off the pin;
# uninstall removes only unchanged files and keeps .maestro/src/ hidden; a
# failed copy stays hidden and a rerun finishes it; a file git can't hash
# stops check and uninstall; harnesses without claude stop apply and
# check; bad choices and bad repos refuse and change nothing.
# Run from anywhere: bash scripts/install_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { cat "$tmp/out" 2>/dev/null || true; echo "FAIL: $*" >&2; exit 1; }
export GIT_CONFIG_GLOBAL=$tmp/gitconfig GIT_CONFIG_NOSYSTEM=1
printf '[user]\n\tname = t\n\temail = t@t\n[init]\n\tdefaultBranch = main\n' >"$GIT_CONFIG_GLOBAL"

# A Maestro to install from: the parts INSTALL.md checks out.
m=$tmp/maestro
mkdir "$m"
cp -R "$here/../skills" "$here/../templates" "$here/../bot-profiles" "$m/"
mkdir "$m/scripts" && cp "$here/install.sh" "$here/bot-bundle.sh" "$m/scripts/"
touch "$m/templates/empty" # an empty blob in Maestro's history
# Filled in, as INSTALL.md step 6 leaves it, so check passes; the real
# template's placeholder is tested below.
sed -i 's/^<each check .*>$/- test: `true`/' "$m/templates/project-setup.md"
grep -qx -- '- test: `true`' "$m/templates/project-setup.md" || fail "the template's ## Checks placeholder moved"
# Decision 12: a headless run can't answer a permission prompt.
if grep -rn 'claude -p' "$m"/bot-profiles/*/persona.md | grep -v -- '--permission-mode bypassPermissions' >"$tmp/out"; then
	fail "a persona's claude -p runs without all permissions"
fi
for f in "$m"/scripts/*.sh; do [ -x "$f" ] || fail "${f##*/} isn't executable; INSTALL.md runs it as a command"; done
git -C "$m" init -q && git -C "$m" add -A && git -C "$m" commit -q -m one
sha=$(git -C "$m" rev-parse --short HEAD)
skills=$(cd "$m/skills" && ls -d */ | tr -d / | sort)
nskills=$(echo "$skills" | wc -l)

# A stand-in maestro-ticket: prints a help that lists log-run --brief,
# or, when STUB_OLD is set, an older log-run without it; logs where any other command ran, fails when STUB_FAIL
# is set, and otherwise makes the store as doctor does.
mkdir "$tmp/bin"
# shellcheck disable=SC2016 # the stub's code, not ours
printf '%s\n' '#!/usr/bin/env bash' \
	'[ "$1" != help ] || { if [ -n "${STUB_OLD:-}" ]; then printf "Commands:\n  log-run     [--ticket ID]\n  list  [--brief]\n"; else printf "Commands:\n  log-run     [--ticket ID] [--brief]\n"; fi; exit 0; }' \
	'echo "$PWD $*" >>"$STUB_LOG"' \
	'[ -z "${STUB_FAIL:-}" ] || { echo "FAIL  stub: no store here"; exit 1; }' \
	'[ "$1" != doctor ] || { d=$(git rev-parse --path-format=absolute --git-common-dir)/maestro && mkdir -p "$d" && printf "SQLite format 3\0" >"$d/tickets.db"; }' >"$tmp/bin/maestro-ticket"
chmod +x "$tmp/bin/maestro-ticket"
# check wants what the bots' runs need; stand-ins, so the test passes on a
# machine without them.
for t in claude gh jq; do printf '#!/bin/sh\n' >"$tmp/bin/$t" && chmod +x "$tmp/bin/$t"; done
# Claude Code's settings, where check looks for code graph hooks: not the
# user's own.
export PATH="$tmp/bin:$PATH" STUB_LOG="$tmp/doctor.log" CLAUDE_CONFIG_DIR=$tmp/claude-config
: >"$STUB_LOG"

run() { bash "$m/scripts/install.sh" "$@" >"$tmp/out" 2>&1; }
ok() { run "$@" || { cat "$tmp/out"; fail "install.sh $* exited non-zero"; }; }
says() { grep -qF -- "$1" "$tmp/out" || { cat "$tmp/out"; fail "output lacks: $1"; }; }
# A tree snapshot: every path and file checksum, .git included.
snap() { (cd "$1" && find . -type f -print0 | sort -z | xargs -0 cksum && find . | sort); }
# refuses <message> <repo> <args...>: install.sh <args> exits non-zero,
# prints <message> and leaves <repo> as it was.
refuses() {
	local msg=$1 repo=$2 before
	shift 2
	before=$(snap "$repo")
	if run "$@"; then fail "install.sh $* exited 0"; fi
	says "$msg"
	[ "$before" = "$(snap "$repo")" ] || fail "install.sh $* changed $repo"
}
# hide <tool>: print a PATH that lacks <tool> and keeps every other
# command: each folder that holds it gives way to one of links to the rest.
hide() {
	local t=$1 d f out='' IFS=:
	for d in $PATH; do
		[ -n "$d" ] || continue
		if [ -e "$d/$t" ]; then
			f=$(mktemp -d "$tmp/farm.XXXXXX")
			ln -s "$d"/* "$f/" && rm "$f/$t"
			d=$f
		fi
		out=${out:+$out:}$d
	done
	echo "$out"
}
# repo <path> <choices...>: a new repo with one commit and these choices,
# and code-graph: none unless they give one.
repo() {
	local r=$1
	shift
	git init -q "$r"
	git -C "$r" commit -q --allow-empty -m init
	mkdir -p "$r/.maestro/project"
	printf '%s\n' "maestro-sha: $sha" "$@" >"$r/.maestro/project/install.yaml"
	[[ " $* " == *" code-graph: "* ]] || echo "code-graph: none" >>"$r/.maestro/project/install.yaml"
}
# installed <repo> <harness dir>...: every skill is there, as Maestro has it.
installed() {
	local r=$1 d n
	shift
	for d in "$@"; do
		for n in $skills; do
			diff -r "$m/skills/$n" "$r/$d/skills/$n" >/dev/null || fail "$r/$d/skills/$n differs from skills/$n"
		done
	done
}

# --- hidden: a space in the path, and a user exclude file with no final newline ---
r="$tmp/my repo"
repo "$r" "harnesses: claude codex grok" "skills-home: hidden" "bots: planner reviewer"
printf '# mine\n*.log' >"$r/.git/info/exclude"
ok apply "$r"
installed "$r" .claude .agents
cmp -s "$m/templates/project-setup.md" "$r/.maestro/project/project-setup.md" || fail "project-setup.md isn't the template"
grep -qxF '*.log' "$r/.git/info/exclude" || fail "the user's exclude line was changed"
mkdir -p "$r/.maestro/src" "$r/.maestro/worktrees/T-1" "$r/.maestro/bots" && touch "$r/.maestro/src/a" "$r/.maestro/worktrees/T-1/a" "$r/.maestro/bots/a"
seen=$(git -C "$r" status --porcelain --untracked-files=all)
[ "$seen" = "?? .maestro/project/install.yaml
?? .maestro/project/project-setup.md" ] || fail "git should see only .maestro/project: $seen"
[ "$(cat "$STUB_LOG")" = "$(cd "$r" && pwd -P) doctor" ] || fail "doctor didn't run once, in the repo: $(cat "$STUB_LOG")"
[ "$(grep -c . "$r/.git/maestro/installed.list")" -eq $(($(find "$m/skills" -mindepth 2 -type f | wc -l) * 2 + 1)) ] ||
	fail "the record should list every file in both harness folders"

before=$(snap "$r")
ok apply "$r"
[ "$before" = "$(snap "$r")" ] || fail "a second apply changed the repo"

# check changes nothing, the store included: it runs no maestro-ticket
# command but help.
before=$(snap "$r")
: >"$STUB_LOG"
ok check "$r"
says "ok: hidden install of $nskills skills at $sha"
[ "$before" = "$(snap "$r")" ] || fail "check changed the repo"
[ ! -s "$STUB_LOG" ] || fail "check ran maestro-ticket: $(cat "$STUB_LOG")"
# With no origin, no ticket's worktree has a main to branch from.
says "warn: no origin/main ref as of the last fetch"
# A store that is empty or no SQLite file, a tool the bots' runs need, and
# the identity they commit with: each is a problem.
cp "$r/.git/maestro/tickets.db" "$tmp/tickets.db"
: >"$r/.git/maestro/tickets.db"
refuses "problem: the ticket store at $(cd "$r" && pwd -P)/.git/maestro/tickets.db is empty, unreadable or isn't a SQLite file" "$r" check "$r"
echo garbage >"$r/.git/maestro/tickets.db"
refuses "unreadable or isn't a SQLite file" "$r" check "$r"
cp "$tmp/tickets.db" "$r/.git/maestro/tickets.db"
for t in claude gh jq; do PATH=$(hide $t) refuses "problem: $t isn't on PATH" "$r" check "$r"; done
# A maestro-ticket built before log-run would eat every bot's reply.
STUB_OLD=1 refuses "problem: maestro-ticket at $tmp/bin/maestro-ticket has no log-run --brief" "$r" check "$r"
GIT_CONFIG_GLOBAL=/dev/null refuses "problem: git user.name isn't set" "$r" check "$r"
says "problem: git user.email isn't set"
# A name of spaces commits nothing; an identity from the environment does.
git -C "$r" config user.name '   '
GIT_CONFIG_GLOBAL=/dev/null refuses "problem: git user.name isn't set" "$r" check "$r"
git -C "$r" config --unset user.name
GIT_CONFIG_GLOBAL=/dev/null GIT_AUTHOR_NAME=b GIT_AUTHOR_EMAIL=b@b GIT_COMMITTER_NAME=b GIT_COMMITTER_EMAIL=b@b ok check "$r"
GIT_CONFIG_GLOBAL=/dev/null GIT_AUTHOR_NAME=' ' GIT_AUTHOR_EMAIL=b@b GIT_COMMITTER_NAME=' ' GIT_COMMITTER_EMAIL=b@b refuses "problem: git user.name isn't set" "$r" check "$r"
# The code graph: mcp wants the server in .mcp.json and its binary on PATH;
# none warns on a server left there; its hooks warn either way.
sed -i 's/^code-graph: .*/code-graph: mcp/' "$r/.maestro/project/install.yaml"
refuses "problem: code-graph is mcp, but .mcp.json registers no codebase-memory-mcp server" "$r" check "$r"
PATH=$(hide codebase-memory-mcp) refuses "problem: codebase-memory-mcp isn't on PATH" "$r" check "$r"
printf '{\n  "mcpServers": {\n    "codebase-memory-mcp": {"command": "codebase-memory-mcp", "args": []}\n  }\n}\n' >"$r/.mcp.json"
printf '#!/bin/sh\n' >"$tmp/bin/codebase-memory-mcp" && chmod +x "$tmp/bin/codebase-memory-mcp"
ok check "$r"
grep -q codebase-memory "$tmp/out" && fail "check named the code graph when all was well"
mkdir -p "$CLAUDE_CONFIG_DIR"
printf '{"hooks": {"PreToolUse": [{"matcher": "Grep|Glob", "hooks": [{"type": "command", "command": "~/.claude/hooks/cbm-code-discovery-gate"}]}]}}\n' >"$CLAUDE_CONFIG_DIR/settings.json"
ok check "$r"
says "warn: $CLAUDE_CONFIG_DIR/settings.json runs codebase-memory-mcp hooks"
rm "$CLAUDE_CONFIG_DIR/settings.json"
sed -i 's/^code-graph: .*/code-graph: none/' "$r/.maestro/project/install.yaml"
ok check "$r"
says "warn: code-graph is none, but .mcp.json registers codebase-memory-mcp"
rm "$r/.mcp.json" "$tmp/bin/codebase-memory-mcp"
# Review and prune need git merge-tree --write-tree, from git 2.38.
# uninstall doesn't, so a team on an older git can still remove Maestro.
mkdir "$tmp/oldgit"
# shellcheck disable=SC2016 # the stub's code, not ours
printf '%s\n' '#!/usr/bin/env bash' '[ "${1-}" != --version ] || { echo "git version $FAKE_GIT"; exit 0; }' 'exec "$REAL_GIT" "$@"' >"$tmp/oldgit/git"
chmod +x "$tmp/oldgit/git"
oldgit() { local v=$1; shift; FAKE_GIT=$v REAL_GIT=$(command -v git) PATH=$tmp/oldgit:$PATH "$@"; }
for v in 2.37.1 "2.9.5 (Apple Git-1)" 1.99.0; do
	for c in apply check; do oldgit "$v" refuses "needs git 2.38 or later" "$r" $c "$r"; done
done
for v in 2.38.0 "2.39.3 (Apple Git-146)" 2.100.0 3.0.1; do oldgit "$v" ok check "$r"; done
# A clone that skipped apply has no store.
mv "$r/.git/maestro/tickets.db" "$tmp/tickets.db"
refuses "no ticket store at $(cd "$r" && pwd -P)/.git/maestro/tickets.db: run apply" "$r" check "$r"
mv "$tmp/tickets.db" "$r/.git/maestro/tickets.db"
MAESTRO_TICKET_STORE=tickets.db refuses "MAESTRO_TICKET_STORE is relative" "$r" check "$r"
# The template's ## Checks placeholder fails check: engineers would run it.
cp "$here/../templates/project-setup.md" "$r/.maestro/project/project-setup.md"
if run check "$r"; then fail "check passed the template's ## Checks text"; fi
says "problem: project-setup.md: ## Checks still holds the template's <...> text"
cp "$m/templates/project-setup.md" "$r/.maestro/project/project-setup.md"
echo mine >>"$r/.claude/skills/maestro-tdd/SKILL.md"
rm "$r/.agents/skills/maestro-swarm/SKILL.md"
if run check "$r"; then fail "check passed with a changed and a missing file"; fi
says "changed since install: .claude/skills/maestro-tdd/SKILL.md"
says "missing: .agents/skills/maestro-swarm/SKILL.md"

# A file changed since install clashes until --replace, which leaves a
# team's file beside it that no clash named.
echo notes >"$r/.claude/skills/maestro-tdd/NOTES.md"
refuses "clash: .claude/skills/maestro-tdd/SKILL.md changed since install" "$r" apply "$r"
ok apply --replace maestro-tdd "$r"
[ -f "$r/.claude/skills/maestro-tdd/NOTES.md" ] || fail "--replace removed a file no clash named"
rm "$r/.claude/skills/maestro-tdd/NOTES.md"
installed "$r" .claude .agents
ok check "$r"

# A file Maestro ships, but at another path, or an empty one, is the
# team's: it stays where it is, out of the record.
cp "$m/skills/maestro-swarm/SKILL.md" "$r/.claude/skills/maestro-tdd/swarm.md"
touch "$r/.claude/skills/maestro-tdd/empty.md"
ok apply "$r"
[ -f "$r/.claude/skills/maestro-tdd/swarm.md" ] && [ -f "$r/.claude/skills/maestro-tdd/empty.md" ] || fail "apply removed the team's files"
! grep -qE 'maestro-tdd/(swarm|empty)\.md' "$r/.git/maestro/installed.list" || fail "the team's files went in the record"
rm "$r/.claude/skills/maestro-tdd/swarm.md" "$r/.claude/skills/maestro-tdd/empty.md"

# A folder where Maestro places a file: an empty one goes, one with a
# file in it clashes.
rm "$r/.agents/skills/maestro-tdd/SKILL.md" && mkdir -p "$r/.agents/skills/maestro-tdd/SKILL.md/sub"
ok apply "$r"
installed "$r" .agents
rm "$r/.agents/skills/maestro-tdd/SKILL.md" && mkdir "$r/.agents/skills/maestro-tdd/SKILL.md" && touch "$r/.agents/skills/maestro-tdd/SKILL.md/x"
refuses "clash: .agents/skills/maestro-tdd/SKILL.md/x isn't Maestro's" "$r" apply "$r"
rm -r "$r/.agents/skills/maestro-tdd/SKILL.md"
ok apply "$r"
# A file where Maestro places a folder clashes too, before anything is written.
rm -r "$r/.agents/skills/maestro-code-review/references" && echo mine >"$r/.agents/skills/maestro-code-review/references"
refuses "clash: .agents/skills/maestro-code-review/references isn't Maestro's" "$r" apply "$r"
rm "$r/.agents/skills/maestro-code-review/references"
ok apply "$r"
# A file where a skill folder goes clashes, until --replace.
rm -r "$r/.agents/skills/maestro-tdd" && echo mine >"$r/.agents/skills/maestro-tdd"
refuses "clash: .agents/skills/maestro-tdd isn't Maestro's" "$r" apply "$r"
ok apply --replace maestro-tdd "$r"
installed "$r" .agents
# A file git ignores in Maestro's skills isn't the pinned commit's: it
# isn't copied.
echo .DS_Store >>"$m/.git/info/exclude" && touch "$m/skills/maestro-tdd/.DS_Store"
ok apply "$r"
[ ! -e "$r/.claude/skills/maestro-tdd/.DS_Store" ] || fail "apply copied a file git ignores in Maestro"
rm "$m/skills/maestro-tdd/.DS_Store"
# A linked skill folder clashes; --replace removes the link, writes the
# skill in its place and leaves the link's target alone.
mv "$r/.claude/skills/maestro-swarm" "$tmp/shared-swarm" && ln -s "$tmp/shared-swarm" "$r/.claude/skills/maestro-swarm"
refuses "clash: .claude/skills/maestro-swarm is a symlink" "$r" apply "$r"
ok apply --replace maestro-swarm "$r"
[ ! -L "$r/.claude/skills/maestro-swarm" ] || fail "--replace kept the link"
installed "$r" .claude
[ -f "$tmp/shared-swarm/SKILL.md" ] || fail "--replace changed the link's target"
# A linked file the same: --replace writes Maestro's copy where the link was.
mv "$r/.claude/skills/maestro-swarm/SKILL.md" "$tmp/swarm-skill.md" && ln -s "$tmp/swarm-skill.md" "$r/.claude/skills/maestro-swarm/SKILL.md"
refuses "clash: .claude/skills/maestro-swarm/SKILL.md is a symlink" "$r" apply "$r"
ok apply --replace maestro-swarm "$r"
[ -f "$r/.claude/skills/maestro-swarm/SKILL.md" ] && [ ! -L "$r/.claude/skills/maestro-swarm/SKILL.md" ] || fail "--replace removed a linked file and wrote nothing in its place"
installed "$r" .claude
[ -f "$tmp/swarm-skill.md" ] || fail "--replace changed the link's target"
# A record line outside a harness skill folder, or one that climbs out of
# it, stops apply and uninstall before they change anything. A last line
# with no newline still counts.
rec=$r/.git/maestro/installed.list
cp "$rec" "$tmp/rec"
echo outside >"$tmp/victim"
for bad in "README.md" ".claude/skills/maestro-tdd/../../../../victim"; do
	printf '%s\t%s\n' "$(git hash-object "$tmp/victim")" "$bad" >>"$rec"
	for c in apply uninstall; do refuses "installed.list:$(wc -l <"$rec"): want a blob hash" "$r" $c "$r"; done
	cp "$tmp/rec" "$rec"
done
[ -f "$tmp/victim" ] || fail "a bad record line deleted a file"
# A record line for a skill that isn't Maestro's, with the hash of the
# team's file there, stops apply and uninstall too: a committed record is
# anyone's to edit, and a hidden install reads it as the other record.
mkdir "$r/.claude/skills/team-deploy" && echo ours >"$r/.claude/skills/team-deploy/SKILL.md"
for f in "$rec" "$r/.maestro/project/installed.list"; do
	printf '%s\t%s\n' "$(git hash-object "$r/.claude/skills/team-deploy/SKILL.md")" .claude/skills/team-deploy/SKILL.md >>"$f"
	refuses "installed.list:$(wc -l <"$f"): want a blob hash" "$r" apply "$r"
	[ "$f" != "$rec" ] || refuses "installed.list:$(wc -l <"$f"): want a blob hash" "$r" uninstall "$r"
	if [ "$f" = "$rec" ]; then cp "$tmp/rec" "$rec"; else rm "$f"; fi
done
[ -f "$r/.claude/skills/team-deploy/SKILL.md" ] || fail "a record line deleted the team's own skill"
rm -r "$r/.claude/skills/team-deploy"
printf %s "$(cat "$rec")" >"$rec"
ok check "$r"
cp "$tmp/rec" "$rec"

# bot-bundle: one block per bot named, and the team's own memories in a
# message apart from it.
mkdir -p "$r/.maestro/project/bots"
printf '# ours\n\nIntro.\n\n- Runs at once: up to 3.\n  Wrapped.\n' >"$r/.maestro/project/bots/planner.memory.md"
# INSTALL.md runs it as a command, not through bash.
"$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle exited non-zero"; }
[ "$(ls "$r/.maestro/bots" | sort | tr '\n' ' ')" = "a planner.md planner.memories.md reviewer.md " ] || fail "bundles: $(ls "$r/.maestro/bots")"
b=$r/.maestro/bots/planner.md bm=$r/.maestro/bots/planner.memories.md
grep -qxF -- "- Runs at once: up to 3." "$bm" && grep -qxF "  Wrapped." "$bm" || fail "planner memories lack the team's memory"
! grep -qF "Intro." "$bm" || fail "planner memories hold the memory file's intro"
grep -qF 'save each bullet below as one memory that starts `Starting:`' "$bm" || fail "planner memories don't say to save them"
! grep -qF "Runs at once" "$b" || fail "planner block holds a memory: Grok caps the block, so memories go in their own message"
grep -qF "$(sed -n 2p "$m/bot-profiles/team-rules.md")" "$b" || fail "planner block lacks the team rules"
! grep -q '^name: planner' "$b" || fail "planner block holds front matter"
grep -qxF -- "- Maestro: $sha" "$b" || fail "planner block lacks the sha"
# A rebuild says which blocks changed, and drops a bot no longer listed.
says "planner: new" && says "planner memories: new"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner: same" && says "planner memories: same" && says "reviewer: same"
! grep -qF "reviewer memories" "$tmp/out" || fail "bot-bundle wrote memories for a bot with none"
# An update that changes no persona changes only the block's Maestro line:
# the block is the same, so no one pastes it again.
sed -i 's/^- Maestro: .*/- Maestro: 0000000/' "$b"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner: same" && grep -qxF -- "- Maestro: 0000000" "$b" || fail "a block that differs only in its Maestro line was rewritten"
# A changed memory changes only the memories, and every line of them
# counts, the last too. The message replaces the bot's starting memories
# as a set, and each bullet any memory on its topic: it is the owner's.
echo "- Maestro: pin updates to Mondays." >>"$r/.maestro/project/bots/planner.memory.md"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner: same" && says "planner memories: changed"
sed -i 's/Mondays/Fridays/' "$r/.maestro/project/bots/planner.memory.md"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner memories: changed" && grep -qxF -- "- Maestro: pin updates to Fridays." "$bm" || fail "a changed last memory didn't count"
grep -q "^Forget your old starting memories: each starts \`Starting:\`, or you saved it from a \`## Starting memories\` list.*the owner's: each takes the place of any memory on its topic; tell" "$bm" || fail "the memories don't replace the old set: $(cat "$bm")"
echo "- Another." >>"$r/.maestro/project/bots/planner.memory.md"
cp "$r/.maestro/project/install.yaml" "$tmp/choices"
sed -i 's|^bots: .*|bots: planner|' "$r/.maestro/project/install.yaml"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner memories: changed" && says "reviewer: removed .maestro/bots/reviewer.md"
grep -qxF -- "- Another." "$bm" || fail "planner memories lack the new memory"
[ ! -e "$r/.maestro/bots/reviewer.md" ] && [ -e "$r/.maestro/bots/a" ] || fail "bundles after a shorter bots: $(ls "$r/.maestro/bots")"
# A dropped bot loses its memories too. A bot whose memories went gets a
# message to forget the old ones.
sed -i 's|^bots: .*|bots: reviewer|' "$r/.maestro/project/install.yaml"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner: removed .maestro/bots/planner.memories.md" && [ ! -e "$bm" ] || fail "a dropped bot kept its memories"
sed -i 's|^bots: .*|bots: planner|' "$r/.maestro/project/install.yaml"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner memories: new"
mv "$r/.maestro/project/bots/planner.memory.md" "$tmp/mem" && printf '# none\n' >"$r/.maestro/project/bots/planner.memory.md"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner memories: changed" && grep -q '^Forget your old starting memories.*set none in their place' "$bm" && ! grep -q '^- ' "$bm" || fail "no message to forget the memories that went: $(cat "$bm")"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner memories: same"
mv "$tmp/mem" "$r/.maestro/project/bots/planner.memory.md"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner memories: changed" && grep -qxF -- "- Another." "$bm" || fail "memories didn't come back with the team's file"
# A bot set up from an older block, with its memories in it, gets a
# message to forget them even when the team now sets none.
cp "$r/.maestro/project/bots/planner.memory.md" "$tmp/mem" && printf '# none\n' >"$r/.maestro/project/bots/planner.memory.md"
printf 'Persona.\n\n## Starting memories\n\n- Runs at once: up to 9.\n\n## Where you work\n' >"$b" && rm "$bm"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner memories: new" && grep -q 'set none in their place' "$bm" || fail "an old block's memories got no message to forget them"
mv "$tmp/mem" "$r/.maestro/project/bots/planner.memory.md"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle rebuild failed"; }
says "planner memories: changed" && grep -qxF -- "- Another." "$bm" || fail "the team's memories didn't come back after the old block's"
# Every shipped profile fits in Grok's 20,000 characters, with room for a
# long repo path: bot-bundle refuses one that doesn't.
all=$(cd "$m/bot-profiles" && ls -d */ | tr -d / | tr '\n' ' ')
sed -i "s|^bots: .*|bots: $all|" "$r/.maestro/project/install.yaml"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "a shipped profile's block is over Grok's limit"; }
for p in $all; do
	n=$(sed '/^## Where you work$/,$d' "$r/.maestro/bots/$p.md" | wc -c)
	[ "$n" -le 19700 ] || fail "$p: persona and team rules are $n bytes; keep them under 19,700 so the block fits Grok's 20,000 with a repo path up to about 190 characters"
done
cp "$tmp/choices" "$r/.maestro/project/install.yaml"
"$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle exited non-zero"; }
# A CRLF checkout of install.yaml names the same bots.
cp "$r/.maestro/project/install.yaml" "$tmp/choices"
sed -i 's/$/\r/' "$r/.maestro/project/install.yaml"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle failed on a CRLF install.yaml"; }
cp "$tmp/choices" "$r/.maestro/project/install.yaml"
# A bot name holding a path refuses.
sed -i 's|^bots: .*|bots: ../planner|' "$r/.maestro/project/install.yaml"
if bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" 2>&1; then fail "bot-bundle took a bot name with a path"; fi
says "no bot profile ../planner"
cp "$tmp/choices" "$r/.maestro/project/install.yaml"
# A block or a memory file that is a symlink, which a PR can commit,
# refuses: bot-bundle would write through it, or paste its target into
# the block.
echo outside >"$tmp/victim"
mv "$b" "$tmp/planner.md" && ln -s "$tmp/victim" "$b"
if bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" 2>&1; then fail "bot-bundle wrote through a symlinked block"; fi
says ".maestro/bots/planner.md is a symlink"
[ "$(cat "$tmp/victim")" = outside ] || fail "bot-bundle wrote through a symlinked block"
rm "$b" && mv "$tmp/planner.md" "$b"
mem=$r/.maestro/project/bots/planner.memory.md
mv "$mem" "$tmp/memory" && printf -- '- token: s3cr3t\n' >"$tmp/victim" && ln -s "$tmp/victim" "$mem"
if bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" 2>&1; then fail "bot-bundle read a symlinked memory file"; fi
says ".maestro/project/bots/planner.memory.md is a symlink"
! grep -qF s3cr3t "$b" "$bm" || fail "bot-bundle pasted a symlink's target into a block"
rm "$mem" && mv "$tmp/memory" "$mem"
echo outside >"$tmp/victim"
mv "$bm" "$tmp/planner.memories.md" && ln -s "$tmp/victim" "$bm"
if bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" 2>&1; then fail "bot-bundle wrote through a symlinked memories file"; fi
says ".maestro/bots/planner.memories.md is a symlink"
[ "$(cat "$tmp/victim")" = outside ] || fail "bot-bundle wrote through a symlinked memories file"
rm "$bm" && mv "$tmp/planner.memories.md" "$bm"
# A block over Grok's limit refuses before any bot's files are written,
# even a bot listed first that is due a new one.
sed -i 's|^bots: .*|bots: reviewer planner|' "$r/.maestro/project/install.yaml"
rm -f "$r/.maestro/bots/reviewer.md"
echo "- One more." >>"$r/.maestro/project/bots/planner.memory.md"
snap "$r/.maestro/bots" >"$tmp/before"
head -c 20000 /dev/zero | tr '\0' x >>"$m/bot-profiles/planner/persona.md"
git -C "$m" commit -q -am big && sed -i "s/^maestro-sha: .*/maestro-sha: $(git -C "$m" rev-parse --short HEAD)/" "$r/.maestro/project/install.yaml"
if bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" 2>&1; then fail "bot-bundle wrote a block over Grok's limit"; fi
says "over Grok's 20,000-character limit"
snap "$r/.maestro/bots" | diff "$tmp/before" - >/dev/null || fail "bot-bundle wrote files before it refused a block over Grok's limit"
git -C "$m" reset -q --hard HEAD~1 && cp "$tmp/choices" "$r/.maestro/project/install.yaml"
sed -i '/^- One more\.$/d' "$r/.maestro/project/bots/planner.memory.md"
# The blocks come from the pinned commit, as install.sh's copies do.
echo "- An edit no one reviewed." >>"$m/bot-profiles/team-rules.md"
if bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" 2>&1; then fail "bot-bundle took uncommitted bot-profiles"; fi
says "uncommitted changes in bot-profiles/"
! grep -qF "no one reviewed" "$b" || fail "bot-bundle put an uncommitted edit in the block"
git -C "$m" checkout -q -- bot-profiles/team-rules.md
sed -i "s/^maestro-sha: .*/maestro-sha: 0000000/" "$r/.maestro/project/install.yaml"
if bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" 2>&1; then fail "bot-bundle ran off its pin"; fi
says "but install.yaml pins 0000000"
cp "$tmp/choices" "$r/.maestro/project/install.yaml"

# --- update: Maestro changes a skill, adds a file and drops a skill ---
# The team changed one copy of the dropped skill: that one stays as
# theirs, out of the record; the other goes, but a file beside it stays.
before_exclude=$(cat "$r/.git/info/exclude")
echo mine >>"$r/.claude/skills/maestro-prune/SKILL.md"
touch "$r/.agents/skills/maestro-prune/.DS_Store"
echo "new line" >>"$m/skills/maestro-tdd/SKILL.md"
echo new >"$m/skills/maestro-recall/extra.md"
git -C "$m" rm -rq skills/maestro-prune
git -C "$m" add -A && git -C "$m" commit -q -m two
refuses "but install.yaml pins $sha" "$r" apply "$r"
sha2=$(git -C "$m" rev-parse --short HEAD)
sed -i "s/^maestro-sha: .*/maestro-sha: $sha2/" "$r/.maestro/project/install.yaml"
if run check "$r"; then fail "check passed with copies from the old pin"; fi
says "the record is from Maestro $sha, not the pinned $sha2: run apply"
ok apply "$r"
says "kept as the team's, out of the record and no longer hidden from git: .claude/skills/maestro-prune"
! grep -q "kept as the team's.*\.agents/skills/maestro-prune" "$tmp/out" || fail "a .DS_Store kept Maestro's copies as the team's"
[ "$(tail -1 "$r/.claude/skills/maestro-prune/SKILL.md")" = mine ] || fail "update changed the team's copy of a dropped skill"
[ ! -e "$r/.agents/skills/maestro-prune/SKILL.md" ] || fail "update kept a dropped skill's copy the team didn't change"
[ -f "$r/.agents/skills/maestro-prune/.DS_Store" ] || fail "update removed a file the record doesn't list"
! grep -q maestro-prune "$r/.git/maestro/installed.list" || fail "the team's copy is still in the record"
rm -r "$r/.claude/skills/maestro-prune" "$r/.agents/skills/maestro-prune"
skills=$(cd "$m/skills" && ls -d */ | tr -d / | sort)
installed "$r" .claude .agents
[ ! -e "$r/.claude/skills/maestro-prune" ] && [ ! -e "$r/.agents/skills/maestro-prune" ] || fail "update kept a skill Maestro dropped"
! grep -q maestro-prune "$r/.git/info/exclude" || fail "update kept the dropped skill's exclude line"
[ "$(echo "$before_exclude" | grep -v maestro-prune)" = "$(cat "$r/.git/info/exclude")" ] || fail "update changed other exclude lines"
sha1=$sha sha=$sha2

# --- the install.sh before the record: its copies, from the first commit,
# and its exclude lines. A copy the team changed clashes; then apply takes
# the rest over, drops the old lines and removes the skill Maestro dropped.
r10=$tmp/legacy
repo "$r10" "harnesses: claude codex" "skills-home: committed"
for d in .claude .agents; do
	mkdir "$r10/$d" && git -C "$m" archive "$sha1" skills | tar -x -C "$r10/$d"
	find "$r10/$d/skills" -maxdepth 1 -type f -delete
	for n in $(cd "$r10/$d/skills" && ls); do echo "$d/skills/$n/"; done >>"$r10/.git/info/exclude"
done
echo mine >>"$r10/.agents/skills/maestro-swarm/SKILL.md"
refuses "clash: .agents/skills/maestro-swarm/SKILL.md isn't Maestro's" "$r10" apply "$r10"
git -C "$m" show "$sha1:skills/maestro-swarm/SKILL.md" >"$r10/.agents/skills/maestro-swarm/SKILL.md"
ok apply "$r10"
installed "$r10" .claude .agents
[ ! -e "$r10/.claude/skills/maestro-prune" ] && [ ! -e "$r10/.agents/skills/maestro-prune" ] || fail "the old install's dropped skill stayed"
! grep -q '^\.[a-z]*/skills/' "$r10/.git/info/exclude" || fail "the old exclude lines stayed"
git -C "$r10" status --porcelain --untracked-files=all | grep -q '^?? .claude/skills/maestro-tdd/SKILL.md$' || fail "committed skills are still hidden"
ok check "$r10"

# A team's skill folder by a Maestro name clashes, and the team moves it.
r2=$tmp/own
repo "$r2" "harnesses: claude codex" "skills-home: hidden"
mkdir -p "$r2/.agents/skills/maestro-swarm" "$r2/.agents/skills/review"
echo mine >"$r2/.agents/skills/maestro-swarm/SKILL.md"
echo mine >"$r2/.agents/skills/review/SKILL.md"
refuses "clash: .agents/skills/maestro-swarm/SKILL.md isn't Maestro's" "$r2" apply "$r2"
says "move the team's changes to a skill named without maestro-"
[ "$(cat "$r2/.agents/skills/maestro-swarm/SKILL.md")" = mine ] || fail "a refused apply changed the team's skill"
mv "$r2/.agents/skills/maestro-swarm" "$r2/.agents/skills/swarm"
ok apply "$r2"
[ "$(cat "$r2/.agents/skills/review/SKILL.md")" = mine ] || fail "apply changed the team's own review skill"

# --- switch hidden to committed: the copies show up in git ---
sed -i 's/^skills-home: .*/skills-home: committed/' "$r/.maestro/project/install.yaml"
ok apply "$r"
says "0 files written, 0 removed"
installed "$r" .claude .agents
[ -f "$r/.maestro/project/installed.list" ] && [ ! -e "$r/.git/maestro/installed.list" ] || fail "committed record should be in .maestro/project only"
git -C "$r" status --porcelain --untracked-files=all | grep -q '^?? .claude/skills/maestro-tdd/SKILL.md$' || fail "committed skills are hidden from git"

# A CRLF checkout of the committed copies still matches the record, from
# inside another repo with other attributes.
git -C "$r" add -A && git -C "$r" commit -qm install
printf '*.md text eol=crlf\ninstalled.list text eol=crlf\n' >"$r/.gitattributes"
git -C "$r" add .gitattributes && git -C "$r" commit -qm crlf
rm "$r/.claude/skills/maestro-tdd/SKILL.md" "$r/.maestro/project/installed.list"
git -C "$r" checkout -- .claude/skills/maestro-tdd/SKILL.md .maestro/project/installed.list
grep -q $'\r$' "$r/.claude/skills/maestro-tdd/SKILL.md" || fail "the checkout should be CRLF"
grep -q $'\r$' "$r/.maestro/project/installed.list" || fail "the record's checkout should be CRLF"
(cd "$r2" && run check "$r") || { cat "$tmp/out"; fail "check of a CRLF checkout from another repo failed"; }
(cd "$r2" && run apply "$r") || { cat "$tmp/out"; fail "apply over a CRLF checkout clashed"; }
# An apply that changes nothing leaves the CRLF record as git wrote it: an
# LF copy reads as modified, and the next pull that touches it aborts.
[ -z "$(git -C "$r" status --porcelain)" ] || { git -C "$r" status --porcelain; fail "apply over a CRLF checkout left the repo dirty"; }

# --- switch committed to hidden: the tracked copies are untracked ---
sed -i 's/^skills-home: .*/skills-home: hidden/' "$r/.maestro/project/install.yaml"
n=$(($(git -C "$r" ls-files .claude .agents | wc -l) + 1))
ok apply "$r"
says "untracked $n files git tracked: commit that"
says "every other clone loses the skills on its next pull until it runs the install"
git -C "$r" commit -qam hide
seen=$(git -C "$r" status --porcelain --untracked-files=all)
[ -z "$seen" ] || fail "after the switch to hidden, git still sees: $seen"
[ -z "$(git -C "$r" ls-files .claude .agents .maestro/project/installed.list)" ] || fail "the switch to hidden left files tracked"
ok check "$r"

# --- uninstall: removes unchanged files, keeps a changed one, and never
# deletes through a linked folder ---
echo mine >>"$r/.agents/skills/maestro-tdd/SKILL.md"
cp -R "$r/.claude/skills/maestro-swarm" "$tmp/shared-swarm2"
rm -r "$r/.claude/skills/maestro-swarm" && ln -s "$tmp/shared-swarm2" "$r/.claude/skills/maestro-swarm"
if run uninstall "$r"; then fail "uninstall with a changed file exited 0"; fi
says "kept, changed since install: .agents/skills/maestro-tdd/SKILL.md"
says "kept, behind a symlink: .claude/skills/maestro-swarm/SKILL.md"
[ -f "$tmp/shared-swarm2/SKILL.md" ] || fail "uninstall deleted a file through a link"
rm "$r/.claude/skills/maestro-swarm" && rmdir "$r/.claude/skills" "$r/.claude"
left=$(cd "$r" && { find .claude .agents -type f 2>/dev/null || true; } | sort | tr '\n' ' ')
[ "$left" = ".agents/skills/maestro-tdd/SKILL.md " ] || fail "uninstall left: $left"
[ ! -e "$r/.claude" ] || fail "uninstall left an empty .claude/"
! grep -q '/skills/' "$r/.git/info/exclude" || fail "uninstall left skill exclude lines"
grep -qxF /.maestro/src/ "$r/.git/info/exclude" && grep -qxF /.maestro/bots/ "$r/.git/info/exclude" ||
	fail "uninstall should keep .maestro/src/ and .maestro/bots/ hidden: they stay"
grep -qxF '*.log' "$r/.git/info/exclude" || fail "uninstall changed the user's exclude line"
[ -f "$r/.maestro/project/install.yaml" ] && [ -f "$r/.maestro/project/project-setup.md" ] || fail "uninstall removed the team's choices or setup"

# uninstall runs with Maestro off the pin, as after an update stopped
# halfway; apply and check don't.
r14=$tmp/offpin
repo "$r14" "harnesses: claude" "skills-home: hidden"
ok apply "$r14"
sed -i 's/^maestro-sha: .*/maestro-sha: 0000000/' "$r14/.maestro/project/install.yaml"
if run check "$r14"; then fail "check passed off the pin"; fi
says "but install.yaml pins 0000000"
# A bot whose profile that commit no longer has doesn't stop it either.
echo "bots: gone-bot" >>"$r14/.maestro/project/install.yaml"
ok uninstall "$r14"
[ ! -e "$r14/.claude" ] || fail "uninstall off the pin left .claude/"

# --- A CRLF checkout of Maestro, as core.autocrlf makes one, matches its copies ---
r3=$tmp/crlf
repo "$r3" "harnesses: claude" "skills-home: hidden" "tickets: maestro-ticket"
f=$m/skills/maestro-tdd/SKILL.md
git -C "$m" config core.autocrlf true
rm "$f" && git -C "$m" checkout -q -- skills/maestro-tdd/SKILL.md
grep -q $'\r$' "$f" || fail "the autocrlf checkout wrote no CRLF"
git -C "$r3" config core.autocrlf true
ok apply "$r3"
grep -q $'\r$' "$r3/.claude/skills/maestro-tdd/SKILL.md" || fail "the copy lost the CRLF source's bytes"
ok check "$r3"
before=$(snap "$r3")
ok apply "$r3"
[ "$before" = "$(snap "$r3")" ] || fail "a second apply over a CRLF source changed the repo"
git -C "$m" config --unset core.autocrlf
rm "$f" && git -C "$m" checkout -q -- skills/maestro-tdd/SKILL.md

# --- refusals ---
mkdir "$tmp/plain"
refuses "not in a git work tree" "$tmp/plain" apply "$tmp/plain"
# A repo another user owns, as when the bots get a user of their own: git's
# reason shows, with its fix.
# A warning git prints on stderr is no part of the repo's path.
git -C "$r2" config core.fsyncObjectFiles true
run check "$r2" || true
! grep -qF "is inside the repo at" "$tmp/out" || fail "a git warning on stderr broke the path check"
git -C "$r2" config --unset core.fsyncObjectFiles
if GIT_TEST_ASSUME_DIFFERENT_OWNER=1 run check "$r2"; then fail "check passed in a repo git doesn't trust"; fi
says "dubious ownership"
if GIT_TEST_ASSUME_DIFFERENT_OWNER=1 bash "$m/scripts/bot-bundle.sh" "$r2" >"$tmp/out" 2>&1; then fail "bot-bundle passed in a repo git doesn't trust"; fi
says "dubious ownership" && says "git can't read the repo"
export GIT_DIR="$r2/.git"
refuses "not in a git work tree" "$tmp/plain" apply "$tmp/plain"
unset GIT_DIR
mkdir "$r2/pkg"
refuses "install at the top of the repo" "$r2" apply "$r2/pkg"
r4=$tmp/bad
repo "$r4" "harnesses: claude" "skills-home: hidden"
cp "$r4/.maestro/project/install.yaml" "$tmp/keep"
while IFS='|' read -r bad msg; do
	cp "$tmp/keep" "$r4/.maestro/project/install.yaml"
	echo "$bad" >>"$r4/.maestro/project/install.yaml"
	refuses "$msg" "$r4" apply "$r4"
done <<'EOF'
colour: blue|install.yaml:5: unknown key colour
nonsense|install.yaml:5: want key: value
skills-home: hidden|install.yaml:5: skills-home twice
code-graph: mcp|install.yaml:5: code-graph twice
skip: maestro-recall|install.yaml:5: skip: is gone: every skill installs
bots: designer|no bot profile designer
bots: ../bot-profiles/planner|no bot profile ../bot-profiles/planner
tickets: our-tickets|install.yaml:5: tickets: Maestro's bots run only on maestro-ticket
EOF
for bad in "harnesses: vim|unknown harness vim" "harnesses: codex grok|harnesses must include claude" "skills-home: user|skills-home wants committed or hidden" "code-graph: yes|code-graph wants mcp or none"; do
	sed "s/^${bad%%:*}: .*/${bad%%|*}/" "$tmp/keep" >"$r4/.maestro/project/install.yaml"
	refuses "${bad#*|}" "$r4" apply "$r4"
done
grep -v '^code-graph:' "$tmp/keep" >"$r4/.maestro/project/install.yaml"
for c in apply check; do refuses "code-graph wants mcp or none; INSTALL.md step 4" "$r4" $c "$r4"; done
cp "$tmp/keep" "$r4/.maestro/project/install.yaml"
refuses "no skill maestro-nope in" "$r4" apply --replace maestro-nope "$r4"
sed -i 's/^maestro-sha: .*/maestro-sha: 0000000/' "$r4/.maestro/project/install.yaml"
refuses "but install.yaml pins 0000000" "$r4" apply "$r4"
cp "$tmp/keep" "$r4/.maestro/project/install.yaml"
echo dirty >>"$m/skills/maestro-tdd/SKILL.md"
refuses "uncommitted changes in skills/" "$r4" apply "$r4"
git -C "$m" checkout -q -- skills
rm "$r4/.maestro/project/install.yaml"
refuses "no $r4/.maestro/project/install.yaml" "$r4" apply "$r4"
r5=$tmp/link
repo "$r5" "harnesses: claude" "skills-home: hidden"
mkdir "$tmp/elsewhere" && ln -s "$tmp/elsewhere" "$r5/.claude"
refuses "is a symlink" "$r5" apply "$r5"
# A link in a harness folder install neither writes to nor names in its
# record is the team's: a claude-only install goes ahead.
r5b=$tmp/link-other
repo "$r5b" "harnesses: claude" "skills-home: hidden"
mkdir -p "$r5b/.claude/skills" "$r5b/.agents" && ln -s ../.claude/skills "$r5b/.agents/skills"
ok apply "$r5b"
installed "$r5b" .claude
# Dropping a harness whose folder is now a link to the other's: a delete
# through it would take the kept harness's copies, so apply refuses.
r5c=$tmp/link-dropped
repo "$r5c" "harnesses: claude codex" "skills-home: hidden"
ok apply "$r5c"
rm -r "$r5c/.agents/skills" && ln -s ../.claude/skills "$r5c/.agents/skills"
sed -i 's/^harnesses: .*/harnesses: claude/' "$r5c/.maestro/project/install.yaml"
refuses "$r5c/.agents/skills is a symlink" "$r5c" apply "$r5c"
# An install without claude leaves every bot without its skills: check
# fails, and uninstall still works.
r5d=$tmp/no-claude
repo "$r5d" "harnesses: claude codex" "skills-home: hidden"
ok apply "$r5d"
sed -i 's/^harnesses: .*/harnesses: codex/' "$r5d/.maestro/project/install.yaml"
refuses "harnesses must include claude" "$r5d" check "$r5d"
# On a git too old for apply, uninstall still works.
oldgit 2.37.1 ok uninstall "$r5d"
r6=$tmp/nostore
repo "$r6" "harnesses: claude" "skills-home: hidden"
STUB_FAIL=1 refuses "doctor failed" "$r6" apply "$r6"
says "FAIL  stub: no store here"
refuses "usage:" "$r6" frob "$r6"

# A failed copy leaves a partial install that a second apply finishes.
r7=$tmp/half
repo "$r7" "harnesses: claude codex" "skills-home: hidden"
mkdir "$tmp/cpbin"
printf '%s\n' '#!/usr/bin/env bash' 'case "$*" in */skills/maestro-tdd/*) exit 1 ;; esac' 'exec /bin/cp "$@"' >"$tmp/cpbin/cp"
chmod +x "$tmp/cpbin/cp"
if PATH="$tmp/cpbin:$PATH" run apply "$r7"; then fail "apply with a failing cp exited 0"; fi
seen=$(git -C "$r7" status --porcelain --untracked-files=all)
[ "$seen" = "?? .maestro/project/install.yaml" ] || fail "a failed apply should leave its copies hidden: $seen"
ok apply "$r7"
installed "$r7" .claude .agents
left=$(find "$r7" -name 'maestro-install.*'); [ -z "$left" ] || fail "a failed copy left its temp folder: $left"
# check reads project-setup.md in any heading case, with a
# bullet or backticks round the branch, CRLF.
sed -i 's/^## Main branch$/## Main Branch/; s/^main$/- `main`/; s/^\(- [a-z]*:\) `/\1\t`/; s/$/\r/' "$r7/.maestro/project/project-setup.md"
ok check "$r7"
! grep -q "lists no checks" "$tmp/out" || fail "check missed a check with a tab in it"
# A bullet under ## Checks that isn't a check is a problem.
cp "$r7/.maestro/project/project-setup.md" "$tmp/setup"
sed -i 's/^## Checks\r$/&\n- lint: run golangci-lint/' "$r7/.maestro/project/project-setup.md"
if run check "$r7"; then fail "check passed a bad ## Checks bullet"; fi
says "under ## Checks, want - <name>: \`<command>\`, not: - lint: run golangci-lint"
cp "$tmp/setup" "$r7/.maestro/project/project-setup.md"
printf '## Run\n\n<how to start it>\n' >>"$r7/.maestro/project/project-setup.md"
ok check "$r7"
says "warn: project-setup.md: ## Run still holds"
printf '## Project\n\n- Name: <name>\n- Summary: a shop\n' >>"$r7/.maestro/project/project-setup.md"
ok check "$r7"
says "warn: project-setup.md: ## Project still holds"
# An older install's ## Tickets section, CRLF or not: one warn to delete it.
printf ' ## Tickets\r\n\n<or your own tool>\n' >>"$r7/.maestro/project/project-setup.md"
ok check "$r7"
says "warn: project-setup.md: ## Tickets is gone"
! grep -q "## Tickets still holds" "$tmp/out" || fail "check warned twice about ## Tickets"
# A linked worktree refuses, hidden or committed: it shares the main
# checkout's exclude file and record.
git -C "$r7" add .maestro/project && git -C "$r7" commit -qm setup
git -C "$r7" worktree add -q "$tmp/wt7"
for c in apply check uninstall; do refuses "is a linked worktree; run install.sh in the main checkout, $r7" "$r7" $c "$tmp/wt7"; done
sed -i 's/^skills-home: .*/skills-home: committed/' "$tmp/wt7/.maestro/project/install.yaml"
refuses "is a linked worktree" "$r7" apply "$tmp/wt7"
# An install started in the worktree leaves a .maestro/src/ there. The
# main checkout then says to delete it, not to run install.sh there, which
# refuses.
mkdir -p "$tmp/wt7/.maestro/src"
for c in apply check uninstall; do
	refuses "$tmp/wt7, a linked worktree, holds a .maestro/src/ that an install started there left behind; delete that folder" "$r7" $c "$r7"
done
git -C "$r7" worktree remove --force "$tmp/wt7"
# With a bare main repo, a linked worktree takes the install, and a second
# worktree then refuses.
r11=$tmp/bare
git clone -q --bare "$r2" "$r11/.git"
git -C "$r11/.git" worktree add -q "$r11/main" main
git -C "$r11/.git" worktree add -q -b feat "$r11/feat"
mkdir -p "$r11/main/.maestro/project" "$r11/main/.maestro/src"
printf '%s\n' "maestro-sha: $sha" "harnesses: claude" "skills-home: hidden" "code-graph: none" >"$r11/main/.maestro/project/install.yaml"
ok apply "$r11/main"
installed "$r11/main" .claude
refuses "Maestro is installed in $r11/main" "$r11/feat" apply "$r11/feat"
says "or delete its .maestro/src/"
# safe.bareRepository=explicit keeps git from running inside a bare repo's
# folder, unless it is named .git: the install in its worktree still works.
r15=$tmp/bare2
git clone -q --bare "$r2" "$r15/.bare"
git -C "$r15/.bare" worktree add -q "$r15/main" main
mkdir -p "$r15/main/.maestro/project"
printf '%s\n' "maestro-sha: $sha" "harnesses: claude" "skills-home: hidden" "code-graph: none" >"$r15/main/.maestro/project/install.yaml"
git config --global safe.bareRepository explicit
ok apply "$r15/main"
installed "$r15/main" .claude
git config --global --unset safe.bareRepository
# A file git can't hash stops check and uninstall; uninstall keeps the record.
if [ "$(id -u)" -ne 0 ]; then
	chmod 000 "$r7/.claude/skills/maestro-tdd/SKILL.md"
	if run check "$r7"; then fail "check passed with an unreadable file"; fi
	says "git hash-object failed"
	if run uninstall "$r7"; then fail "uninstall passed with an unreadable file"; fi
	says "nothing was changed"
	[ -f "$r7/.git/maestro/installed.list" ] && [ -f "$r7/.claude/skills/maestro-swarm/SKILL.md" ] || fail "a failed uninstall removed files or the record"
	chmod 644 "$r7/.claude/skills/maestro-tdd/SKILL.md"
fi

# Committed skills git would ignore don't pass check.
r9=$tmp/ignored
repo "$r9" "harnesses: claude" "skills-home: committed"
echo /.claude/ >"$r9/.gitignore"
ok apply "$r9"
if run check "$r9"; then fail "check passed with committed skills git ignores"; fi
says ".claude/skills/maestro-tdd is hidden from git, but skills-home is committed"

# check, through a symlinked path, still checks Maestro's clone in
# .maestro/src/ for files the harnesses would load.
r8=$tmp/cloned
repo "$r8" "harnesses: claude" "skills-home: hidden"
git clone -q "$m" "$r8/.maestro/src"
bash "$r8/.maestro/src/scripts/install.sh" apply "$r8" >"$tmp/out" 2>&1 || { cat "$tmp/out"; fail "apply from .maestro/src failed"; }
touch "$r8/.maestro/src/AGENTS.md"
ln -s "$tmp" "$tmp/via"
if bash "$tmp/via/cloned/.maestro/src/scripts/install.sh" check "$tmp/via/cloned" >"$tmp/out" 2>&1; then fail "check passed with AGENTS.md in .maestro/src"; fi
says "problem: .maestro/src/AGENTS.md exists"
# A clone that skipped apply leaves Maestro's clone in sight of git add.
sed -i '\#^/\.maestro/src/$#d' "$r8/.git/info/exclude"
if bash "$r8/.maestro/src/scripts/install.sh" check "$r8" >"$tmp/out" 2>&1; then fail "check passed with .maestro/src/ seen by git"; fi
says "problem: .maestro/src/ isn't hidden from git: run apply"
echo /.maestro/src/ >>"$r8/.git/info/exclude" && rm "$r8/.maestro/src/AGENTS.md"
bash "$r8/.maestro/src/scripts/install.sh" check "$r8" >"$tmp/out" 2>&1 || true
! grep -qF "isn't hidden" "$tmp/out" || { cat "$tmp/out"; fail "check flagged a hidden .maestro/src/"; }

# --- check warns until origin/<main> has project-setup.md as the repo has
# it: each ticket's worktree branches from there.
r12=$tmp/pushed
repo "$r12" "harnesses: claude" "skills-home: hidden"
git init -q --bare "$tmp/pushed.git" && git -C "$r12" remote add origin "$tmp/pushed.git" && git -C "$r12" push -q origin main
ok apply "$r12"
ok check "$r12"
says "warn: project-setup.md isn't on origin/main"
git -C "$r12" add .maestro/project && git -C "$r12" commit -qm setup && git -C "$r12" push -q origin main
ok check "$r12"
! grep -q "origin/main" "$tmp/out" || fail "check warned with project-setup.md pushed"
echo extra >>"$r12/.maestro/project/project-setup.md"
ok check "$r12"
says "warn: project-setup.md differs from origin/main's copy"

# --- check warns until origin/<main> has the committed skills: until
# then only this checkout has them.
r13=$tmp/pushed-committed
repo "$r13" "harnesses: claude codex" "skills-home: committed"
git init -q --bare "$tmp/pushed-committed.git" && git -C "$r13" remote add origin "$tmp/pushed-committed.git"
ok apply "$r13"
git -C "$r13" add .maestro/project && git -C "$r13" commit -qm setup && git -C "$r13" push -q origin main
ok check "$r13"
nfiles=$(($(find "$m/skills" -mindepth 2 -type f | wc -l) * 2))
says "warn: $nfiles of $nfiles skill files are missing from origin/main as of the last fetch, or differ there: commit .claude/skills/ .agents/skills/"
git -C "$r13" add .claude .agents && git -C "$r13" commit -qm skills && git -C "$r13" push -q origin main
ok check "$r13"
! grep -q "origin/main" "$tmp/out" || { cat "$tmp/out"; fail "check warned with the skills pushed"; }
# A harness dropped from the choices, before the next apply: its files
# are on main and still in the record, and they don't count.
sed -i 's/^harnesses: .*/harnesses: claude/' "$r13/.maestro/project/install.yaml"
git -C "$r13" commit -qam "drop codex" && git -C "$r13" push -q origin main
ok check "$r13"
! grep -q "skill files" "$tmp/out" || { cat "$tmp/out"; fail "check counted a dropped harness's files"; }
# main moves on: a file there differs from this checkout's.
git clone -q "$tmp/pushed-committed.git" "$tmp/ahead" && echo newer >>"$tmp/ahead/.claude/skills/maestro-tdd/SKILL.md"
git -C "$tmp/ahead" commit -qam newer && git -C "$tmp/ahead" push -q origin main
git -C "$r13" fetch -q origin
ok check "$r13"
says "warn: 1 of $((nfiles / 2)) skill files are missing from origin/main as of the last fetch, or differ there"
says "if main is ahead of this checkout, pull and run apply instead"

# --- an install by the install.sh before the record, from a commit where
# a merge made a skill's copy: that copy is Maestro's too.
git -C "$m" checkout -q -b side
echo side >>"$m/skills/maestro-tdd/SKILL.md" && git -C "$m" commit -qam side
git -C "$m" checkout -q main
echo main >>"$m/skills/maestro-tdd/SKILL.md" && git -C "$m" commit -qam main
if git -C "$m" merge -q side >/dev/null 2>&1; then fail "the test merge had no conflict"; fi
git -C "$m" show main:skills/maestro-tdd/SKILL.md >"$m/skills/maestro-tdd/SKILL.md"
echo both >>"$m/skills/maestro-tdd/SKILL.md"
git -C "$m" commit -qam merge
merged=$(git -C "$m" rev-parse HEAD)
echo after >>"$m/skills/maestro-tdd/SKILL.md" && git -C "$m" commit -qam after
sha=$(git -C "$m" rev-parse --short HEAD)
r11=$tmp/legacy-merge
repo "$r11" "harnesses: claude" "skills-home: committed"
mkdir "$r11/.claude" && git -C "$m" archive "$merged" skills | tar -x -C "$r11/.claude"
find "$r11/.claude/skills" -maxdepth 1 -type f -delete
for n in $(cd "$r11/.claude/skills" && ls); do echo ".claude/skills/$n/"; done >>"$r11/.git/info/exclude"
ok apply "$r11"
installed "$r11" .claude

echo "ok: install.sh applies hidden and committed installs, updates and switches cleanly, refuses clashes and bad choices, survives a failed copy and a CRLF checkout, and uninstalls only what it placed; bot-bundle builds each bot's block"
