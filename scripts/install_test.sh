#!/usr/bin/env bash
# Acceptance test for scripts/install.sh (decisions 0880, 0881): a fresh repo
# installs hidden from git; a second run, a non-repo (also through GIT_DIR), a
# subdirectory, a symlinked .claude, a failed doctor and a failed copy all
# refuse and change nothing.
# Run from anywhere: bash scripts/install_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
skills=$(cd "$here/../skills" && pwd)
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
# refuses <message> <repo> <arg>: install <arg> exits non-zero, prints <message>
# and leaves <repo> as it was.
refuses() {
	local before
	before=$(snap "$2")
	if install "$3"; then fail "install $3 exited 0"; fi
	grep -qF "$1" "$tmp/out" || { cat "$tmp/out"; fail "install $3 didn't say: $1"; }
	[ "$before" = "$(snap "$2")" ] || fail "install $3 changed $2"
}

# A space in the path, and a user exclude file with no final newline.
repo="$tmp/my repo"
git init -q "$repo"
printf '# mine\n*.log' >"$repo/.git/info/exclude"
install "$repo" || { cat "$tmp/out"; fail "first run exited non-zero"; }
for dir in "$skills"/*/; do
	name=$(basename "$dir")
	for harness in .claude .agents; do
		diff -r "$dir" "$repo/$harness/skills/$name" >/dev/null || fail "$harness/skills/$name differs from skills/$name"
	done
done
[ -d "$repo/.maestro/project" ] && [ -d "$repo/.maestro/local" ] || fail ".maestro dirs missing"
grep -qxF '*.log' "$repo/.git/info/exclude" || fail "the user's exclude line was changed"
[ "$(grep -cxF '.maestro/local/' "$repo/.git/info/exclude")" = 1 ] || fail ".maestro/local/ not excluded once"
touch "$repo/.maestro/local/environment.md"
[ -z "$(git -C "$repo" status --porcelain --untracked-files=all)" ] || fail "git sees installed files: $(git -C "$repo" status --porcelain)"
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

echo "ok: install.sh installs once, hidden from git, and otherwise changes nothing"
