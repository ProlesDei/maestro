#!/usr/bin/env bash
# Test for skills/maestro-qa-lead/scripts/screens.sh, with a stand-in
# playwright-cli that fetches each page the script asks for through the
# script's own file server and writes a stand-in image for it. Covers bad
# arguments, no browser, a browser that won't open or fails mid-run,
# shots and sheets for local files and a URL in light and dark, the
# served folders and a path that climbs out of them, and that the server
# and browser are gone after.
# Run from anywhere: bash scripts/screens_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
shots=$here/skills/maestro-qa-lead/scripts/screens.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { cat "$tmp/out" 2>/dev/null || true; echo "FAIL: $*" >&2; exit 1; }
has() { grep -qxF -- "$1" "$tmp/out" || fail "want line '$1'"; }
count() { [ "$(grep -c "^$1=" "$tmp/out" || true)" = "$2" ] || fail "want $2 $1= lines"; }

node=$(command -v node) || { echo "SKIP: no node" >&2; exit 0; }
export TMPDIR=$tmp/t LOG=$tmp/pw.log
mkdir -p "$TMPDIR" "$tmp/bin" "$tmp/pages" "$tmp/other"

# The stand-in. run-code reads the jobs from the code file, fetches each
# URL (it must answer 200) and writes a stand-in image to its path.
cat >"$tmp/bin/playwright-cli" <<'EOF'
#!/usr/bin/env bash
s=${1#-s=}; shift
echo "$s $1 $PWD" >>"$LOG"
case $1 in
open) [ -z "${PW_NO_OPEN-}" ] || { echo "Error: no browser here" >&2; exit 1; } ;;
close) ;;
run-code)
	[ -z "${PW_RUN_ERR-}" ] || { echo "### Error"; echo "Error: page crashed"; exit 0; }
	f=${2#--filename=}
	node -e '
const fs = require("fs"), http = require("http");
const src = fs.readFileSync(process.argv[1], "utf8");
const jobs = JSON.parse(src.slice(src.indexOf("const jobs =") + 12, src.indexOf(";\n  for")));
const get = u => new Promise(ok => http.get(u, r => { r.resume(); ok(r.statusCode); }).on("error", () => ok(0)));
(async () => {
  for (const j of jobs) {
    const code = j.url.startsWith("http://127.0.0.1") ? await get(j.url) : 200;
    if (code !== 200) { console.log("### Error\nfetch " + j.url + " gave " + code); return; }
    fs.appendFileSync(process.env.LOG, "job " + j.url.replace(/:[0-9]+/, ":PORT") + " " + j.width + " " + j.scheme + "\n");
    fs.writeFileSync(j.path, "png");
  }
  const base = (jobs.find(j => j.url.startsWith("http://127.0.0.1")) || {}).url;
  if (base) {
    const root = base.replace(/(:[0-9]+).*/, "$1");
    for (const p of ["/p/..%2F..%2Fsecret", "/p/../other/x.html", "/x/a.html", "/o/..%2Fsecret", "/p/50%.png", "/p/a.html"])
      fs.appendFileSync(process.env.LOG, "probe " + p + " " + await get(root + p) + "\n");
    fs.writeFileSync(process.env.LOG + ".port", root);
  }
})();' "$f"
	;;
*) echo "stand-in: $*" >&2; exit 9 ;;
esac
EOF
chmod +x "$tmp/bin/playwright-cli"
PATH=$tmp/bin:$(dirname "$node"):/usr/bin:/bin
export PATH
echo '<h1>a</h1>' >"$tmp/pages/a.html"
echo '<h1>b</h1>' >"$tmp/pages/b.html"
echo '<h1>s</h1>' >"$tmp/pages/sheet-x.html"
echo 'no' >"$tmp/secret"
echo '<h1>o</h1>' >"$tmp/other/x.html"
run() { bash "$shots" "$@" >"$tmp/out" 2>&1; }

# Bad arguments.
for args in "--out $tmp/o --widths 375 $tmp/pages/a.html" \
	"--shoot --widths 375 $tmp/pages/a.html" \
	"--shoot --out $tmp/o --widths 375" \
	"--shoot --out $tmp/o --widths 375px $tmp/pages/a.html" \
	"--shoot --out $tmp/o --widths 375 --run a/b $tmp/pages/a.html" \
	"--shoot --out $tmp/o --widths 375 $tmp/pages/nope.html" \
	"--shoot --out $tmp/o --widths 375 $tmp/pages/a.html $tmp/other/x.html" \
	"--shoot --out $tmp/o --widths 375 $tmp/pages/a.html $tmp/pages/a.html" \
	"--shoot --out $tmp/o --widths 375 --bogus $tmp/pages/a.html"; do
	# shellcheck disable=SC2086
	if run $args; then fail "accepted: $args"; else rc=$?; [ $rc = 2 ] || fail "exit $rc for: $args"; fi
done
[ ! -e "$tmp/o" ] || fail "bad arguments made the output folder"

# No browser on PATH.
if PATH=/usr/bin:/bin run --shoot --out "$tmp/o" --widths 375 "$tmp/pages/a.html"; then fail "ran with no browser"; else rc=$?; [ $rc = 4 ] || fail "exit $rc with no browser"; fi
has "stop=no browser"
# A browser that won't open.
if PW_NO_OPEN=1 run --shoot --out "$tmp/o" --widths 375 --run t-open "$tmp/pages/a.html"; then fail "ran when the browser wouldn't open"; else rc=$?; [ $rc = 4 ] || fail "exit $rc when open failed"; fi
has "stop=no browser"
# A browser that fails mid-run: exit 1, and the session still closes.
: >"$LOG"
if PW_RUN_ERR=1 run --shoot --out "$tmp/o" --widths 375 --run t-err "$tmp/pages/a.html"; then fail "passed a failed run"; else rc=$?; [ $rc = 1 ] || fail "exit $rc on a failed run"; fi
grep -q "^t-err close " "$LOG" || fail "the browser wasn't closed after a failure"

# Shots and sheets: two local pages and a URL, two widths, light and dark.
: >"$LOG"
run --shoot --out "$tmp/o" --widths 375,1280 --dark --run t-ok "$tmp/pages/a.html" "$tmp/pages/sheet-x.html" https://example.test/x || fail "a good run failed"
has "run=t-ok"
has "ok=1"
count shot 12
count sheet 4
for f in a-375 a-375-dark a-1280 a-1280-dark page-sheet-x-375 page3-1280-dark sheet-375 sheet-375-dark sheet-1280 sheet-1280-dark; do
	has "$( [[ $f == sheet-* ]] && echo sheet || echo shot)=$tmp/o/$f.png"
	[ -s "$tmp/o/$f.png" ] || fail "no file $f.png"
done
! ls "$tmp/o"/.sheet-* >/dev/null 2>&1 || fail "the sheet pages were left behind"
grep -qxF "job http://127.0.0.1:PORT/p/a.html 375 light" "$LOG" || fail "a local page wasn't served from /p/"
grep -qxF "job http://127.0.0.1:PORT/p/a.html 1280 dark" "$LOG" || fail "no dark shot at 1280"
grep -qxF "job https://example.test/x 375 light" "$LOG" || fail "a URL wasn't shot as given"
grep -qxF "job http://127.0.0.1:PORT/o/.sheet-1280-dark.html 1600 light" "$LOG" || fail "no 1600-wide sheet shot"
for p in "/p/..%2F..%2Fsecret" "/p/../other/x.html" "/x/a.html" "/o/..%2Fsecret"; do
	grep -qE "^probe $p 404$" "$LOG" || fail "the server answered $p: $(grep "^probe $p " "$LOG")"
done
grep -qxF "probe /p/50%.png 400" "$LOG" || fail "a bad escape got: $(grep "^probe /p/50" "$LOG")"
grep -qxF "probe /p/a.html 200" "$LOG" || fail "the server died after a bad escape"
grep -qE "^t-ok run-code $tmp/o$" "$LOG" || fail "browser commands didn't run from the output folder"
grep -q "^t-ok close " "$LOG" || fail "the browser wasn't closed"
root=$(cat "$LOG.port")
if "$node" -e 'require("http").get(process.argv[1] + "/p/a.html", () => process.exit(1)).on("error", () => process.exit(0))' "$root"; then :; else fail "the file server still answers after the run"; fi

# One page: one column, and a run name made from random letters.
run --shoot --out "$tmp/o2" --widths 375 "$tmp/pages/b.html" || fail "a one-page run failed"
grep -qE "^run=shots-[a-z0-9]{6}$" "$tmp/out" || fail "no random run name"
count shot 1
count sheet 1

echo "screens_test: ok"
