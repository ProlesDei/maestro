#!/usr/bin/env bash
# screens.sh: screenshots of pages at set widths, and one contact sheet
# per width, in one headless browser of its own, so a session spends no
# calls on browser steps and looks at one image per width, not one per
# page. Or clips of what moves on them.
#
#   screens.sh --shoot --out <dir> --widths 375,1280 [--dark] [--run <name>] <page>...
#     <page> is an http(s) URL or a local HTML file; local files must
#     share one folder, which is served on 127.0.0.1, port 0, for the
#     run. Each page is shot once its animations have run to their end.
#     Writes <name>-<width>.png per page and width (-dark too with
#     --dark), then sheet-<width>.png (and sheet-<width>-dark.png) with
#     every page side by side, all in <dir>. Prints key=value lines:
#     run=, then shot= and sheet= per file, then ok=1.
#
#   screens.sh --motion --out <dir> --widths 375,1280 [--ms 1000] [--run <name>] <page>[#<cue>]...
#     Plays what moves on each page at each width, in the top 900px, on
#     a stepped clock: a frame every 40ms from load, for --ms (200 to
#     4000, default 1000). CSS transitions and animations, the Web
#     Animations API, timers and requestAnimationFrame all keep that
#     clock. With #<cue>, the page first settles, so what load starts
#     (timers, animations, what their endings start) runs its course;
#     then it calls the page's window.motion("<cue>") at 0ms. Writes
#     <name>[+<cue>]-<width>-motion.png, an animated PNG that loops, and
#     <name>[+<cue>]-<width>-strip.png, up to eight of its frames in a
#     grid, each labelled with its ms. A run first deletes every clip
#     and strip in <dir> of each page it clips, so clip every cue of a
#     page in one run; other pages' clips stay. Prints run=, then
#     clip= and strip= per pair; still=<name>[+<cue>]-<width> for each
#     clip where nothing moved, which writes no file; and cut=<same> for
#     each clip still moving on its last frame; then ok=1. If nothing
#     moved in any clip, it fails.
#
# Needs jq, and playwright-cli and node on PATH. With no browser it prints
# stop=no browser and exits 4. Exit 2: bad arguments; 1: any other
# failure. The browser session is named after the run and closed on exit;
# the file server stops with this script.
set -euo pipefail

die() { echo "screens: $2" >&2; exit "$1"; }

shoot='' motion='' out='' widths='' dark='' run='' ms=''
pages=()
while [ $# -gt 0 ]; do
	case $1 in
	--shoot) shoot=1; shift ;;
	--motion) motion=1; shift ;;
	--out) [ $# -ge 2 ] || die 2 "--out needs a folder"; out=$2; shift 2 ;;
	--widths) [ $# -ge 2 ] || die 2 "--widths needs a list"; widths=$2; shift 2 ;;
	--dark) dark=1; shift ;;
	--ms) [ $# -ge 2 ] || die 2 "--ms needs a number"; ms=$2; shift 2 ;;
	--run) [ $# -ge 2 ] || die 2 "--run needs a name"; run=$2; shift 2 ;;
	--) shift; pages+=("$@"); break ;;
	-*) die 2 "unknown flag $1" ;;
	*) pages+=("$1"); shift ;;
	esac
done
[ -n "$shoot$motion" ] && [ "$shoot$motion" != 11 ] ||
	die 2 "usage: screens.sh --shoot|--motion --out <dir> --widths 375,1280 [--dark] [--ms <n>] [--run <name>] <page>..."
[ -n "$out" ] || die 2 "give --out <dir>"
[ ${#pages[@]} -gt 0 ] || die 2 "give at least one page"
[[ $widths =~ ^[0-9]{2,4}(,[0-9]{2,4})*$ ]] || die 2 "--widths must be numbers joined by commas, such as 375,1280"
[ -z "$run" ] || [[ $run =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die 2 "--run must be letters, digits, dot, dash or underscore"
if [ -n "$motion" ]; then
	[ -z "$dark" ] || die 2 "--dark is for --shoot"
	ms=${ms:-1000}
	[[ $ms =~ ^[1-9][0-9]{2,3}$ ]] && [ "$ms" -ge 200 ] && [ "$ms" -le 4000 ] || die 2 "--ms must be a number from 200 to 4000"
else
	[ -z "$ms" ] || die 2 "--ms is for --motion"
fi
if [ -z "$run" ]; then
	run=shots-$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')
fi

command -v jq >/dev/null 2>&1 || die 1 "needs jq on PATH"
if ! command -v playwright-cli >/dev/null 2>&1 || ! command -v node >/dev/null 2>&1; then
	echo "run=$run"
	echo "stop=no browser"
	exit 4
fi

# Pages: URLs pass through; local files are served from their one folder.
# Under --motion, a #<cue> after a page names the motion to play.
pdir=
names=()
pnames=()
urls=()
cues=()
seen=" "
for p in "${pages[@]}"; do
	cue=
	if [ -n "$motion" ] && [[ $p == *#* ]]; then
		cue=${p##*#}
		p=${p%#*}
		[[ $cue =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$ ]] || die 2 "a cue must be letters, digits, dash or underscore: #$cue"
	fi
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
	pnames+=("$n")
	# A + (never in a page's name) sets the cue apart, so a-cart, a page,
	# and a+cart, a's cue, never share a clip name.
	[ -z "$cue" ] || n=$n+$cue
	case $seen in *" $n "*) die 2 "two pages would share the name $n" ;; esac
	seen="$seen$n "
	names+=("$n")
	cues+=("$cue")
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
# the sheets, and /f/ the clips' frames, for the strips. Bound to
# 127.0.0.1 on port 0; it exits once this script does, even on a kill
# this trap can't catch.
mkdir "$tmp/f"
cat >"$tmp/serve.js" <<'EOF'
const http = require("http"), fs = require("fs"), path = require("path");
const roots = { p: process.argv[2], o: process.argv[3], f: process.argv[4] };
const parent = process.ppid;
const types = { ".html": "text/html", ".css": "text/css", ".js": "text/javascript", ".png": "image/png",
  ".jpg": "image/jpeg", ".svg": "image/svg+xml", ".woff2": "font/woff2", ".json": "application/json" };
const s = http.createServer((q, r) => {
  let u;
  try { u = decodeURIComponent(new URL(q.url, "http://x").pathname).split("/"); }
  catch { r.writeHead(400); return r.end(); }
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
node "$tmp/serve.js" "${pdir:-$tmp}" "$out" "$tmp/f" >"$tmp/port" &
srv_pid=$!
for _ in $(seq 50); do [ -s "$tmp/port" ] && break; sleep 0.1; done
port=$(cat "$tmp/port")
[[ $port =~ ^[0-9]+$ ]] || die 1 "the file server didn't start"
base=http://127.0.0.1:$port

schemes=light
[ -z "$dark" ] || schemes="light dark"
IFS=, read -r -a ws <<<"$widths"

# One entry per shot, made from "url|path|width|scheme" lines; the
# browser shoots them all in one call. Running animations run to their
# end first, so a shot never catches one part way; one that loops stays
# where it is, and one the page paused (a bar held at 30%) stays paused.
mkjobs() { # <file>
	jq -R -s 'split("\n") | map(select(length > 0) | split("|") | {url: .[0], path: .[1], width: (.[2] | tonumber), scheme: .[3]})' >"$1"
}
cat >"$tmp/shoot.tail" <<'EOF'
;
  for (const j of jobs) {
    await page.emulateMedia({ colorScheme: j.scheme });
    await page.setViewportSize({ width: j.width, height: 900 });
    await page.goto(j.url, { waitUntil: "load", timeout: 30000 });
    await page.evaluate(() => {
      for (const a of document.getAnimations()) {
        if (a.playState !== "running") continue;
        try { a.finish(); } catch {}
      }
    });
    await page.screenshot({ path: j.path, fullPage: true });
  }
  return "shot " + jobs.length;
}
EOF
# A clip: a page on a fake clock, paused so only the steps move it. A
# clock covers its whole browser context, and a paused one would stall
# the session's own page, so each clip gets a context of its own. Each
# step moves the clock, then each animation the page plays: paused, and
# moved by the step times its playbackRate, so a reversed or faster one
# keeps its own course. One that starts mid-clip (a class a timer adds)
# counts from the step it shows up in. Past its end (or its start, when
# reversed) it is finished, so its events, finished promise and fill run
# as the page meant. One the page paused itself is left alone; one the
# page plays again is picked up where it stands. Under a cue, load
# settles first: up to three rounds of 5s page time, each ending what
# still runs, so load's timers and the handlers its endings start run
# before the cue, not inside its clip.
cat >"$tmp/motion.tail" <<'EOF'
;
  for (const j of jobs) {
    const browser = page.context().browser();
    if (!browser) throw new Error("--motion needs a browser of its own, not a persistent profile");
    const ctx = await browser.newContext();
    try {
      const p = await ctx.newPage();
      await p.clock.install({ time: 0 });
      await p.clock.pauseAt(1000);
      await p.setViewportSize({ width: j.width, height: 900 });
      await p.goto(j.url, { waitUntil: "load", timeout: 30000 });
      for (let r = 0; j.cue && r < 3; r++) {
        await p.clock.runFor(5000);
        const ended = await p.evaluate(() => {
          let n = 0;
          for (const a of document.getAnimations()) {
            if (a.playState !== "running") continue;
            try { a.finish(); n++; } catch {}
          }
          return n;
        });
        if (!ended) break;
        // Finish events go out with the next frame the page draws, and a
        // paused clock draws none: a tiny shot draws one.
        await p.screenshot({ clip: { x: 0, y: 0, width: 1, height: 1 } });
      }
      await p.evaluate(([cue, name]) => {
        const seen = new WeakSet(), last = new Map(), near = (x, y) => Math.abs(x - y) < 40;
        // Drive an animation from c: a fresh one, a step behind real time,
        // starts from its start; one played again resumes where it stood.
        const take = a => {
          // reverse() leaves its rate pending; setting the time settles it.
          a.pause();
          if (a.currentTime !== null) a.currentTime = a.currentTime;
          const end = a.effect ? a.effect.getComputedTiming().endTime : 0;
          let c = a.currentTime ?? 0;
          if (!seen.has(a)) c = a.playbackRate < 0 ? end : 0;
          else if (last.has(a) && near(c, last.get(a))) c = last.get(a);
          else if (a.playbackRate >= 0 && near(c, 0)) c = 0;
          else if (a.playbackRate < 0 && near(c, end)) c = end;
          seen.add(a);
          a.currentTime = c;
          last.set(a, c);
        };
        window.__maestroStep = d => {
          for (const a of document.getAnimations()) {
            if (a.playState === "running") { take(a); continue; }
            if (a.playState !== "paused" || !last.has(a)) { last.delete(a); continue; }
            const end = a.effect ? a.effect.getComputedTiming().endTime : 0;
            const c = a.currentTime + d * a.playbackRate;
            if (a.playbackRate > 0 ? c >= end : a.playbackRate < 0 && c <= 0) {
              last.delete(a);
              a.finish();
            } else {
              a.currentTime = c;
              last.set(a, c);
            }
          }
        };
        if (cue) {
          if (typeof window.motion !== "function") throw new Error(name + ": the page has no window.motion for #" + cue);
          for (const a of document.getAnimations()) {
            if (a.playState !== "running") continue;
            try { a.finish(); } catch { take(a); }
          }
          window.motion(cue);
        }
        window.__maestroStep(0);
      }, [j.cue, j.name]);
      for (let i = 0; i < j.frames; i++) {
        if (i) {
          await p.clock.runFor(j.step);
          await p.evaluate(d => window.__maestroStep(d), j.step);
        }
        await p.screenshot({ path: j.dir + "/" + String(i).padStart(3, "0") + ".png" });
      }
    } finally {
      await ctx.close();
    }
  }
  return "clips " + jobs.length;
}
EOF
run_jobs() { # <jobs.json> <tail>
	{ printf 'async page => {\n  const jobs = '; cat "$1"; cat "$2"; } >"$tmp/run.js"
	(cd "$out" && playwright-cli -s="$run" run-code --filename="$tmp/run.js") >"$tmp/log" 2>&1 || {
		cat "$tmp/log" >&2
		die 1 "the browser failed"
	}
	! grep -q '^### Error' "$tmp/log" || { cat "$tmp/log" >&2; die 1 "the browser failed"; }
}
shoot_jobs() { run_jobs "$1" "$tmp/shoot.tail"; }

(cd "$out" && playwright-cli -s="$run" open --browser chromium --idle-timeout 900000) >"$tmp/log" 2>&1 || {
	cat "$tmp/log" >&2
	echo "run=$run"
	echo "stop=no browser"
	exit 4
}

echo "run=$run"
url_of() { # <index>
	local u=${urls[$1]}
	[[ $u != @* ]] || u=$base${u#@}
	printf '%s' "$u"
}

if [ -n "$motion" ]; then
	step=40
	# Clips an earlier run made of these pages (a cue since renamed, a
	# width since dropped) would reach the owner as current: this run's
	# replace them. Other pages' clips stay. A name is <page>[+<cue>]-<w>.
	for f in "$out"/*-motion.png "$out"/*-strip.png; do
		[ -e "$f" ] || continue
		s=${f##*/}
		s=${s%-*}
		s=${s%-*}
		s=${s%%+*}
		for pn in "${pnames[@]}"; do [ "$s" != "$pn" ] || rm -f "$f"; done
	done
	# Frames: one folder per clip, under /f/; then one call shoots them all.
	for i in "${!names[@]}"; do
		for w in "${ws[@]}"; do
			c=${names[$i]}-$w
			mkdir "$tmp/f/$c"
			jq -n --arg url "$(url_of "$i")" --arg name "${names[$i]}" --arg cue "${cues[$i]}" --arg dir "$tmp/f/$c" \
				--argjson width "$w" --argjson step "$step" --argjson frames $((ms / step + 1)) \
				'{url: $url, name: $name, cue: $cue, dir: $dir, width: $width, step: $step, frames: $frames}'
		done
	done | jq -s . >"$tmp/clips.json"
	run_jobs "$tmp/clips.json" "$tmp/motion.tail"

	# Each clip: frames that repeat the one before merge into it; the
	# first holds 600ms longer and the last 1200ms, so a viewer sees the
	# start and the end. An APNG stores each frame's PNG data as is, so
	# no image library is needed. Writes the clip, and the page its strip
	# is shot from.
	cat >"$tmp/clip.js" <<'EOF'
const fs = require("fs");
const [fdir, out, base, step] = [process.argv[2], process.argv[3], process.argv[4], +process.argv[5]];
const table = [];
for (let n = 0; n < 256; n++) { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; table[n] = c >>> 0; }
const crc = b => { let c = ~0; for (const x of b) c = table[(c ^ x) & 255] ^ (c >>> 8); return ~c >>> 0; };
const u32 = n => { const b = Buffer.alloc(4); b.writeUInt32BE(n); return b; };
const chunk = (type, data) => {
  const td = Buffer.concat([Buffer.from(type, "latin1"), data]);
  return Buffer.concat([u32(data.length), td, u32(crc(td))]);
};
const read = f => {
  const b = fs.readFileSync(f), pre = [], idat = [];
  if (b.toString("latin1", 1, 4) !== "PNG") throw new Error(f + " is not a PNG");
  let ihdr;
  for (let i = 8; i < b.length;) {
    const n = b.readUInt32BE(i), type = b.toString("latin1", i + 4, i + 8), d = b.subarray(i + 8, i + 8 + n);
    if (type === "IHDR") ihdr = d;
    else if (type === "IDAT") idat.push(d);
    else if (!idat.length && type !== "IEND") pre.push(chunk(type, d));
    i += 12 + n;
  }
  return { ihdr, pre, data: Buffer.concat(idat) };
};
for (const c of fs.readdirSync(fdir).filter(c => fs.statSync(fdir + "/" + c).isDirectory()).sort()) {
  const files = fs.readdirSync(fdir + "/" + c).filter(f => f.endsWith(".png")).sort();
  if (!files.length) throw new Error(c + ": no frames");
  const frames = files.map(f => read(fdir + "/" + c + "/" + f));
  const groups = [];
  frames.forEach((f, i) => {
    if (!f.ihdr.equals(frames[0].ihdr)) throw new Error(c + ": frame " + i + " is another size");
    const g = groups[groups.length - 1];
    if (g && g.data.equals(f.data)) g.ms += step;
    else groups.push({ data: f.data, ms: step, at: i });
  });
  if (groups.length < 2) { console.log("still=" + c); continue; }
  if (groups[groups.length - 1].ms === step) console.log("cut=" + c);
  groups[0].ms += 600;
  groups[groups.length - 1].ms += 1200;
  const w = frames[0].ihdr.readUInt32BE(0), h = frames[0].ihdr.readUInt32BE(4);
  const parts = [Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", frames[0].ihdr),
    chunk("acTL", Buffer.concat([u32(groups.length), u32(0)])), ...frames[0].pre];
  let seq = 0;
  groups.forEach((g, i) => {
    const fc = Buffer.alloc(26);
    fc.writeUInt32BE(seq++, 0); fc.writeUInt32BE(w, 4); fc.writeUInt32BE(h, 8);
    fc.writeUInt16BE(Math.min(g.ms, 65535), 20); fc.writeUInt16BE(1000, 22);
    parts.push(chunk("fcTL", fc));
    parts.push(i ? chunk("fdAT", Buffer.concat([u32(seq++), g.data])) : chunk("IDAT", g.data));
  });
  parts.push(chunk("IEND", Buffer.alloc(0)));
  fs.writeFileSync(out + "/" + c + "-motion.png", Buffer.concat(parts));
  // The strip: up to eight frames, evenly spaced from 0ms to the last change.
  const last = groups[groups.length - 1].at, picks = [];
  for (let k = 0; k < 8; k++) { const i = Math.round(k * last / 7); if (!picks.includes(i)) picks.push(i); }
  const cols = w >= 800 ? 2 : 4;
  fs.writeFileSync(fdir + "/" + c + ".html", '<!doctype html><meta charset="utf-8"><style>body{margin:0;padding:16px;background:#fff;font:600 20px sans-serif;color:#111}' +
    ".g{display:grid;grid-template-columns:repeat(" + cols + ",1fr);gap:16px;align-items:start}" +
    "figure{margin:0}figcaption{padding:0 0 8px}img{max-width:100%;border:1px solid #ccc}</style><div class=\"g\">" +
    picks.map(i => "<figure><figcaption>" + c + " " + i * step + "ms</figcaption><img src=\"" + base + "/f/" + c + "/" + files[i] + "\"></figure>").join("") +
    "</div>\n");
  console.log("clip=" + c);
}
EOF
	node "$tmp/clip.js" "$tmp/f" "$out" "$base" "$step" >"$tmp/clips" 2>"$tmp/err" || {
		cat "$tmp/err" >&2
		die 1 "can't make the clips"
	}
	# Motion that shows at one width only leaves the other widths still:
	# say so and go on. Nothing moving anywhere is a wrong cue or page.
	grep -q '^clip=' "$tmp/clips" ||
		die 1 "nothing moved in $(sed -n 's/^still=//p' "$tmp/clips" | paste -sd, -): check its cue, or give more --ms"
	sed -n 's/^clip=//p' "$tmp/clips" | while IFS= read -r c; do
		echo "$base/f/$c.html|$out/$c-strip.png|1600|light"
	done | mkjobs "$tmp/strips.json"
	shoot_jobs "$tmp/strips.json"
	while IFS= read -r c; do
		[ -s "$out/$c-motion.png" ] || die 1 "no clip written: $out/$c-motion.png"
		[ -s "$out/$c-strip.png" ] || die 1 "no strip written: $out/$c-strip.png"
		echo "clip=$out/$c-motion.png"
		echo "strip=$out/$c-strip.png"
	done < <(sed -n 's/^clip=//p' "$tmp/clips")
	grep -E '^(still|cut)=' "$tmp/clips" || true
	echo "ok=1"
	exit 0
fi

for i in "${!names[@]}"; do
	u=$(url_of "$i")
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
			printf 'figure{margin:0}figcaption{padding:0 0 8px}img{max-width:100%%;border:1px solid #ccc}</style><div class="g">'
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
