#!/usr/bin/env bash
# Acceptance test for scripts/install.sh: a fresh repo installs hidden from
# git, with project-setup.md written once; --tickets swaps in a team's
# create-ticket and work; a second run, a non-repo (also through GIT_DIR), a
# subdirectory, a symlinked .claude, a bad --tickets dir, a failed doctor and
# a failed copy all refuse and change nothing.
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
[ -d "$repo/.maestro/project" ] && [ -d "$repo/.maestro/local" ] || fail ".maestro dirs missing"
cmp -s "$template" "$repo/.maestro/project/project-setup.md" || fail "project-setup.md isn't the template"
grep -qxF '*.log' "$repo/.git/info/exclude" || fail "the user's exclude line was changed"
[ "$(grep -cxF '.maestro/local/' "$repo/.git/info/exclude")" = 1 ] || fail ".maestro/local/ not excluded once"
touch "$repo/.maestro/local/environment.md"
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

# --tickets: the team's create-ticket replaces Maestro's, and its .git is
# skipped; work stays Maestro's until the team ships one too.
team="$tmp/team one"
mkdir -p "$team/create-ticket/agents" "$team/.git"
printf '%s\n' '---' 'name: create-ticket' '---' 'Our tickets.' >"$team/create-ticket/SKILL.md"
echo 'policy: {allow_implicit_invocation: false}' >"$team/create-ticket/agents/openai.yaml"
git init -q "$tmp/team"
install --tickets "$team" "$tmp/team" || { cat "$tmp/out"; fail "install --tickets exited non-zero"; }
for harness in .claude .agents; do
	same "$team/create-ticket" "$tmp/team/$harness/skills/create-ticket"
	same "$skills/work" "$tmp/team/$harness/skills/work"
	same "$skills/review" "$tmp/team/$harness/skills/review"
done
mkdir "$team/work"
echo 'Our work.' >"$team/work/SKILL.md"
git init -q "$tmp/team2"
install --tickets "$team" "$tmp/team2" || { cat "$tmp/out"; fail "install --tickets with work exited non-zero"; }
for harness in .claude .agents; do
	same "$team/work" "$tmp/team2/$harness/skills/work"
done

# A bad --tickets dir refuses before doctor runs or anything is written.
git init -q "$tmp/bad"
refuses "is not a directory" "$tmp/bad" --tickets "$tmp/nope" "$tmp/bad"
mkdir -p "$tmp/t-nocreate/work"
echo x >"$tmp/t-nocreate/work/SKILL.md"
refuses "has no create-ticket/" "$tmp/bad" --tickets "$tmp/t-nocreate" "$tmp/bad"
mkdir -p "$tmp/t-noskill/create-ticket"
refuses "isn't a skill" "$tmp/bad" --tickets "$tmp/t-noskill" "$tmp/bad"
cp -R "$team" "$tmp/t-extra"
mkdir "$tmp/t-extra/review"
refuses "may hold only create-ticket/ and work/" "$tmp/bad" --tickets "$tmp/t-extra" "$tmp/bad"
refuses "usage:" "$tmp/bad" --tickets
[ "$(wc -l <"$STUB_LOG")" -eq 4 ] || fail "doctor ran for a refused install: $(cat "$STUB_LOG")"

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
