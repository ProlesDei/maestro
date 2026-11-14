#!/usr/bin/env bash
# screens.sh: screenshots of pages at set widths, and one contact sheet
# per width, in one headless browser of its own, so a session spends no
# calls on browser steps and looks at one image per width, not one per
# page.
#
#   screens.sh --shoot --out <dir> --widths 375,1280 [--dark] [--run <name>] <page>...
#     <page> is an http(s) URL or a local HTML file; local files must
#     share one folder, which is served on 127.0.0.1, port 0, for the
#     run. Writes <name>-<width>.png per page and width (-dark too with
#     --dark), then sheet-<width>.png (and sheet-<width>-dark.png) with
#     every page side by side, all in <dir>. Prints key=value lines:
#     run=, then shot= and sheet= per file, then ok=1.
#
# Needs playwright-cli and node on PATH. With no browser it prints
# stop=no browser and exits 4. Exit 2: bad arguments; 1: any other
# failure. The browser session is named after the run and closed on exit;
# the file server stops with this script.
set -euo pipefail

die() { echo "screens: $2" >&2; exit "$1"; }

shoot='' out='' widths='' dark='' run=''
pages=()
while [ $# -gt 0 ]; do
	case $1 in
	--shoot) shoot=1; shift ;;
	--out) [ $# -ge 2 ] || die 2 "--out needs a folder"; out=$2; shift 2 ;;
	--widths) [ $# -ge 2 ] || die 2 "--widths needs a list"; widths=$2; shift 2 ;;
	--dark) dark=1; shift ;;
	--run) [ $# -ge 2 ] || die 2 "--run needs a name"; run=$2; shift 2 ;;
	--) shift; pages+=("$@"); break ;;
	-*) die 2 "unknown flag $1" ;;
	*) pages+=("$1"); shift ;;
	esac
done
[ -n "$shoot" ] || die 2 "usage: screens.sh --shoot --out <dir> --widths 375,1280 [--dark] [--run <name>] <page>..."
[ -n "$out" ] || die 2 "give --out <dir>"
[ ${#pages[@]} -gt 0 ] || die 2 "give at least one page"
[[ $widths =~ ^[0-9]{2,4}(,[0-9]{2,4})*$ ]] || die 2 "--widths must be numbers joined by commas, such as 375,1280"
[ -z "$run" ] || [[ $run =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die 2 "--run must be letters, digits, dot, dash or underscore"
if [ -z "$run" ]; then
	run=shots-$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')
fi

if ! command -v playwright-cli >/dev/null 2>&1 || ! command -v node >/dev/null 2>&1; then
	echo "run=$run"
	echo "stop=no browser"
	exit 4
fi

# Pages: URLs pass through; local files are served from their one folder.
pdir=
names=()
urls=()
seen=" "
for p in "${pages[@]}"; do
	if [[ $p =~ ^https?:// ]]; then
		n=page$((${#names[@]} + 1))
		urls+=("$p")
	else
		[ -f "$p" ] || die 2 "no such file: $p"
		d=$(cd "$(dirname "$p")" && pwd)
		[ -z "$pdir" ] || [ "$d" = "$pdir" ] || die 2 "local pages must share one folder: $p"
		pdir=$d
		b=$(basename "$p")
		n=${b%.*}
		urls+=("@/p/$b")
	fi
	n=$(printf '%s' "$n" | LC_ALL=C tr -c 'A-Za-z0-9._-' '-')
	case $n in sheet-*) n=page-$n ;; esac
	case $seen in *" $n "*) die 2 "two pages would share the name $n" ;; esac
	seen="$seen$n "
	names+=("$n")
done

mkdir -p "$out"
out=$(cd "$out" && pwd)
tmp=$(mktemp -d)
srv_pid=
cleanup() {
	(cd "$out" && playwright-cli -s="$run" close >/dev/null 2>&1) || true
	[ -z "$srv_pid" ] || kill "$srv_pid" 2>/dev/null || true
	rm -rf "$tmp"
}
trap cleanup EXIT

# The file server: /p/ is the pages' folder, /o/ the output folder, for
# the sheets. Bound to 127.0.0.1 on port 0; it exits once this script
# does, even on a kill this trap can't catch.
cat >"$tmp/serve.js" <<'EOF'
const http = require("http"), fs = require("fs"), path = require("path");
const roots = { p: process.argv[2], o: process.argv[3] };
const parent = process.ppid;
const types = { ".html": "text/html", ".css": "text/css", ".js": "text/javascript", ".png": "image/png",
  ".jpg": "image/jpeg", ".svg": "image/svg+xml", ".woff2": "font/woff2", ".json": "application/json" };
const s = http.createServer((q, r) => {
  const u = decodeURIComponent(new URL(q.url, "http://x").pathname).split("/");
  const root = roots[u[1]];
  const f = root && path.resolve(root, "." + path.sep + u.slice(2).join(path.sep));
  if (!root || (f !== root && !f.startsWith(root + path.sep))) { r.writeHead(404); return r.end(); }
  fs.readFile(f, (e, d) => {
    if (e) { r.writeHead(404); return r.end(); }
    r.writeHead(200, { "content-type": types[path.extname(f)] || "application/octet-stream" });
    r.end(d);
  });
});
s.listen(0, "127.0.0.1", () => console.log(s.address().port));
setInterval(() => { try { process.kill(parent, 0); } catch { process.exit(0); } }, 2000).unref();
EOF
node "$tmp/serve.js" "${pdir:-$tmp}" "$out" >"$tmp/port" &
srv_pid=$!
for _ in $(seq 50); do [ -s "$tmp/port" ] && break; sleep 0.1; done
port=$(cat "$tmp/port")
[[ $port =~ ^[0-9]+$ ]] || die 1 "the file server didn't start"
base=http://127.0.0.1:$port

schemes=light
[ -z "$dark" ] || schemes="light dark"
IFS=, read -r -a ws <<<"$widths"

# One entry per shot, made from "url|path|width|scheme" lines; the
# browser shoots them all in one call.
mkjobs() { # <file>
	jq -R -s 'split("\n") | map(select(length > 0) | split("|") | {url: .[0], path: .[1], width: (.[2] | tonumber), scheme: .[3]})' >"$1"
}
cat >"$tmp/run.tail" <<'EOF'
;
  for (const j of jobs) {
    await page.emulateMedia({ colorScheme: j.scheme });
    await page.setViewportSize({ width: j.width, height: 900 });
    await page.goto(j.url, { waitUntil: "load", timeout: 30000 });
    await page.screenshot({ path: j.path, fullPage: true });
  }
  return "shot " + jobs.length;
}
EOF
shoot_jobs() { # <jobs.json>
	{ printf 'async page => {\n  const jobs = '; cat "$1"; cat "$tmp/run.tail"; } >"$tmp/run.js"
	(cd "$out" && playwright-cli -s="$run" run-code --filename="$tmp/run.js") >"$tmp/log" 2>&1 || {
		cat "$tmp/log" >&2
		die 1 "the browser failed"
	}
	! grep -q '^### Error' "$tmp/log" || { cat "$tmp/log" >&2; die 1 "the browser failed"; }
}

(cd "$out" && playwright-cli -s="$run" open --browser chromium --idle-timeout 900000) >"$tmp/log" 2>&1 || {
	cat "$tmp/log" >&2
	echo "run=$run"
	echo "stop=no browser"
	exit 4
}

echo "run=$run"
for i in "${!names[@]}"; do
	u=${urls[$i]}
	[[ $u != @* ]] || u=$base${u#@}
	for w in "${ws[@]}"; do
		for sc in $schemes; do
			sfx=; [ "$sc" = light ] || sfx=-dark
			echo "$u|$out/${names[$i]}-$w$sfx.png|$w|$sc"
		done
	done
done | mkjobs "$tmp/jobs.json"
shoot_jobs "$tmp/jobs.json"
for i in "${!names[@]}"; do
	for w in "${ws[@]}"; do
		for sc in $schemes; do
			sfx=; [ "$sc" = light ] || sfx=-dark
			f=$out/${names[$i]}-$w$sfx.png
			[ -s "$f" ] || die 1 "no shot written: $f"
			echo "shot=$f"
		done
	done
done

# One sheet per width and scheme: every page side by side, labeled.
cols=${#names[@]}
[ "$cols" -le 4 ] || cols=4
for w in "${ws[@]}"; do
	for sc in $schemes; do
		sfx=; [ "$sc" = light ] || sfx=-dark
		{
			printf '<!doctype html><meta charset="utf-8"><style>body{margin:0;padding:16px;background:#fff;font:600 20px sans-serif;color:#111}'
			printf '.g{display:grid;grid-template-columns:repeat(%s,1fr);gap:16px;align-items:start}' "$cols"
			printf 'figure{margin:0}figcaption{padding:0 0 8px}img{width:100%%;border:1px solid #ccc}</style><div class="g">'
			for n in "${names[@]}"; do
				printf '<figure><figcaption>%s (%spx%s)</figcaption><img src="/o/%s"></figure>' "$n" "$w" "${sfx:+, dark}" "$n-$w$sfx.png"
			done
			printf '</div>\n'
		} >"$out/.sheet-$w$sfx.html"
		echo "$base/o/.sheet-$w$sfx.html|$out/sheet-$w$sfx.png|1600|light"
	done
done | mkjobs "$tmp/sheets.json"
shoot_jobs "$tmp/sheets.json"
rm -f "$out"/.sheet-*.html
for w in "${ws[@]}"; do
	for sc in $schemes; do
		sfx=; [ "$sc" = light ] || sfx=-dark
		[ -s "$out/sheet-$w$sfx.png" ] || die 1 "no sheet written: $out/sheet-$w$sfx.png"
		echo "sheet=$out/sheet-$w$sfx.png"
	done
done
echo "ok=1"
