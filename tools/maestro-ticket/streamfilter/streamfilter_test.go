package streamfilter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const marker = "MARKER-7f3a9c"

// The lines below follow what Claude Code 2.1.322 printed in R0, field
// for field where a field matters, with a marker in every field that
// holds text.
func hookStarted() string {
	return `{"type":"system","subtype":"hook_started","hook_id":"h1","hook_name":"SessionStart:` + marker + `","hook_event":"SessionStart","uuid":"u","session_id":"s"}`
}

func hookResponse() string {
	return `{"type":"system","subtype":"hook_response","hook_id":"h1","hook_name":"` + marker + `","hook_event":"SessionStart","output":"` + marker + `","stdout":"` + marker + `","stderr":"` + marker + `","exit_code":0,"outcome":"success","uuid":"u","session_id":"s"}`
}

func initLine(version string) string {
	return `{"type":"system","subtype":"init","cwd":"/home/` + marker + `","session_id":"s","tools":["Bash","` + marker + `"],"mcp_servers":[{"name":"` + marker + `","status":"connected","source":"x"}],"model":"claude-sonnet-5-5","permissionMode":"bypassPermissions","slash_commands":["` + marker + `"],"apiKeySource":"none","claude_code_version":"` + version + `","output_style":"default","agents":["` + marker + `"],"skills":["` + marker + `"],"plugins":[{"name":"` + marker + `","path":"/` + marker + `","source":"x"}],"uuid":"u","scratchpad_path":"/tmp/` + marker + `","original_cwd":"/` + marker + `"}`
}

func rateLimit() string {
	return `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1,"rateLimitType":"five_hour"},"uuid":"u","session_id":"s"}`
}

type use struct {
	id, name string
	input    map[string]string
}

func assistant(msgID, parent, model, at string, in, cr, cw, out int, uses ...use) string {
	var content []map[string]any
	if len(uses) == 0 {
		content = append(content, map[string]any{"type": "text", "text": marker})
	}
	for _, u := range uses {
		in := map[string]any{"description": marker}
		for k, v := range u.input {
			in[k] = v
		}
		content = append(content, map[string]any{"type": "tool_use", "id": u.id, "name": u.name, "input": in, "caller": map[string]any{"type": "direct"}})
	}
	var p any
	if parent != "" {
		p = parent
	}
	b, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{"model": model, "id": msgID, "type": "message", "role": "assistant", "content": content,
			"usage": map[string]any{"input_tokens": in, "cache_creation_input_tokens": cw, "cache_read_input_tokens": cr, "output_tokens": out,
				"service_tier": "standard", "cache_creation": map[string]any{"ephemeral_5m_input_tokens": cw}}},
		"parent_tool_use_id": p, "session_id": "s", "uuid": "u", "timestamp": at, "request_id": "req",
		"wire_tool_inputs": map[string]any{"x": map[string]any{"command": marker}},
	})
	return string(b)
}

func result(at string, id string, isErr bool) string {
	b, _ := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{"role": "user", "content": []map[string]any{
			{"type": "tool_result", "content": marker + " no space left on device", "is_error": isErr, "tool_use_id": id}}},
		"parent_tool_use_id": nil, "session_id": "s", "uuid": "u", "timestamp": at,
		"tool_use_result": marker, "tool_result_meta": []map[string]any{{"id": id}},
	})
	return string(b)
}

// resultLine starts with duration_api_ms, not type, as Claude Code
// prints it.
func resultLine(reply string) string {
	return `{"duration_api_ms":1571,"stop_reason":"end_turn","session_id":"9e826f97-f358-494b-ba61-a9b4f1b68347","total_cost_usd":0.05,"usage":{"input_tokens":2},"modelUsage":{"claude-sonnet-5-5":{"inputTokens":2,"costUSD":0.05}},"permission_denials":[],"model_usage_since_last_result":{},"result":"` + reply + `","subtype":"success","is_error":false,"num_turns":3,"duration_ms":2000,"type":"result","uuid":"u"}`
}

func stream(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func family(id string) string {
	if strings.HasPrefix(id, "claude-sonnet") {
		return "sonnet"
	}
	if strings.HasPrefix(id, "claude-haiku") {
		return "haiku"
	}
	return Other
}

var names = Names{Sub: []string{"look", "pickup", "job"}, Model: family}

func scanString(s string) (Summary, string) {
	sum, kept := Scan(strings.NewReader(s), names)
	return sum, string(kept)
}

func full() string {
	return stream(
		hookStarted(), hookResponse(), initLine("2.1.322"), rateLimit(),
		assistant("m1", "", "claude-sonnet-5-5", "2026-11-16T01:06:44.000Z", 2, 100, 50, 5,
			use{"t1", "Bash", map[string]string{"command": "cd /x && maestro-ticket pickup " + marker}}),
		result("2026-11-16T01:06:44.250Z", "t1", false),
		// One message, two blocks, two lines: its usage counts once.
		assistant("m2", "", "claude-sonnet-5-5", "2026-11-16T01:06:45.000Z", 3, 1000, 10, 7,
			use{"t2", "Read", map[string]string{"file_path": "/r/.claude/skills/maestro-code-review/references/passes.md"}}),
		assistant("m2", "", "claude-sonnet-5-5", "2026-11-16T01:06:45.000Z", 3, 1000, 10, 7,
			use{"t3", "Bash", map[string]string{"command": "false " + marker}}),
		result("2026-11-16T01:06:45.100Z", "t2", false),
		result("2026-11-16T01:06:46.000Z", "t3", true),
		// A subagent's turn: not the main context, but its tokens count.
		assistant("m3", "t9", "claude-haiku-5-5", "2026-11-16T01:06:47.000Z", 50000, 0, 0, 3,
			use{"t4", "Grep", map[string]string{"pattern": marker}}),
		`{"type":"system","subtype":"api_retry","attempt":1,"error":"`+marker+`"}`,
		`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"auto"}}`,
		assistant("m4", "", "claude-sonnet-5-5", "2026-11-16T01:06:48.000Z", 1, 1200, 0, 9),
		resultLine("job: done: "+marker),
	)
}

// .out gets the result line and nothing else: no init, no hook, no tool
// text.
func TestStreamOutHoldsOnlyResult(t *testing.T) {
	_, kept := scanString(full())
	if want := resultLine("job: done: "+marker) + "\n"; kept != want {
		t.Fatalf("kept %q;\nwant %q", kept, want)
	}
	if strings.Contains(kept, `"subtype":"init"`) || strings.Contains(kept, "hook_") {
		t.Fatal("kept a system line")
	}
	// Of two results, the last one.
	_, kept = scanString(stream(resultLine("one"), initLine("2.1.322"), resultLine("two")))
	if kept != resultLine("two")+"\n" {
		t.Fatalf("kept %q", kept)
	}
}

// The command's own text is kept as json would have shown it, before the
// stream and after its first line, blank lines and a last line with no
// newline included; a stream line cut short is not.
func TestStreamPreJSONTextKept(t *testing.T) {
	s, kept := scanString("Error: boom\n\nsecond line\n" + stream(initLine("2.1.322"), "trailing text", `{"type":"assistant","mess`, resultLine("r")))
	if want := "Error: boom\n\nsecond line\ntrailing text\n" + resultLine("r") + "\n"; kept != want || !s.Result {
		t.Fatalf("kept %q", kept)
	}
	for _, c := range []struct{ in, want string }{
		{"Error: Credit balance is too low", "Error: Credit balance is too low"},
		{stream(initLine("2.1.322")) + "TypeError: boom\n\nat x.js:1", "TypeError: boom\n\nat x.js:1"},
		{"no newline", "no newline"},
	} {
		if _, kept := scanString(c.in); kept != c.want {
			t.Errorf("Scan(%q) kept %q; want %q", c.in, kept, c.want)
		}
	}
	// A last line with no newline before a result seen earlier stays on
	// its own line.
	if _, kept := scanString(stream(resultLine("r")) + "late"); kept != "late\n"+resultLine("r")+"\n" {
		t.Errorf("kept %q", kept)
	}
	// Only the last MaxPre bytes, from a whole line.
	long := strings.Repeat("x", 1000) + "\n"
	_, kept = scanString(strings.Repeat(long, 100) + "end\n")
	if len(kept) > MaxPre || !strings.HasSuffix(kept, "end\n") || !strings.HasPrefix(kept, "x") || len(kept)%1001 != 4 {
		t.Fatalf("kept %d bytes", len(kept))
	}
}

func TestStreamCounts(t *testing.T) {
	s, _ := scanString(full())
	if s.Version != "2.1.322" || !s.Result || s.Retries != 1 || s.Compactions != 1 || s.Subagents != 1 {
		t.Fatalf("summary %+v", s)
	}
	if s.ToolCalls != 4 || s.ToolFails != 1 {
		t.Fatalf("calls %d fails %d", s.ToolCalls, s.ToolFails)
	}
	if s.CtxPeak != 1201 { // m4: 1 + 1200; the subagent's 50000 isn't the main context
		t.Fatalf("ctx_peak %d", s.CtxPeak)
	}
	wantSteps := []Step{
		{Code: "Bash", Calls: 1, Fails: 1, MsSum: 1000, MsMax: 1000},
		{Code: "Grep", Calls: 1},
		{Code: "R:maestro-code-review/passes", Calls: 1, MsSum: 100, MsMax: 100},
		{Code: "mt:pickup", Calls: 1, MsSum: 250, MsMax: 250},
	}
	if !reflect.DeepEqual(s.Steps, wantSteps) {
		t.Fatalf("steps %+v", s.Steps)
	}
	wantTail := []Call{{"mt:pickup", false}, {"R:maestro-code-review/passes", false}, {"Bash", true}, {"Grep", false}}
	if !reflect.DeepEqual(s.Tail, wantTail) {
		t.Fatalf("tail %+v", s.Tail)
	}
	// The Grep never got its result, but the run gave its own: no call
	// hung. With no result line, the Grep is the call that hung.
	if s.OpenCode != "" {
		t.Fatalf("a run with a result has open %q", s.OpenCode)
	}
	noResult := strings.TrimSuffix(full(), resultLine("job: done: "+marker)+"\n")
	if s, _ := scanString(noResult); s.OpenCode != "Grep" || !s.OpenAt.Equal(time.Date(2026, 11, 16, 1, 6, 47, 0, time.UTC)) {
		t.Fatalf("open %q at %v", s.OpenCode, s.OpenAt)
	}
	wantUse := []Usage{
		{Model: "haiku", Input: 50000, Output: 3},
		{Model: "sonnet", Input: 2 + 3 + 1, Output: 5 + 7 + 9, CacheRead: 100 + 1000 + 1200, CacheWrite: 50 + 10},
	}
	if !reflect.DeepEqual(s.Usage, wantUse) {
		t.Fatalf("usage %+v", s.Usage)
	}
}

// The tail keeps the last TailLen calls, and a fail mark lands on its
// own call even when later calls came first.
func TestStreamTail(t *testing.T) {
	var lines []string
	for i := range 30 {
		lines = append(lines, assistant(fmt.Sprint("m", i), "", "claude-sonnet-5-5", "", 1, 0, 0, 1,
			use{fmt.Sprint("t", i), "Edit", nil}))
	}
	lines = append(lines, result("", "t29", true), result("", "t10", true), result("", "t1", true))
	s, _ := scanString(stream(lines...))
	if len(s.Tail) != TailLen {
		t.Fatalf("tail %d", len(s.Tail))
	}
	for i, c := range s.Tail {
		call := 30 - TailLen + i
		if c.Fail != (call == 29 || call == 10) {
			t.Fatalf("tail[%d] (call %d) fail %v", i, call, c.Fail)
		}
	}
	if s.ToolFails != 3 || s.OpenCode != "Edit" {
		t.Fatalf("summary %+v", s)
	}
}

// A line past MaxResult is skipped, and the scan goes on to the result.
// One of a few MB, such as a big file's tool result, still counts: its
// call finished, and failed.
func TestStreamLongLine(t *testing.T) {
	big := assistant("m1", "", "claude-sonnet-5-5", "", 1, 0, 0, 1, use{"t1", "Write", map[string]string{"content": strings.Repeat("y", 9<<20)}})
	mid := assistant("m2", "", "claude-sonnet-5-5", "", 1, 0, 0, 1, use{"t2", "Read", map[string]string{"content": strings.Repeat("y", 2<<20)}})
	read := result("", "t2", true)
	read = strings.Replace(read, `"content":"`, `"content":"`+strings.Repeat("z", 2<<20), 1)
	s, kept := scanString(stream(initLine("2.1.322"), big, mid, read))
	if s.Result || kept != "" || s.ToolCalls != 1 || s.ToolFails != 1 || s.OpenCode != "" {
		t.Fatalf("summary %+v, kept %d bytes", s, len(kept))
	}
	if s, kept := scanString(stream(initLine("2.1.322"), big, resultLine("ok"))); !s.Result || kept != resultLine("ok")+"\n" || s.ToolCalls != 0 {
		t.Fatalf("summary %+v, kept %d bytes", s, len(kept))
	}
}

// A result line past MaxResult is no result: nothing of it is kept.
func TestStreamHugeResult(t *testing.T) {
	s, kept := scanString(stream(initLine("2.1.322"), resultLine(strings.Repeat("z", 9<<20))))
	if s.Result || kept != "" {
		t.Fatalf("summary %+v, kept %d bytes", s, len(kept))
	}
	// Under the cap, a long result is kept whole.
	r := resultLine(strings.Repeat("z", 7<<20))
	if s, kept := scanString(stream(r)); !s.Result || kept != r+"\n" {
		t.Fatalf("7 MB result: %+v", s)
	}
}

// A line the command was killed in the middle of is dropped.
func TestStreamCutAtEnd(t *testing.T) {
	cut := result("2026-11-16T01:06:44.250Z", "t1", false)
	s, kept := scanString(stream(initLine("2.1.322"), assistant("m1", "", "claude-sonnet-5-5", "2026-11-16T01:06:44.000Z", 1, 0, 0, 1,
		use{"t1", "Bash", map[string]string{"command": "go test ./..."}})) + cut[:len(cut)/2])
	if s.Result || kept != "" || s.OpenCode != "Bash:go" {
		t.Fatalf("summary %+v, kept %q", s, kept)
	}
}

func TestStepCodes(t *testing.T) {
	bash := map[string]string{
		"maestro-ticket pickup":                                    "mt:pickup",
		"cd /w/x && maestro-ticket job build T-1":                  "mt:job",
		`cd "/w/a b" && cd sub && /home/u/bin/maestro-ticket look`: "mt:look",
		"maestro-ticket " + marker:                                 "Bash",
		"MAESTRO_TICKET_ACTOR=x maestro-ticket look --json":        "mt:look",
		"git push -u origin HEAD":                                  "git:push",
		"git -C /w/x commit -m " + marker:                          "git:commit",
		"git " + marker:                                            "Bash",
		"git status":                                               "Bash",
		"gh pr create --title " + marker:                           "gh:pr",
		"go test -race ./...":                                      "Bash:go",
		"npm run build":                                            "Bash:npm",
		"bash .claude/skills/maestro-code-review/scripts/review-prep.sh --route T-1": "X:maestro-code-review/review-prep",
		"/r/.claude/skills/maestro-qa-lead/scripts/screens.sh":                       "X:maestro-qa-lead/screens",
		"bash .claude/skills/maestro-code-review/scripts/" + "mine.sh":               "Bash",
		"bash .claude/skills/maestro-" + "teamskill/scripts/x.sh":                    "Bash",
		marker + " --flag": "Bash",
		"":                 "Bash",
	}
	for cmd, want := range bash {
		if got := names.code(block{Name: "Bash", Input: input{Command: cmd}}); got != want {
			t.Errorf("Bash %q: %q; want %q", cmd, got, want)
		}
	}
	read := map[string]string{
		"/r/.claude/skills/maestro-planner/SKILL.md":             "R:maestro-planner",
		"/r/.claude/skills/maestro-planner/references/judge.md":  "R:maestro-planner/judge",
		"/r/.claude/skills/maestro-planner/references/" + marker: "Read",
		"/r/.claude/skills/maestro-planner/references/mine.md":   "Read",
		"/r/.claude/skills/maestro-" + "teamskill/SKILL.md":      "Read",
		"/r/src/" + marker + ".go":                               "Read",
	}
	for p, want := range read {
		if got := names.code(block{Name: "Read", Input: input{FilePath: p}}); got != want {
			t.Errorf("Read %q: %q; want %q", p, got, want)
		}
	}
	for name, want := range map[string]string{
		"Edit": "Edit", "Task": "Task", "Agent": "Agent", "WebFetch": "WebFetch",
		"mcp__" + marker + "__x": "mcp", marker: Other,
	} {
		if got := names.code(block{Name: name}); got != want {
			t.Errorf("tool %q: %q; want %q", name, got, want)
		}
	}
	for skill, want := range map[string]string{"maestro-code-review": "S:maestro-code-review", marker: "S:other", "pdf": "S:other"} {
		if got := names.code(block{Name: "Skill", Input: input{Skill: skill}}); got != want {
			t.Errorf("Skill %q: %q; want %q", skill, got, want)
		}
	}
	// Every code a call can get is on the list.
	codes := Codes(names)
	for _, want := range bash {
		if !slices.Contains(codes, want) {
			t.Errorf("%q is off Codes", want)
		}
	}
	if slices.Contains(codes, Other) {
		t.Error("Codes holds other")
	}
}

// The hand-kept list names every shipped skill, reference and script,
// and nothing else.
func TestSkillListMatchesRepo(t *testing.T) {
	root := filepath.Join("..", "..", "..", "skills")
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Skip("no skills/ beside tools/:", err)
	}
	var repo []string
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), "maestro-") {
			repo = append(repo, e.Name())
		}
	}
	if !slices.Equal(repo, Skills()) {
		t.Fatalf("skills/ has %q;\nthe list has %q", repo, Skills())
	}
	stems := func(dir, ext string) []string {
		var out []string
		fs, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
		for _, f := range fs {
			out = append(out, strings.TrimSuffix(filepath.Base(f), ext))
		}
		return out
	}
	for _, s := range repo {
		refs, scripts := skillStems(s)
		slices.Sort(refs)
		slices.Sort(scripts)
		if got := stems(filepath.Join(root, s, "references"), ".md"); !slices.Equal(got, refs) {
			t.Errorf("%s references: repo %q, list %q", s, got, refs)
		}
		if got := stems(filepath.Join(root, s, "scripts"), ".sh"); !slices.Equal(got, scripts) {
			t.Errorf("%s scripts: repo %q, list %q", s, got, scripts)
		}
	}
}

// The decoders read only the fields a code or count needs: no text field
// of any line has a place to land.
func TestStreamDecoderFields(t *testing.T) {
	var tags []string
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(ty reflect.Type) {
		for ty.Kind() == reflect.Pointer || ty.Kind() == reflect.Slice {
			ty = ty.Elem()
		}
		if ty.Kind() != reflect.Struct || seen[ty] {
			return
		}
		seen[ty] = true
		for i := range ty.NumField() {
			f := ty.Field(i)
			tags = append(tags, ty.Name()+"."+f.Tag.Get("json"))
			switch f.Type.Kind() {
			case reflect.String, reflect.Bool, reflect.Int64:
			case reflect.Pointer, reflect.Slice, reflect.Struct:
				walk(f.Type)
			default:
				t.Errorf("%s.%s is %s", ty.Name(), f.Name, f.Type)
			}
		}
	}
	walk(reflect.TypeFor[line]())
	slices.Sort(tags)
	want := []string{
		"block.id", "block.input", "block.is_error", "block.name", "block.tool_use_id", "block.type",
		"input.command", "input.file_path", "input.skill",
		"line.claude_code_version", "line.message", "line.parent_tool_use_id", "line.subtype", "line.timestamp", "line.type",
		"message.content", "message.id", "message.model", "message.usage",
		"usage.cache_creation_input_tokens", "usage.cache_read_input_tokens", "usage.input_tokens", "usage.output_tokens",
	}
	if !slices.Equal(tags, want) {
		t.Fatalf("decoders read %q;\nwant %q", tags, want)
	}
}

// A Summary holds no text: its strings are codes, model families and a
// version, whatever the stream held.
func TestSummaryHoldsNoText(t *testing.T) {
	ty := reflect.TypeFor[Summary]()
	strs := map[string]bool{}
	var walk func(reflect.Type, string)
	walk = func(ty reflect.Type, at string) {
		switch ty.Kind() {
		case reflect.String:
			strs[at] = true
		case reflect.Slice:
			walk(ty.Elem(), at+"[]")
		case reflect.Struct:
			if ty == reflect.TypeFor[time.Time]() {
				return
			}
			for i := range ty.NumField() {
				walk(ty.Field(i).Type, at+"."+ty.Field(i).Name)
			}
		case reflect.Bool, reflect.Int64:
		default:
			t.Errorf("%s is %s", at, ty)
		}
	}
	walk(ty, "Summary")
	want := map[string]bool{"Summary.Steps[].Code": true, "Summary.Tail[].Code": true, "Summary.OpenCode": true,
		"Summary.Usage[].Model": true, "Summary.Version": true}
	if !reflect.DeepEqual(strs, want) {
		t.Fatalf("string fields %v", strs)
	}
	s, _ := scanString(full())
	if dump := fmt.Sprintf("%+v", s); strings.Contains(dump, marker) {
		t.Fatalf("the summary holds the marker: %s", dump)
	}
	codes := append(Codes(names), Other)
	for _, st := range s.Steps {
		if !slices.Contains(codes, st.Code) {
			t.Errorf("step %q off the list", st.Code)
		}
	}
	// A version off its shape is dropped.
	if s, _ := scanString(stream(initLine("2.1.322-" + marker))); s.Version != "" {
		t.Fatalf("version %q", s.Version)
	}
}

// Markers in tool input and output, the init line, hooks, MCP names,
// slash commands and a team's own skill names reach neither the summary
// nor the kept bytes.
func TestStreamMarkers(t *testing.T) {
	in := stream(hookStarted(), hookResponse(), initLine("2.1.322"),
		assistant("m1", "", "claude-"+marker, "", 1, 0, 0, 1,
			use{"t1", "Skill", map[string]string{"skill": marker}},
			use{"t2", "mcp__" + marker + "__q", map[string]string{"q": marker}},
			use{"t3", "Read", map[string]string{"file_path": "/r/.claude/skills/maestro-" + marker + "/SKILL.md"}},
			use{"t4", "Bash", map[string]string{"command": "/x/" + marker + "/maestro-ticket " + marker}},
			use{"t5", marker, nil}),
		result("", "t1", true), result("", "t4", false),
		`{"type":"`+marker+`","message":{"content":[{"type":"tool_use","name":"`+marker+`"}]}}`,
		resultLine("fine"))
	s, kept := scanString(in)
	if dump := fmt.Sprintf("%+v", s); strings.Contains(dump, marker) || strings.Contains(kept, marker) {
		t.Fatalf("a marker got through:\n%s\n%s", dump, kept)
	}
	if s.ToolCalls != 5 || len(s.Usage) != 1 || s.Usage[0].Model != Other {
		t.Fatalf("summary %+v", s)
	}
}

// The package is a leaf: no network, process, database or other package
// of this module, and no file of its own (encoding/json brings os in).
func TestStreamfilterDeps(t *testing.T) {
	direct, err := exec.Command("go", "list", "-f", "{{join .Imports \" \"}}", ".").Output()
	if err != nil {
		t.Fatal("go list:", err)
	}
	if p := strings.Fields(string(direct)); slices.Contains(p, "os") || slices.Contains(p, "io/fs") || slices.Contains(p, "syscall") {
		t.Errorf("streamfilter imports %q", p)
	}
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal("go list:", err)
	}
	for _, p := range strings.Fields(string(out)) {
		switch {
		case p == "net", strings.HasPrefix(p, "net/"), p == "os/exec", p == "crypto/tls", p == "database/sql",
			strings.HasPrefix(p, "modernc.org/"), strings.HasPrefix(p, "github.com/google/uuid"),
			strings.Contains(p, "/maestro-ticket/") && !strings.HasSuffix(p, "/maestro-ticket/streamfilter"):
			t.Errorf("streamfilter depends on %s", p)
		}
	}
}

// Only runs names the stream file: no other package opens it, so nothing
// else can read a transcript.
func TestStreamNeverReads(t *testing.T) {
	var files []string
	for _, dir := range []string{"../cli", "../stats", "../streamfilter", "../sqlitestore", "../ticket", "../render", "../envcheck", "../cmd/maestro-ticket"} {
		fs, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		files = append(files, fs...)
	}
	ext := regexp.MustCompile(`\.stream\b`)
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		seen++
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.STRING {
				if s, err := strconv.Unquote(l.Value); err == nil && ext.MatchString(s) {
					t.Errorf("%s: literal %q", fset.Position(l.Pos()), s)
				}
			}
			return true
		})
	}
	if seen < 20 {
		t.Fatalf("read %d files", seen)
	}
	// And runs names it once.
	b, err := os.ReadFile("../runs/supervise.go")
	if err != nil || bytes.Count(b, []byte(`".stream"`)) != 1 {
		t.Fatalf("runs/supervise.go: want one .stream literal (%v)", err)
	}
}
