#!/usr/bin/env bash
# Acceptance test for scripts/install.sh and scripts/bot-bundle.sh, run
# against a copy of Maestro in its own git repo so an update can change it.
# apply installs each skills-home, hidden or not as asked, and a second
# run changes nothing; an update replaces and removes only what it placed;
# a team's file or one changed since install clashes and changes nothing,
# until skip: or --replace; a switch of skills-home moves the copies;
# check finds what's wrong; uninstall removes only unchanged files; bad
# choices and bad repos refuse and change nothing.
# Run from anywhere: bash scripts/install_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { cat "$tmp/out" 2>/dev/null; echo "FAIL: $*" >&2; exit 1; }
export GIT_CONFIG_GLOBAL=$tmp/gitconfig GIT_CONFIG_NOSYSTEM=1 HOME=$tmp/home
printf '[user]\n\tname = t\n\temail = t@t\n[init]\n\tdefaultBranch = main\n' >"$GIT_CONFIG_GLOBAL"
mkdir "$HOME"

# A Maestro to install from: the parts INSTALL.md checks out.
m=$tmp/maestro
mkdir "$m"
cp -R "$here/../skills" "$here/../templates" "$here/../bot-profiles" "$m/"
mkdir "$m/scripts" && cp "$here/install.sh" "$here/bot-bundle.sh" "$m/scripts/"
git -C "$m" init -q && git -C "$m" add -A && git -C "$m" commit -q -m one
sha=$(git -C "$m" rev-parse --short HEAD)
skills=$(cd "$m/skills" && ls -d */ | tr -d / | sort)
nskills=$(echo "$skills" | wc -l)

# A stand-in maestro-ticket: logs where doctor ran, fails when STUB_FAIL is set.
mkdir "$tmp/bin"
printf '%s\n' '#!/usr/bin/env bash' 'echo "$PWD $*" >>"$STUB_LOG"' '[ -z "${STUB_FAIL:-}" ]' >"$tmp/bin/maestro-ticket"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$tmp/bin/maestro-play"
chmod +x "$tmp/bin/maestro-ticket" "$tmp/bin/maestro-play"
export PATH="$tmp/bin:$PATH" STUB_LOG="$tmp/doctor.log"
: >"$STUB_LOG"

run() { bash "$m/scripts/install.sh" "$@" >"$tmp/out" 2>&1; }
ok() { run "$@" || { cat "$tmp/out"; fail "install.sh $* exited non-zero"; }; }
says() { grep -qF -- "$1" "$tmp/out" || { cat "$tmp/out"; fail "output lacks: $1"; }; }
# A tree snapshot: every path and file checksum, .git included.
snap() { (cd "$1" && find . -type f -print0 | sort -z | xargs -0 cksum && find . | sort); }
# refuses <message> <repo> <args...>: install.sh <args> exits non-zero,
# prints <message> and leaves <repo> and ~ as they were.
refuses() {
	local msg=$1 repo=$2 before
	shift 2
	before=$(snap "$repo"; snap "$HOME")
	if run "$@"; then fail "install.sh $* exited 0"; fi
	says "$msg"
	[ "$before" = "$(snap "$repo"; snap "$HOME")" ] || fail "install.sh $* changed $repo or ~"
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
# installed <base> <harness dir>...: every skill is there, as Maestro has it.
installed() {
	local base=$1 d n
	shift
	for d in "$@"; do
		for n in $skills; do
			diff -r "$m/skills/$n" "$base/$d/skills/$n" >/dev/null || fail "$base/$d/skills/$n differs from skills/$n"
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
echo mine >>"$r/.claude/skills/maestro-tdd/SKILL.md"
rm "$r/.agents/skills/maestro-swarm/SKILL.md"
if run check "$r"; then fail "check passed with a changed and a missing file"; fi
says "changed since install: .claude/skills/maestro-tdd/SKILL.md"
says "missing: .agents/skills/maestro-swarm/SKILL.md"

# A file changed since install clashes until --replace.
refuses "clash: .claude/skills/maestro-tdd/SKILL.md changed since install" "$r" apply "$r"
ok apply --replace maestro-tdd "$r"
installed "$r" .claude .agents
ok check "$r"

# bot-bundle: one block per bot named, with the team's own memories.
mkdir -p "$r/.maestro/project/bots"
printf '# ours\n\nIntro.\n\n- Runs at once: up to 3.\n  Wrapped.\n' >"$r/.maestro/project/bots/planner.memory.md"
bash "$m/scripts/bot-bundle.sh" "$r" >"$tmp/out" || { cat "$tmp/out"; fail "bot-bundle exited non-zero"; }
[ "$(ls "$r/.maestro/bots" | sort | tr '\n' ' ')" = "a planner.md reviewer.md " ] || fail "bundles: $(ls "$r/.maestro/bots")"
b=$r/.maestro/bots/planner.md
grep -qxF -- "- Runs at once: up to 3." "$b" && grep -qxF "  Wrapped." "$b" || fail "planner block lacks the team's memory"
! grep -qF "Intro." "$b" || fail "planner block holds the memory file's intro"
grep -qF "$(sed -n 2p "$m/bot-profiles/team-rules.md")" "$b" || fail "planner block lacks the team rules"
! grep -q '^name: planner' "$b" || fail "planner block holds front matter"
grep -qxF -- "- Maestro: $sha" "$b" || fail "planner block lacks the sha"

# --- update: Maestro changes a skill, adds a file and drops a skill ---
before_exclude=$(cat "$r/.git/info/exclude")
echo "new line" >>"$m/skills/maestro-tdd/SKILL.md"
echo new >"$m/skills/maestro-recall/extra.md"
git -C "$m" rm -rq skills/maestro-prune
git -C "$m" add -A && git -C "$m" commit -q -m two
refuses "but install.yaml pins $sha" "$r" apply "$r"
sha2=$(git -C "$m" rev-parse --short HEAD)
sed -i "s/^maestro-sha: .*/maestro-sha: $sha2/" "$r/.maestro/project/install.yaml"
ok apply "$r"
skills=$(cd "$m/skills" && ls -d */ | tr -d / | sort)
installed "$r" .claude .agents
[ ! -e "$r/.claude/skills/maestro-prune" ] && [ ! -e "$r/.agents/skills/maestro-prune" ] || fail "update kept a skill Maestro dropped"
! grep -q maestro-prune "$r/.git/info/exclude" || fail "update kept the dropped skill's exclude line"
[ "$(echo "$before_exclude" | grep -v maestro-prune)" = "$(cat "$r/.git/info/exclude")" ] || fail "update changed other exclude lines"
sha=$sha2

# A team's skill folder by a Maestro name clashes until skip:.
r2=$tmp/own
repo "$r2" "harnesses: codex" "skills-home: hidden"
mkdir -p "$r2/.agents/skills/maestro-code-review" "$r2/.agents/skills/review"
echo mine >"$r2/.agents/skills/maestro-code-review/SKILL.md"
echo mine >"$r2/.agents/skills/review/SKILL.md"
refuses "clash: .agents/skills/maestro-code-review/SKILL.md isn't Maestro's" "$r2" apply "$r2"
echo "skip: maestro-code-review" >>"$r2/.maestro/project/install.yaml"
ok apply "$r2"
[ "$(cat "$r2/.agents/skills/maestro-code-review/SKILL.md")" = mine ] || fail "apply changed the team's skill"
[ "$(cat "$r2/.agents/skills/review/SKILL.md")" = mine ] || fail "apply changed the team's own review skill"
[ ! -e "$r2/.claude" ] || fail "codex alone wrote .claude/"

# --- switch hidden to user: the copies move to ~ ---
sed -i 's/^skills-home: .*/skills-home: user/' "$r/.maestro/project/install.yaml"
ok apply "$r"
installed "$HOME" .claude .agents
[ ! -e "$r/.claude/skills" ] && [ ! -e "$r/.agents/skills" ] || fail "the switch left copies in the repo"
[ ! -e "$r/.git/maestro/installed.list" ] || [ "$(head -c2 <(cut -f2 "$r/.git/maestro/installed.list" | sed -n 2p))" = "~/" ] ||
	fail "user record paths should start with ~/"
ok check "$r"

# --- switch user to committed: the copies show up in git ---
sed -i 's/^skills-home: .*/skills-home: committed/' "$r/.maestro/project/install.yaml"
ok apply "$r"
installed "$r" .claude .agents
[ ! -e "$HOME/.claude/skills/maestro-tdd" ] || fail "the switch left copies in ~"
[ -f "$r/.maestro/project/installed.list" ] && [ ! -e "$r/.git/maestro/installed.list" ] || fail "committed record should be in .maestro/project only"
git -C "$r" status --porcelain --untracked-files=all | grep -q '^?? .claude/skills/maestro-tdd/SKILL.md$' || fail "committed skills are hidden from git"

# --- uninstall: removes unchanged files, keeps a changed one ---
echo mine >>"$r/.agents/skills/maestro-tdd/SKILL.md"
if run uninstall "$r"; then fail "uninstall with a changed file exited 0"; fi
says "kept, changed since install: .agents/skills/maestro-tdd/SKILL.md"
left=$(cd "$r" && { find .claude .agents -type f 2>/dev/null || true; } | sort | tr '\n' ' ')
[ "$left" = ".agents/skills/maestro-tdd/SKILL.md " ] || fail "uninstall left: $left"
[ ! -e "$r/.claude" ] || fail "uninstall left an empty .claude/"
! grep -q 'maestro install' "$r/.git/info/exclude" || fail "uninstall left the exclude block"
grep -qxF '*.log' "$r/.git/info/exclude" || fail "uninstall changed the user's exclude line"
[ -f "$r/.maestro/project/install.yaml" ] && [ -f "$r/.maestro/project/project-setup.md" ] || fail "uninstall removed the team's choices or setup"

# --- tickets: the team's maestro-create-ticket replaces Maestro's ---
r3=$tmp/team
repo "$r3" "harnesses: claude" "skills-home: hidden" "tickets: our tickets"
t="$r3/our tickets"
mkdir -p "$t/maestro-create-ticket/agents" "$t/.git"
printf '%s\n' '---' 'name: maestro-create-ticket' '---' 'Our tickets.' >"$t/maestro-create-ticket/SKILL.md"
echo 'policy: {}' >"$t/maestro-create-ticket/agents/openai.yaml"
lines=$(wc -l <"$STUB_LOG")
ok apply "$r3"
says "ticket method from"
diff -r "$t/maestro-create-ticket" "$r3/.claude/skills/maestro-create-ticket" >/dev/null || fail "the team's ticket skill wasn't installed"
[ "$(wc -l <"$STUB_LOG")" -eq "$lines" ] || fail "doctor ran with the team's own tickets"
mkdir "$t/maestro-work"
refuses "may hold only maestro-create-ticket/" "$r3" apply "$r3"

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
skip: maestro-nope|no skill maestro-nope
bots: designer|no bot profile designer
EOF
for bad in "harnesses: vim|unknown harness vim" "skills-home: attic|skills-home wants committed, hidden or user"; do
	sed "s/^${bad%%:*}: .*/${bad%%|*}/" "$tmp/keep" >"$r4/.maestro/project/install.yaml"
	refuses "${bad#*|}" "$r4" apply "$r4"
done
cp "$tmp/keep" "$r4/.maestro/project/install.yaml"
sed -i 's/^maestro-sha: .*/maestro-sha: 0000000/' "$r4/.maestro/project/install.yaml"
refuses "but install.yaml pins 0000000" "$r4" apply "$r4"
rm "$r4/.maestro/project/install.yaml"
refuses "no $r4/.maestro/project/install.yaml" "$r4" apply "$r4"
r5=$tmp/link
repo "$r5" "harnesses: claude" "skills-home: hidden"
mkdir "$tmp/elsewhere" && ln -s "$tmp/elsewhere" "$r5/.claude"
refuses "is a symlink" "$r5" apply "$r5"
r6=$tmp/nostore
repo "$r6" "harnesses: claude" "skills-home: hidden"
STUB_FAIL=1 refuses "doctor failed" "$r6" apply "$r6"
refuses "usage:" "$r6" frob "$r6"

# A failed copy leaves a partial install that a second apply finishes.
r7=$tmp/half
repo "$r7" "harnesses: claude codex" "skills-home: hidden"
mkdir "$tmp/cpbin"
printf '%s\n' '#!/usr/bin/env bash' 'case "$*" in *.agents/skills/maestro-tdd/*) exit 1 ;; esac' 'exec /bin/cp "$@"' >"$tmp/cpbin/cp"
chmod +x "$tmp/cpbin/cp"
if PATH="$tmp/cpbin:$PATH" run apply "$r7"; then fail "apply with a failing cp exited 0"; fi
ok apply "$r7"
installed "$r7" .claude .agents
ok check "$r7"

echo "ok: install.sh applies each skills-home, updates and switches cleanly, refuses clashes and bad choices, and uninstalls only what it placed; bot-bundle builds each bot's block"
