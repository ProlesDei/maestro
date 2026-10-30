#!/usr/bin/env bash
# Acceptance test for scripts/install.sh: a fresh repo installs hidden from
# git, with project-setup.md written once; --tickets swaps in a team's
# maestro-create-ticket; a team's own unprefixed skills
# don't clash; a second run, a maestro- skill already there, a non-repo
# (also through GIT_DIR), a subdirectory, a symlinked .claude, a bad
# --tickets dir, a failed doctor and a failed copy all refuse and change
# nothing.
# Run from anywhere: bash scripts/install_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
skills=$(cd "$here/../skills" && pwd)
template=$(cd "$here/../templates" && pwd)/project-setup.md
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

# A stand-in maestro-ticket: logs where doctor ran, fails when STUB_FAIL is set.
mkdir "$tmp/bin"
printf '%s\n' '#!/usr/bin/env bash' 'echo "$PWD $*" >>"$STUB_LOG"' '[ -z "${STUB_FAIL:-}" ]' >"$tmp/bin/maestro-ticket"
chmod +x "$tmp/bin/maestro-ticket"
export PATH="$tmp/bin:$PATH" STUB_LOG="$tmp/doctor.log"

install() { bash "$here/install.sh" "$@" >"$tmp/out" 2>&1; }
# A tree snapshot: every path and file checksum, .git included.
snap() { (cd "$1" && find . -type f -print0 | sort -z | xargs -0 cksum && find . | sort); }
# refuses <message> <repo> <args...>: install <args> exits non-zero, prints
# <message> and leaves <repo> as it was.
refuses() {
	local msg=$1 repo=$2 before
	shift 2
	before=$(snap "$repo")
	if install "$@"; then fail "install $* exited 0"; fi
	grep -qF -- "$msg" "$tmp/out" || { cat "$tmp/out"; fail "install $* didn't say: $msg"; }
	[ "$before" = "$(snap "$repo")" ] || fail "install $* changed $repo"
}
# same <a> <b>: the two folders hold the same files.
same() { diff -r "$1" "$2" >/dev/null || fail "$2 differs from $1"; }

# A space in the path, and a user exclude file with no final newline.
repo="$tmp/my repo"
git init -q "$repo"
printf '# mine\n*.log' >"$repo/.git/info/exclude"
install "$repo" || { cat "$tmp/out"; fail "first run exited non-zero"; }
for dir in "$skills"/*/; do
	name=$(basename "$dir")
	for harness in .claude .agents; do
		same "$dir" "$repo/$harness/skills/$name"
	done
done
cmp -s "$template" "$repo/.maestro/project/project-setup.md" || fail "project-setup.md isn't the template"
grep -qxF '*.log' "$repo/.git/info/exclude" || fail "the user's exclude line was changed"
grep -qxF "add to your shell profile: export MAESTRO_PLAYBOOKS=$(cd "$here/.." && pwd)/playbooks" "$tmp/out" ||
	{ cat "$tmp/out"; fail "install didn't print the MAESTRO_PLAYBOOKS line"; }
seen=$(git -C "$repo" status --porcelain --untracked-files=all)
[ "$seen" = "?? .maestro/project/project-setup.md" ] || fail "git should see only project-setup.md: $seen"
[ "$(cat "$STUB_LOG")" = "$(cd "$repo" && pwd -P) doctor" ] || fail "doctor didn't run once, in the repo: $(cat "$STUB_LOG")"

refuses "nothing was written" "$repo" "$repo"
[ "$(wc -l <"$STUB_LOG")" -eq 1 ] || fail "second run ran doctor"

mkdir "$tmp/plain"
refuses "not in a git work tree" "$tmp/plain" "$tmp/plain"
export GIT_DIR="$repo/.git"
refuses "not in a git work tree" "$repo" "$tmp/plain"
unset GIT_DIR

git init -q "$tmp/link"
mkdir "$tmp/link/elsewhere"
ln -s elsewhere "$tmp/link/.claude"
refuses "is a symlink" "$tmp/link" "$tmp/link"

git init -q "$tmp/sub"
mkdir "$tmp/sub/pkg"
refuses "install at the top of the repo" "$tmp/sub" "$tmp/sub/pkg"

# A project-setup.md already there stays as it was.
git init -q "$tmp/setup"
mkdir -p "$tmp/setup/.maestro/project"
echo mine >"$tmp/setup/.maestro/project/project-setup.md"
install "$tmp/setup" || { cat "$tmp/out"; fail "install with a project-setup.md exited non-zero"; }
[ "$(cat "$tmp/setup/.maestro/project/project-setup.md")" = mine ] || fail "install overwrote project-setup.md"

# --tickets: the team's maestro-create-ticket replaces Maestro's, and its
# .git is skipped.
team="$tmp/team one"
mkdir -p "$team/maestro-create-ticket/agents" "$team/.git"
printf '%s\n' '---' 'name: maestro-create-ticket' '---' 'Our tickets.' >"$team/maestro-create-ticket/SKILL.md"
echo 'policy: {allow_implicit_invocation: false}' >"$team/maestro-create-ticket/agents/openai.yaml"
git init -q "$tmp/team"
install --tickets "$team" "$tmp/team" || { cat "$tmp/out"; fail "install --tickets exited non-zero"; }
for harness in .claude .agents; do
	same "$team/maestro-create-ticket" "$tmp/team/$harness/skills/maestro-create-ticket"
	same "$skills/maestro-code-review" "$tmp/team/$harness/skills/maestro-code-review"
done

# A bad --tickets dir refuses before doctor runs or anything is written.
git init -q "$tmp/bad"
refuses "is not a directory" "$tmp/bad" --tickets "$tmp/nope" "$tmp/bad"
mkdir -p "$tmp/t-nocreate/maestro-work"
echo x >"$tmp/t-nocreate/maestro-work/SKILL.md"
refuses "has no maestro-create-ticket/" "$tmp/bad" --tickets "$tmp/t-nocreate" "$tmp/bad"
mkdir -p "$tmp/t-noskill/maestro-create-ticket"
refuses "isn't a skill" "$tmp/bad" --tickets "$tmp/t-noskill" "$tmp/bad"
cp -R "$team" "$tmp/t-extra"
mkdir "$tmp/t-extra/maestro-work"
refuses "may hold only maestro-create-ticket/" "$tmp/bad" --tickets "$tmp/t-extra" "$tmp/bad"
mkdir -p "$tmp/t-old/create-ticket"
echo x >"$tmp/t-old/create-ticket/SKILL.md"
refuses "has no maestro-create-ticket/" "$tmp/bad" --tickets "$tmp/t-old" "$tmp/bad"
refuses "usage:" "$tmp/bad" --tickets
refuses "--tickets needs a directory" "$tmp/bad" --tickets "" "$tmp/bad"
[ "$(wc -l <"$STUB_LOG")" -eq 3 ] || fail "doctor ran for a refused install: $(cat "$STUB_LOG")"

# A team's own work and review skills don't clash; a maestro-code-review does.
git init -q "$tmp/own"
for name in work review code-review create-ticket; do
	mkdir -p "$tmp/own/.claude/skills/$name"
	echo mine >"$tmp/own/.claude/skills/$name/SKILL.md"
done
install "$tmp/own" || { cat "$tmp/out"; fail "install beside the team's own skills exited non-zero"; }
for name in work review code-review create-ticket; do
	[ "$(cat "$tmp/own/.claude/skills/$name/SKILL.md")" = mine ] || fail "install changed the team's $name skill"
done
same "$skills/maestro-code-review" "$tmp/own/.claude/skills/maestro-code-review"
git init -q "$tmp/clash"
mkdir -p "$tmp/clash/.agents/skills/maestro-code-review"
refuses ".agents/skills/maestro-code-review already exists" "$tmp/clash" "$tmp/clash"

git init -q "$tmp/nostore"
export STUB_FAIL=1
refuses "doctor failed" "$tmp/nostore" "$tmp/nostore"
unset STUB_FAIL

# .claude/skills is copied in full before .agents/skills can't be made.
git init -q "$tmp/half"
echo mine >"$tmp/half/.agents"
refuses "" "$tmp/half" "$tmp/half"

# A harness writes into the .claude/ this run made, then a copy fails: the
# rollback removes the skills and keeps that file.
git init -q "$tmp/busy"
mkdir "$tmp/cpbin"
printf '%s\n' '#!/usr/bin/env bash' 'case "$*" in *.agents/skills/*)' \
	'	echo "{}" >"$BUSY/.claude/settings.local.json"; exit 1 ;; esac' 'exec /bin/cp "$@"' >"$tmp/cpbin/cp"
chmod +x "$tmp/cpbin/cp"
if BUSY="$tmp/busy" PATH="$tmp/cpbin:$PATH" install "$tmp/busy"; then fail "install with a failing cp exited 0"; fi
left=$(cd "$tmp/busy" && find . -path ./.git -prune -o -print | sort | tr '\n' ' ')
[ "$left" = ". ./.claude ./.claude/settings.local.json " ] || fail "rollback left: $left"

echo "ok: install.sh installs once, hidden from git, keeps project-setup.md, takes a team's ticket method, and otherwise changes nothing"
