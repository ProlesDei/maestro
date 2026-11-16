//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
)

const streamMarker = "STREAM-MARKER-41c7"

// streamClaude stands in for claude in both formats: with stream-json
// it prints $FAKE_OUT/head first, as Claude Code's system, assistant and
// user lines would come; then, by $FAKE_OUT/mode, it ends as a run that
// gave its result, one killed in the middle of a line ($FAKE_OUT/cut, in
// stream-json only), one that exits 1 with no result, one that prints a
// crash of its own after the stream began (crash), or one that prints
// only an error with no newline (bare); one that hangs marks its head
// printed ($FAKE_OUT/<run>/headed).
const streamClaude = `#!/bin/sh
d=$FAKE_OUT/$MAESTRO_RUN_ID
mkdir -p "$d"
printf '%s\n' "$@" >"$d/args"
cat >"$d/stdin"
env >"$d/env"
sid= prev= stream=
for a in "$@"; do
	[ "$prev" = --session-id ] && sid=$a
	[ "$prev" = --resume ] && sid=$a
	[ "$a" = stream-json ] && stream=1
	prev=$a
done
mode=$(cat "$FAKE_OUT/mode" 2>/dev/null)
[ -n "$stream" ] && [ "$mode" != bare ] && cat "$FAKE_OUT/head"
case $mode in
crash)
	printf 'TypeError: boom\n\n    at x.js:1'
	exit 3 ;;
bare)
	printf 'Error: Credit balance is too low'
	exit 1 ;;
killed)
	[ -n "$stream" ] && cat "$FAKE_OUT/cut"
	kill -9 $$ ;;
noresult)
	exit 1 ;;
hang)
	: >"$d/headed"
	sleep 300 ;;
esac
extra=
[ -n "$stream" ] && extra='"model_usage_since_last_result":{},'
printf '{"duration_api_ms":5,"stop_reason":"end_turn","session_id":"%s","total_cost_usd":0.01,"usage":{},"modelUsage":{"claude-sonnet-5-5":{"inputTokens":3,"outputTokens":4,"cacheReadInputTokens":5,"cacheCreationInputTokens":6,"costUSD":0.01}},%s"result":"job: done: %s","subtype":"success","is_error":false,"num_turns":2,"duration_ms":9,"type":"result"}\n' "$sid" "$extra" "$MAESTRO_RUN_ID"
`

// streamHead is a stream's lines before its result, with a marker in
// every field that holds text: hooks, the init line's cwd, MCP servers,
// slash commands, skills and plugins, tool input and output, a team's
// own skill and file names, and the model's text.
func streamHead() string {
	m := streamMarker
	lines := []string{
		`{"type":"system","subtype":"hook_started","hook_id":"h","hook_name":"SessionStart:` + m + `","hook_event":"SessionStart","uuid":"u","session_id":"s"}`,
		`{"type":"system","subtype":"hook_response","hook_id":"h","hook_name":"` + m + `","output":"` + m + `","stdout":"` + m + `","stderr":"` + m + `","exit_code":0,"outcome":"success","uuid":"u","session_id":"s"}`,
		`{"type":"system","subtype":"init","cwd":"/home/` + m + `","session_id":"11111111-2222-4333-8444-555555555555","tools":["Bash"],"mcp_servers":[{"name":"` + m + `","status":"connected"}],"model":"claude-sonnet-5-5","slash_commands":["` + m + `"],"claude_code_version":"2.1.322","skills":["` + m + `"],"plugins":[{"name":"` + m + `","path":"/` + m + `"}],"scratchpad_path":"/tmp/` + m + `"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"},"uuid":"u","session_id":"s"}`,
		streamUse("m1", "2026-11-16T01:00:00.000Z", `{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"cd /w/`+m+` && maestro-ticket pickup","description":"`+m+`"}}`),
		streamResult("t1", "2026-11-16T01:00:00.400Z", false, m),
		streamUse("m2", "2026-11-16T01:00:01.000Z", `{"type":"tool_use","id":"t2","name":"Skill","input":{"skill":"`+m+`"}}`),
		streamResult("t2", "2026-11-16T01:00:01.100Z", true, m),
		streamUse("m3", "2026-11-16T01:00:02.000Z", `{"type":"tool_use","id":"t3","name":"Read","input":{"file_path":"/w/.claude/skills/maestro-`+m+`/SKILL.md"}}`),
		streamResult("t3", "2026-11-16T01:00:02.100Z", false, m),
		streamUse("m4", "2026-11-16T01:00:03.000Z", `{"type":"tool_use","id":"t4","name":"mcp__`+m+`__q","input":{"q":"`+m+`"}}`),
		streamResult("t4", "2026-11-16T01:00:03.300Z", false, m),
		streamUse("m5", "2026-11-16T01:00:04.000Z", `{"type":"tool_use","id":"t5","name":"Read","input":{"file_path":"/w/.claude/skills/maestro-code-review/references/passes.md"}}`),
		streamResult("t5", "2026-11-16T01:00:04.050Z", false, m),
		streamUse("m6", "2026-11-16T01:00:05.000Z", `{"type":"text","text":"`+m+`"}`),
	}
	return strings.Join(lines, "\n") + "\n"
}

func streamUse(id, at, block string) string {
	return `{"type":"assistant","message":{"model":"claude-sonnet-5-5","id":"` + id + `","type":"message","role":"assistant","content":[` + block +
		`],"usage":{"input_tokens":2,"cache_creation_input_tokens":10,"cache_read_input_tokens":1000,"output_tokens":5}},"parent_tool_use_id":null,"session_id":"s","uuid":"u","timestamp":"` + at + `"}`
}

func streamResult(id, at string, isErr bool, text string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []map[string]any{
		{"type": "tool_result", "content": text, "is_error": isErr, "tool_use_id": id}}},
		"parent_tool_use_id": nil, "session_id": "s", "uuid": "u", "timestamp": at, "tool_use_result": text})
	return string(b)
}

// newStreamJobs is a job harness with streamClaude, its head written,
// in mode, with stats on, or off (so json) when off is set.
func newStreamJobs(t *testing.T, mode string, off bool) (*jobHarness, *masker) {
	t.Helper()
	j := newJobHarness(t, "junior-engineer")
	j.h.checks = fixedChecks
	for name, body := range map[string]string{"claude": streamClaude} {
		if err := os.WriteFile(filepath.Join(j.bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"head": streamHead(), "mode": mode,
		"cut": streamResult("t9", "2026-11-16T01:00:09.000Z", false, streamMarker+": no space left on device")[:90]} {
		if err := os.WriteFile(filepath.Join(j.out, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if off {
		j.env = append(j.env, stats.OffEnv+"=off")
	}
	return j, newMasker(j.h.dir, "<DIR>", j.exe, "<exe>", j.bin, "<bin>", j.out, "<out>")
}

func needStream(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("stream capture is Linux only")
	}
}

// streamed reports whether claude got stream-json for run id.
func (j *jobHarness) streamed(id string) bool {
	return slices.Contains(j.seen(id).args, "stream-json")
}

// streamRow is run id's stream facts in stats.db.
func streamRow(t *testing.T, dir, id string) (stream string, calls, fails int, tail, open, claude string) {
	t.Helper()
	statsQuery(t, dir, `SELECT coalesce(s.name, '-'), coalesce(f.tool_calls, -1), coalesce(f.tool_fails, -1), coalesce(f.tail, '-'),
		coalesce(o.name, '-'), coalesce(f.v_claude, '-')
		FROM run_facts f LEFT JOIN codes s ON s.id = f.stream LEFT JOIN codes o ON o.id = f.open_code WHERE f.run = '`+id+`'`,
		&stream, &calls, &fails, &tail, &open, &claude)
	return
}

// pickupScene runs a build of T-1 that ends as mode says, and returns
// what a bot reads of it: pickup as text and JSON, and runs as JSON.
func pickupScene(t *testing.T, mode string, off bool) (got string, j *jobHarness, id string) {
	t.Helper()
	j, m := newStreamJobs(t, mode, off)
	j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
	_, r := j.job("", "build", "T-1")
	j.waitSupervisors()
	var b strings.Builder
	for _, args := range [][]string{{"pickup"}, {"pickup", "--json"}, {"pickup", "--full"}, {"runs", r.ID, "--json"}, {"runs", "--json"}} {
		out, _ := j.pinCmd(m, "", args...)
		b.WriteString(out)
	}
	return b.String(), j, r.ID
}

// A bot reads the same bytes of a run, streamed or not.
func TestStreamPickupBytesMatchJSON(t *testing.T) {
	needStream(t)
	t.Parallel()
	asJSON, jj, jid := pickupScene(t, "ok", true)
	asStream, sj, sid := pickupScene(t, "ok", false)
	if jj.streamed(jid) || !sj.streamed(sid) {
		t.Fatalf("streamed: json %v, stream %v", jj.streamed(jid), sj.streamed(sid))
	}
	if asJSON != asStream {
		t.Fatalf("pickup and runs differ:\n--- json\n%s\n--- stream\n%s", asJSON, asStream)
	}
	if !strings.Contains(asStream, "job: done: R-") {
		t.Fatalf("no reply:\n%s", asStream)
	}
	stream, calls, fails, tail, open, claude := streamRow(t, sj.h.dir, sid)
	if stream != "ok" || calls != 5 || fails != 1 || len(strings.Fields(tail)) != 5 || !strings.Contains(tail, "!") || open != "-" || claude != "2.1.322" {
		t.Fatalf("row: %s %d %d %q %s %s", stream, calls, fails, tail, open, claude)
	}
	var steps int
	statsQuery(t, sj.h.dir, `SELECT count(*) FROM run_steps WHERE run = '`+sid+`'`, &steps)
	var mt, rp string
	statsQuery(t, sj.h.dir, `SELECT group_concat(c.name, ' ') FROM run_steps s JOIN codes c ON c.id = s.code WHERE s.run = '`+sid+`' AND c.name LIKE 'mt:%'`, &mt)
	statsQuery(t, sj.h.dir, `SELECT group_concat(c.name || ':' || s.calls || ':' || s.ms_sum, ' ') FROM run_steps s JOIN codes c ON c.id = s.code WHERE s.run = '`+sid+`' AND c.name LIKE 'R:%'`, &rp)
	if steps != 5 || mt != "mt:pickup" || rp != "R:maestro-code-review/passes:1:50" {
		t.Fatalf("run_steps: %d rows, %q, %q", steps, mt, rp)
	}
	grepStats(t, sj.h.dir, []string{streamMarker})
	if _, err := os.Lstat(filepath.Join(jj.h.dir, stats.DBName)); err == nil {
		t.Fatal("stats off made stats.db")
	}
}

// A command's own text on stdout reads as it would have with json: a
// crash printed after the stream began, blank line and all, and an error
// with no newline.
func TestStreamTextMatchesJSON(t *testing.T) {
	needStream(t)
	t.Parallel()
	for mode, want := range map[string]string{"crash": "TypeError: boom", "bare": "Error: Credit balance is too low"} {
		asJSON, _, _ := pickupScene(t, mode, true)
		asStream, j, id := pickupScene(t, mode, false)
		if !j.streamed(id) {
			t.Fatalf("%s: not streamed", mode)
		}
		if asJSON != asStream {
			t.Fatalf("%s: pickup and runs differ:\n--- json\n%s\n--- stream\n%s", mode, asJSON, asStream)
		}
		if !strings.Contains(asStream, want) {
			t.Fatalf("%s: no %q in\n%s", mode, want, asStream)
		}
	}
}

// A run killed in the middle of a tool's result reads as it would have
// with json: failed, not disk_full, and nothing of the cut line
// anywhere.
func TestStreamKilledMidToolResult(t *testing.T) {
	needStream(t)
	t.Parallel()
	asJSON, _, _ := pickupScene(t, "killed", true)
	asStream, j, id := pickupScene(t, "killed", false)
	if !j.streamed(id) {
		t.Fatal("not streamed")
	}
	if asJSON != asStream {
		t.Fatalf("pickup and runs differ:\n--- json\n%s\n--- stream\n%s", asJSON, asStream)
	}
	if strings.Contains(asStream, "disk_full") || !strings.Contains(asStream, "failed") {
		t.Fatalf("outcome:\n%s", asStream)
	}
	dir := filepath.Join(j.h.dir, "runs")
	for _, f := range []string{id + ".out", id + ".json"} {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		if bytes.Contains(b, []byte(streamMarker)) || bytes.Contains(b, []byte("no space left")) {
			t.Fatalf("%s holds the cut line: %s", f, b)
		}
	}
	if strings.Contains(asStream, streamMarker) {
		t.Fatal("a bot reads the marker")
	}
	stream, _, _, _, _, _ := streamRow(t, j.h.dir, id)
	var outcome string
	statsQuery(t, j.h.dir, `SELECT c.name FROM run_facts f JOIN codes c ON c.id = f.outcome WHERE f.run = '`+id+`'`, &outcome)
	if stream != "no_result" || outcome != "failed" {
		t.Fatalf("row: stream %s, outcome %s", stream, outcome)
	}
	grepStats(t, j.h.dir, []string{streamMarker, "no space left"})
}

// A run whose stream held only lines before its result, such as the init
// line, has no session: it frees its claim, and reads as it would have
// with json.
func TestStreamInitOnlyFreesClaim(t *testing.T) {
	needStream(t)
	t.Parallel()
	scene := func(off bool) (string, *jobHarness) {
		j, m := newStreamJobs(t, "noresult", off)
		os.WriteFile(filepath.Join(j.out, "head"), []byte(strings.SplitAfterN(streamHead(), "\n", 4)[2]), 0o644) // the init line
		j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
		j.h.ok(nil, "claim", "T-1", "--holder", "junior-engineer/s1", "--ttl", "1h")
		_, r := j.job("", "build", "T-1")
		j.waitSupervisors()
		if j.streamed(r.ID) == off {
			t.Fatalf("off %v: streamed %v", off, j.streamed(r.ID))
		}
		var tk struct{ Claim *struct{ Holder string } }
		j.h.ok(&tk, "get", "T-1")
		if tk.Claim != nil {
			t.Fatalf("off %v: the claim stayed: %+v", off, tk.Claim)
		}
		if b, _ := os.ReadFile(filepath.Join(j.h.dir, "runs", r.ID+".out")); len(b) != 0 {
			t.Fatalf(".out holds %q", b)
		}
		var b strings.Builder
		for _, args := range [][]string{{"pickup"}, {"pickup", "--json"}, {"runs", r.ID, "--json"}} {
			out, _ := j.pinCmd(m, "", args...)
			b.WriteString(out)
		}
		return b.String(), j
	}
	asJSON, _ := scene(true)
	asStream, _ := scene(false)
	if asJSON != asStream {
		t.Fatalf("pickup and runs differ:\n--- json\n%s\n--- stream\n%s", asJSON, asStream)
	}
}

// A run its limit ended keeps the call that hung, and how long it had
// gone, though its stream ends with no result.
func TestStreamTimedOutKeepsOpenCall(t *testing.T) {
	needStream(t)
	t.Parallel()
	j, _ := newStreamJobs(t, "hang", false)
	hung := streamUse("m7", time.Now().UTC().Format(time.RFC3339Nano), `{"type":"tool_use","id":"t7","name":"Bash","input":{"command":"go test ./... `+streamMarker+`"}}`)
	if err := os.WriteFile(filepath.Join(j.out, "head"), []byte(streamHead()+hung+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
	out, err := j.cmd("", "job", "--json", "build", "T-1", "--limit", "2s").Output()
	if err != nil {
		t.Fatalf("job: %v %s", err, out)
	}
	var s started
	json.Unmarshal(out, &s)
	if r := j.waitEnded(s.Run.ID, 30*time.Second); r.State != runs.TimedOut {
		t.Fatalf("run %+v", r)
	}
	j.waitSupervisors()
	stream, calls, _, tail, open, _ := streamRow(t, j.h.dir, s.Run.ID)
	var openMs int64
	statsQuery(t, j.h.dir, `SELECT coalesce(open_ms, -1) FROM run_facts WHERE run = '`+s.Run.ID+`'`, &openMs)
	if stream != "no_result" || calls != 6 || open != "Bash:go" || len(strings.Fields(tail)) != 6 || openMs < 1000 || openMs > 60000 {
		t.Fatalf("row: %s, %d calls, tail %q, open %s for %d ms", stream, calls, tail, open, openMs)
	}
	grepStats(t, j.h.dir, []string{streamMarker})
}

// Stats off: claude gets json, and no stream file is ever made.
func TestStreamOffWhenStatsOff(t *testing.T) {
	needStream(t)
	t.Parallel()
	for _, how := range []string{"marker", "env"} {
		j, _ := newStreamJobs(t, "hang", how == "env")
		if how == "marker" {
			j.h.ok(nil, "stats", "off")
		}
		j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
		out, err := j.cmd("", "job", "--json", "build", "T-1", "--limit", "2s").Output()
		if err != nil {
			t.Fatalf("job: %v %s", err, out)
		}
		var s started
		json.Unmarshal(out, &s)
		time.Sleep(500 * time.Millisecond)
		if ents, _ := filepath.Glob(filepath.Join(j.h.dir, "runs", "*.stream")); len(ents) != 0 {
			t.Errorf("stats off by %s: %v", how, ents)
		}
		j.waitEnded(s.Run.ID, 20*time.Second)
		if j.streamed(s.Run.ID) {
			t.Errorf("stats off by %s: claude got stream-json", how)
		}
	}
}

// The stream file goes once the run's end is recorded, when its
// supervisor died, and with a pruned run; a died run's row has what its
// stream showed.
func TestStreamFileRemovedCLI(t *testing.T) {
	needStream(t)
	t.Parallel()
	j, _ := newStreamJobs(t, "hang", false)
	j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
	j.h.ok(nil, "create", "--title", "b", "--tier", "junior")
	// The end.
	os.WriteFile(filepath.Join(j.out, "mode"), []byte("ok"), 0o644)
	_, r := j.job("", "build", "T-1")
	j.waitSupervisors()
	streams := func() []string {
		s, _ := filepath.Glob(filepath.Join(j.h.dir, "runs", "*.stream"))
		return s
	}
	if !j.streamed(r.ID) || len(streams()) != 0 {
		t.Fatalf("after the end: streamed %v, files %v", j.streamed(r.ID), streams())
	}
	// A supervisor killed mid-run.
	os.WriteFile(filepath.Join(j.out, "mode"), []byte("hang"), 0o644)
	out, err := j.cmd("", "job", "--json", "build", "T-2").Output()
	if err != nil {
		t.Fatalf("job: %v %s", err, out)
	}
	var s started
	json.Unmarshal(out, &s)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(j.out, s.Run.ID, "headed")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if fi, err := os.Stat(filepath.Join(j.h.dir, "runs", s.Run.ID+".stream")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stream file while the run goes: %v %v", fi, err)
	}
	if err := exec.Command("pkill", "-KILL", "-f", "supervise "+s.Run.ID).Run(); err != nil {
		t.Fatalf("kill supervisor: %v", err)
	}
	if d := j.waitEnded(s.Run.ID, 10*time.Second); d.State != runs.Died {
		t.Fatalf("run whose supervisor died: %+v", d)
	}
	j.h.ok(nil, "pickup")
	if len(streams()) != 0 {
		t.Fatalf("after the orphan path: %v", streams())
	}
	if stream, calls, _, _, _, _ := streamRow(t, j.h.dir, s.Run.ID); stream != "no_result" || calls != 5 {
		t.Fatalf("died run's row: %s, %d calls", stream, calls)
	}
}

// With stats on, claude gets every kind's argv with only its exact
// --output-format json pair swapped, and every record keeps json, as
// the stats-off golden shows them; a plain launch is never swapped.
func TestStreamArgvSwap(t *testing.T) {
	needStream(t)
	t.Parallel()
	want, err := os.ReadFile(filepath.Join("testdata", "pins", "argv.golden"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(want), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "argv: ") {
			if !strings.Contains(l, `"--output-format" "json"`) {
				t.Fatalf("golden argv with no json pair: %s", l)
			}
			lines[i] = strings.Replace(l, `"--output-format" "json"`, `"--output-format" "stream-json" "--verbose"`, 1)
		}
	}
	if got := argvPins(t); got != strings.Join(lines, "\n") {
		t.Fatalf("stats-on argv:\n%s\nwant the golden with the pair swapped:\n%s", got, strings.Join(lines, "\n"))
	}
	l := newLauncher(t, "planner")
	for _, format := range []string{"json", "stream-json"} {
		args := filepath.Join(l.h.dir, "args-"+format)
		r := l.launch("", "--", "sh", "-c", `printf '%s\n' "$@" >`+args, "sh", "--output-format", format)
		l.waitEnded(r.ID, 20*time.Second)
		b, _ := os.ReadFile(args)
		if string(b) != "--output-format\n"+format+"\n" {
			t.Errorf("plain launch with %s: claude got %q", format, b)
		}
	}
}

// A supervisor killed after Claude printed its result but before it read
// the stream: pickup reads the stream as it would have, so the run's
// cost is logged and its row has the counts, as json's .out would have
// let them be.
func TestOrphanReadsStream(t *testing.T) {
	h, id := orphanScene(t)
	prepareStatsAt(t, h.dir)
	dir := filepath.Join(h.dir, "runs")
	writeSide(dir, id, &sideFacts{Stream: true})
	res := `{"duration_api_ms":5,"session_id":"11111111-2222-4333-8444-555555555555","total_cost_usd":2.5,"modelUsage":{"claude-sonnet-5-5":{"inputTokens":3,"outputTokens":4,"costUSD":2.5}},"result":"job: done","subtype":"success","is_error":false,"num_turns":2,"type":"result"}`
	os.WriteFile(filepath.Join(dir, id+".out"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, id+".stream"), []byte(streamHead()+res+"\n"), 0o600)
	h.run("", "pickup")
	if b, _ := os.ReadFile(filepath.Join(dir, id+".out")); string(b) != res+"\n" {
		t.Fatalf(".out %q", b)
	}
	if _, err := os.Lstat(filepath.Join(dir, id+".stream")); err == nil {
		t.Fatal("stream file left")
	}
	if _, out, _ := h.run("", "costs"); !strings.Contains(out, "2.50") {
		t.Fatalf("the result's cost wasn't logged:\n%s", out)
	}
	if stream, calls, _, _, _, _ := streamRow(t, h.dir, id); stream != "ok" || calls != 5 {
		t.Fatalf("row: %s, %d calls", stream, calls)
	}
	// With its stream file gone, its counts are lost.
	h, id = orphanScene(t)
	prepareStatsAt(t, h.dir)
	writeSide(filepath.Join(h.dir, "runs"), id, &sideFacts{Stream: true})
	h.run("", "pickup")
	if stream, calls, _, _, _, _ := streamRow(t, h.dir, id); stream != "lost" || calls != -1 {
		t.Fatalf("row with no stream file: %s, %d calls", stream, calls)
	}
}

// A run started with stats off in its shell gets no row, though its
// supervisor died and a shell with stats on adopted it.
func TestStatsOffFollowsOrphan(t *testing.T) {
	needStream(t)
	t.Parallel()
	j, _ := newStreamJobs(t, "hang", true)
	prepareStatsAt(t, j.h.dir)
	j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
	out, err := j.cmd("", "job", "--json", "build", "T-1").Output()
	if err != nil {
		t.Fatalf("job: %v %s", err, out)
	}
	var s started
	json.Unmarshal(out, &s)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(j.out, s.Run.ID, "env")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := exec.Command("pkill", "-KILL", "-f", "supervise "+s.Run.ID).Run(); err != nil {
		t.Fatalf("kill supervisor: %v", err)
	}
	if d := j.waitEnded(s.Run.ID, 10*time.Second); d.State != runs.Died {
		t.Fatalf("run whose supervisor died: %+v", d)
	}
	j.h.ok(nil, "pickup")
	if n := statsRowCount(t, j.h.dir); n != 0 {
		t.Fatalf("%d rows of a run started with %s=off", n, stats.OffEnv)
	}
}
