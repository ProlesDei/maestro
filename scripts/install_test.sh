#!/usr/bin/env bash
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
# reads project-setup.md; a linked worktree refuses;
# uninstall removes only unchanged files and keeps .maestro/src/ hidden; a
# failed copy stays hidden and a rerun finishes it; a file git can't hash
# stops check and uninstall; bad choices and bad repos refuse and change
# nothing.
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

# A stand-in maestro-ticket: logs where doctor ran, fails when STUB_FAIL is set.
mkdir "$tmp/bin"
printf '%s\n' '#!/usr/bin/env bash' 'echo "$PWD $*" >>"$STUB_LOG"' '[ -z "${STUB_FAIL:-}" ]' >"$tmp/bin/maestro-ticket"
chmod +x "$tmp/bin/maestro-ticket"
export PATH="$tmp/bin:$PATH" STUB_LOG="$tmp/doctor.log"
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
# repo <path> <choices...>: a new repo with one commit and these choices.
repo() {
	local r=$1
	shift
	git init -q "$r"
	git -C "$r" commit -q --allow-empty -m init
	mkdir -p "$r/.maestro/project"
	printf '%s\n' "maestro-sha: $sha" "$@" >"$r/.maestro/project/install.yaml"
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

ok check "$r"
says "ok: hidden install of $nskills skills at $sha"
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
printf %s "$(cat "$rec")" >"$rec"
ok check "$r"
cp "$tmp/rec" "$rec"

# bot-bundle: one block per bot named, with the team's own memories.
mkdir -p "$r/.maestro/project/bots"
printf '# ours\n\nIntro.\n\n- Runs at once: up to 3.\n  Wrapped.\n' >"$r/.maestro/project/bots/planner.memory.md"
# INSTALL.md runs it as a command, not through bash.
"$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle exited non-zero"; }
[ "$(ls "$r/.maestro/bots" | sort | tr '\n' ' ')" = "a planner.md reviewer.md " ] || fail "bundles: $(ls "$r/.maestro/bots")"
b=$r/.maestro/bots/planner.md
grep -qxF -- "- Runs at once: up to 3." "$b" && grep -qxF "  Wrapped." "$b" || fail "planner block lacks the team's memory"
! grep -qF "Intro." "$b" || fail "planner block holds the memory file's intro"
grep -qF "$(sed -n 2p "$m/bot-profiles/team-rules.md")" "$b" || fail "planner block lacks the team rules"
! grep -q '^name: planner' "$b" || fail "planner block holds front matter"
grep -qxF -- "- Maestro: $sha" "$b" || fail "planner block lacks the sha"
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
repo "$r2" "harnesses: codex" "skills-home: hidden"
mkdir -p "$r2/.agents/skills/maestro-swarm" "$r2/.agents/skills/review"
echo mine >"$r2/.agents/skills/maestro-swarm/SKILL.md"
echo mine >"$r2/.agents/skills/review/SKILL.md"
refuses "clash: .agents/skills/maestro-swarm/SKILL.md isn't Maestro's" "$r2" apply "$r2"
says "move the team's changes to a skill named without maestro-"
[ "$(cat "$r2/.agents/skills/maestro-swarm/SKILL.md")" = mine ] || fail "a refused apply changed the team's skill"
mv "$r2/.agents/skills/maestro-swarm" "$r2/.agents/skills/swarm"
ok apply "$r2"
[ "$(cat "$r2/.agents/skills/review/SKILL.md")" = mine ] || fail "apply changed the team's own review skill"
[ ! -e "$r2/.claude" ] || fail "codex alone wrote .claude/"

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

# --- switch committed to hidden: the tracked copies are untracked ---
sed -i 's/^skills-home: .*/skills-home: hidden/' "$r/.maestro/project/install.yaml"
n=$(($(git -C "$r" ls-files .claude .agents | wc -l) + 1))
ok apply "$r"
says "untracked $n files git tracked: commit that"
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
colour: blue|install.yaml:4: unknown key colour
nonsense|install.yaml:4: want key: value
skills-home: hidden|install.yaml:4: skills-home twice
skip: maestro-recall|install.yaml:4: skip: is gone: every skill installs
bots: designer|no bot profile designer
bots: ../bot-profiles/planner|no bot profile ../bot-profiles/planner
tickets: our-tickets|install.yaml:4: tickets: Maestro's bots run only on maestro-ticket
EOF
for bad in "harnesses: vim|unknown harness vim" "skills-home: user|skills-home wants committed or hidden"; do
	sed "s/^${bad%%:*}: .*/${bad%%|*}/" "$tmp/keep" >"$r4/.maestro/project/install.yaml"
	refuses "${bad#*|}" "$r4" apply "$r4"
done
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
# record is the team's: a codex-only install goes ahead.
r5b=$tmp/link-other
repo "$r5b" "harnesses: codex" "skills-home: hidden"
mkdir -p "$r5b/.agents/skills" "$r5b/.claude" && ln -s ../.agents/skills "$r5b/.claude/skills"
ok apply "$r5b"
installed "$r5b" .agents
# Dropping a harness whose folder is now a link to the other's: a delete
# through it would take the kept harness's copies, so apply refuses.
r5c=$tmp/link-dropped
repo "$r5c" "harnesses: claude codex" "skills-home: hidden"
ok apply "$r5c"
rm -r "$r5c/.claude/skills" && ln -s ../.agents/skills "$r5c/.claude/skills"
sed -i 's/^harnesses: .*/harnesses: codex/' "$r5c/.maestro/project/install.yaml"
refuses "$r5c/.claude/skills is a symlink" "$r5c" apply "$r5c"
r6=$tmp/nostore
repo "$r6" "harnesses: claude" "skills-home: hidden"
STUB_FAIL=1 refuses "doctor failed" "$r6" apply "$r6"
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
git -C "$r7" worktree remove --force "$tmp/wt7"
# With a bare main repo, a linked worktree takes the install, and a second
# worktree then refuses.
r11=$tmp/bare
git clone -q --bare "$r2" "$r11/.git"
git -C "$r11/.git" worktree add -q "$r11/main" main
git -C "$r11/.git" worktree add -q -b feat "$r11/feat"
mkdir -p "$r11/main/.maestro/project" "$r11/main/.maestro/src"
printf '%s\n' "maestro-sha: $sha" "harnesses: claude" "skills-home: hidden" >"$r11/main/.maestro/project/install.yaml"
ok apply "$r11/main"
installed "$r11/main" .claude
refuses "Maestro is installed in $r11/main" "$r11/feat" apply "$r11/feat"
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
