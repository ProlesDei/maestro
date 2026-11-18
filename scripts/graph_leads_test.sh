#!/usr/bin/env bash
# Test for skills/maestro-code-review/scripts/graph-leads.sh, with the
# real codebase-memory-mcp (skipped when it isn't on PATH), on a scratch
# repo whose change copies a helper, adds a function nothing calls and
# one over the complexity line. Covers the four lists, a moved file, an
# added line that reads as a file header, graph=none with no tool, a bad
# base, and that no index is left behind.
# Run from anywhere: bash scripts/graph_leads_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
leads=$here/skills/maestro-code-review/scripts/graph-leads.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { cat "$tmp/out" "$tmp/md" 2>/dev/null || true; echo "FAIL: $*" >&2; exit 1; }
has() { grep -qF -- "$1" "$tmp/md" || fail "graph.md wants '$1'"; }
lacks() { ! grep -qF -- "$1" "$tmp/md" || fail "graph.md doesn't want '$1'"; }

if ! command -v codebase-memory-mcp >/dev/null; then
	echo "skip: no codebase-memory-mcp on PATH"
	exit 0
fi
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
cache=${XDG_CACHE_HOME:-$HOME/.cache}/codebase-memory-mcp
indexes() { find "$cache" -maxdepth 1 -name 'maestro-leads-*' 2>/dev/null | wc -l; }
before=$(indexes)

r=$tmp/repo
mkdir -p "$r/app"
git -C "$r" init -q -b main
cat >"$r/app/store.py" <<'EOF'
import os
import tempfile


def write_atomic(path, data):
    fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path), prefix=".part.")
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp, path)
    except BaseException:
        os.unlink(tmp)
        raise


def read_all(path):
    with open(path, "rb") as handle:
        return handle.read()
EOF
# Past a pipe's buffer, so a grep -q that stops early would cut its writer off.
for i in $(seq 1 4000); do printf '# padding line %d, so the base file passes a pipe buffer\n' "$i"; done >>"$r/app/store.py"
cat >"$r/app/main.py" <<'EOF'
from app.store import read_all, write_atomic


def main(path):
    write_atomic(path, read_all(path))
EOF
git -C "$r" add -A && git -C "$r" commit -qm base
base=$(git -C "$r" rev-parse HEAD)

{
	cat <<'EOF'
import os
import tempfile

from app.store import read_all, write_atomic


def main(path):
    write_atomic(path, read_all(path))
    save_report(path, b"x")
    grade(3)


def save_report(target, body):
    fd, partial = tempfile.mkstemp(dir=os.path.dirname(target), prefix=".report.")
    try:
        with os.fdopen(fd, "wb") as out:
            out.write(body)
            out.flush()
            os.fsync(out.fileno())
        os.replace(partial, target)
    except BaseException:
        os.unlink(partial)
        raise


def never_used(x):
    return x * 2


def grade(n):
EOF
	for i in $(seq 1 16); do printf '    if n == %d:\n        return "g%d"\n' "$i" "$i"; done
	printf '    return "none"\n'
} >"$r/app/main.py"
# A change to a function already there, in the big file; and the change's
# own .gitattributes, which must hide no hunk.
sed -i 's/return handle.read()/return handle.read() or b""/' "$r/app/store.py"
echo '*.py -diff' >"$r/.gitattributes"
git -C "$r" add -A && git -C "$r" commit -qm change

# The four lists.
bash "$leads" "$r" "$base" "$tmp/md" >"$tmp/out" || fail "exit $?"
grep -qx 'graph=yes changed=5 new=3 uncalled=1 lookalikes=1 complex=1' "$tmp/out" || fail "counts: $(cat "$tmp/out")"
has "- app/main.py:7-10 main: "
has "app/main.py:13-23 save_report (new): app/main.py:7 main"
has "- app/main.py:26 never_used"
has "- app/main.py:13 save_report ~ app/store.py:5 write_atomic"
has "- app/main.py:30 grade: "
has "- app/store.py:18-20 read_all: "
lacks "read_all (new)"
[ "$(indexes)" -eq "$before" ] || fail "an index was left behind"

# A tree as the base, as review's delta base T is: the same leads.
bash "$leads" "$r" "$(git -C "$r" rev-parse "$base^{tree}")" "$tmp/md-tree" >"$tmp/out" || fail "tree base: exit $?"
cmp -s "$tmp/md" "$tmp/md-tree" || fail "tree base: graph.md differs"

# A moved file with one edit: its functions aren't new. And an added line
# that starts "++ ", which reads "+++ " in the diff: the hunk after it
# stays on its own file.
printf 'def first():\n    return 1\n\n\ndef second():\n    return 2\n' >"$r/app/text.py"
git -C "$r" add -A && git -C "$r" commit -qm text
base2=$(git -C "$r" rev-parse HEAD)
git -C "$r" mv app/main.py app/entry.py
sed -i 's/return x \* 2/return x * 3/' "$r/app/entry.py"
printf 'def first():\n    return """\n++ b/fake.py\n"""\n\n\ndef second():\n    return 3\n' >"$r/app/text.py"
git -C "$r" add -A && git -C "$r" commit -qm move
bash "$leads" "$r" "$base2" "$tmp/md" >"$tmp/out" || fail "move: exit $?"
grep -qx 'graph=yes changed=3 new=0 uncalled=0 lookalikes=0 complex=0' "$tmp/out" || fail "move: $(cat "$tmp/out")"
has "- app/entry.py:26-27 never_used: "
has "- app/text.py:7-8 second: "
lacks "fake.py"

# Uncommitted changes: refused, since the index reads the disk.
echo "# draft" >>"$r/app/entry.py"
code=0; bash "$leads" "$r" "$base" "$tmp/md4" >"$tmp/out" 2>&1 || code=$?
{ [ $code -eq 1 ] && grep -q "uncommitted changes" "$tmp/out"; } || fail "dirty tree: exit $code"
git -C "$r" checkout -q -- app/entry.py

# No tool: graph=none, nothing written.
mkdir "$tmp/bin"
for t in bash git jq od mktemp awk grep cat rm tail; do ln -s "$(command -v "$t")" "$tmp/bin/$t"; done
rm -f "$tmp/md2"
PATH=$tmp/bin bash "$leads" "$r" "$base" "$tmp/md2" >"$tmp/out" || fail "no tool: exit $?"
grep -qx 'graph=none: no codebase-memory-mcp on PATH' "$tmp/out" || fail "no tool: $(cat "$tmp/out")"
[ ! -e "$tmp/md2" ] || fail "no tool: wrote graph.md"

# A bad base: exit 1, nothing written, no index left.
code=0; bash "$leads" "$r" 0000000000000000000000000000000000000000 "$tmp/md3" >"$tmp/out" 2>&1 || code=$?
[ $code -eq 1 ] || fail "bad base: exit $code, want 1"
[ ! -e "$tmp/md3" ] || fail "bad base: wrote graph.md"
[ "$(indexes)" -eq "$before" ] || fail "bad base: an index was left behind"

echo "ok: graph-leads lists callers, uncalled new functions, look-alikes and complex functions, says graph=none with no tool, and leaves no index"
