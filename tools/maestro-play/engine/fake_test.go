package engine

// Fake maestro-ticket, claude, codex, gh, git and check binaries. The test
// binary is symlinked under each name in a temp dir; TestMain dispatches on
// the name it was started as. git logs the call and runs the real git. All
// keep their state in $FAKE_DIR:
//
//	tickets.json  the store: tickets with claims and comments, idempotency keys
//	fake.json     scripted replies (fakeConfig) and the open PRs
//	calls.jsonl   every call: the binary, its args, its directory, and the
//	              prompt a session got

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

func TestMain(m *testing.M) {
	if d := os.Getenv("FAKE_SIGNAL_DIR"); d != "" {
		os.Exit(signalEngine(d))
	}
	switch filepath.Base(os.Args[0]) {
	case "maestro-ticket":
		os.Exit(fakeTicket(os.Args[1:]))
	case "claude":
		os.Exit(fakeClaude(os.Args[1:]))
	case "codex":
		os.Exit(fakeCodex(os.Args[1:]))
	case "gh":
		os.Exit(fakeGh(os.Args[1:]))
	case "git":
		os.Exit(fakeGit(os.Args[1:]))
	case "fake-check":
		os.Exit(fakeCheck(os.Args[1:]))
	}
	os.Exit(m.Run())
}

type fakeClaim struct {
	Holder  string `json:"holder"`
	Token   int64  `json:"token"`
	Expired bool   `json:"expired"`
}

type fakeComment struct {
	ID     int    `json:"id"`
	Author string `json:"author"`
	Body   string `json:"body"`
}

type fakeTicketT struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Body        string        `json:"body"`
	Type        string        `json:"type"`
	Status      string        `json:"status"`
	Role        string        `json:"role"`
	Owner       string        `json:"owner"`
	Priority    int           `json:"priority"`
	Tries       int           `json:"tries"`
	Version     int64         `json:"version"`
	BlockedFrom string        `json:"blocked_from,omitempty"`
	Claim       *fakeClaim    `json:"claim"`
	Comments    []fakeComment `json:"comments"`
}

type fakeStore struct {
	Tickets   map[string]*fakeTicketT `json:"tickets"`
	NextToken int64                   `json:"next_token"`
	Keys      map[string]string       `json:"keys"` // key -> request hash
	Results   map[string]string       `json:"results"`
}

func fakeDir() string { return os.Getenv("FAKE_DIR") }

func loadJSON(name string, v any) {
	b, err := os.ReadFile(filepath.Join(fakeDir(), name))
	if err == nil {
		json.Unmarshal(b, v)
	}
}

func saveJSON(name string, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	os.WriteFile(filepath.Join(fakeDir(), name), b, 0o644)
}

func logCall(bin string, args []string, extra map[string]any) {
	rec := map[string]any{"bin": bin, "args": args}
	for k, v := range extra {
		rec[k] = v
	}
	b, _ := json.Marshal(rec)
	f, _ := os.OpenFile(filepath.Join(fakeDir(), "calls.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	f.Write(append(b, '\n'))
	f.Close()
}

// fakeFlags splits args into positionals and --flag values.
func fakeFlags(args []string) ([]string, map[string]string) {
	bools := map[string]bool{"--json": true, "--unclaimed": true, "--claimed": true, "--release": true, "-p": true}
	flags := map[string]string{}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			if bools[a] {
				flags[a] = "true"
			} else if i+1 < len(args) {
				flags[a] = args[i+1]
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return pos, flags
}

func fail(code string, exit int) int {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": code}})
	fmt.Println(string(b))
	return exit
}

func fakeTicket(args []string) int {
	logCall("ticket", args, nil)
	var s fakeStore
	loadJSON("tickets.json", &s)
	if args[0] == "keepalive" {
		// The real one heartbeats until --pid exits. This one runs in the
		// background too, so it only reads the store: a save would race
		// the engine's next call.
		pos, fl := fakeFlags(args[1:])
		t := s.Tickets[pos[0]]
		if t == nil || t.Claim == nil || t.Claim.Holder != fl["--holder"] || fmt.Sprint(t.Claim.Token) != fl["--token"] || fl["--pid"] == "" {
			return fail("lease_lost", 4)
		}
		return 0
	}
	// fake.json may fail a subcommand ("ticket transition"); a reply with
	// no exit lets the call through.
	if cfg := loadFake(); cfg.Replies != nil {
		r := cfg.pop("ticket " + args[0])
		saveJSON("fake.json", cfg)
		if n := toInt(r["exit"]); n != 0 {
			return fail(fmt.Sprint(r["code"]), n)
		}
	}
	if s.Keys == nil {
		s.Keys, s.Results = map[string]string{}, map[string]string{}
	}
	defer saveJSON("tickets.json", &s)
	pos, fl := fakeFlags(args[1:])
	out := func(v any) int {
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
		return 0
	}
	sub := args[0]
	if _, replay := s.Keys[fl["--idempotency-key"]]; sub == "create" && !replay {
		pos = []string{fmt.Sprintf("T-%d", len(s.Tickets)+100)}
		status := fl["--status"]
		if status == "" {
			status = "todo"
		}
		s.Tickets[pos[0]] = &fakeTicketT{ID: pos[0], Title: fl["--title"], Body: fl["--body"], Type: fl["--type"],
			Role: fl["--role"], Owner: fl["--owner"], Status: status, Version: 1}
	}
	if sub == "create" && len(pos) == 0 {
		fmt.Println(s.Results[fl["--idempotency-key"]])
		return 0
	}
	if sub == "list" {
		// Like the store's list: by priority, then id.
		list := []*fakeTicketT{}
		for _, t := range s.Tickets {
			latest := ""
			if n := len(t.Comments); n > 0 {
				latest = t.Comments[n-1].Body
			}
			switch {
			case fl["--status"] != "" && !slices.Contains(strings.Split(fl["--status"], ","), t.Status):
			case fl["--unclaimed"] == "true" && t.Claim != nil && !t.Claim.Expired:
			case !strings.HasPrefix(latest, fl["--latest-prefix"]):
			default:
				list = append(list, t)
			}
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Priority != list[j].Priority {
				return list[i].Priority < list[j].Priority
			}
			return list[i].ID < list[j].ID
		})
		return out(map[string]any{"tickets": list})
	}
	if len(pos) == 0 {
		return fail("usage", 2)
	}
	// Like the store's ParseID: "1", "t-1" and "T-1" are one ticket.
	if n := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(pos[0])), "T-"); s.Tickets["T-"+n] != nil {
		pos[0] = "T-" + n
	}
	t := s.Tickets[pos[0]]
	if t == nil && sub != "create" {
		return fail("not_found", 3)
	}
	key := fl["--idempotency-key"]
	if key != "" {
		sum := sha256.Sum256([]byte(sub + "\x00" + strings.Join(args, "\x00")))
		h := hex.EncodeToString(sum[:])
		if old, ok := s.Keys[key]; ok {
			if old != h {
				return fail("idempotency_conflict", 4)
			}
			fmt.Println(s.Results[key])
			return 0
		}
		defer func() {
			if s.Results[key] != "" {
				s.Keys[key] = h
			}
		}()
	}
	record := func(v any) int {
		b, _ := json.Marshal(v)
		if key != "" {
			s.Results[key] = string(b)
		}
		fmt.Println(string(b))
		return 0
	}
	leaseOK := func() int {
		if t.Claim == nil || t.Claim.Expired {
			if fl["--token"] != "" {
				return fail("lease_lost", 4)
			}
			return 0
		}
		if fl["--token"] == "" {
			return fail("invalid", 2)
		}
		if fl["--holder"] != t.Claim.Holder {
			return fail("claimed", 4)
		}
		if fl["--token"] != fmt.Sprint(t.Claim.Token) {
			return fail("lease_lost", 4)
		}
		return 0
	}
	switch sub {
	case "create":
		return record(t)
	case "get":
		return out(t)
	case "claim":
		if t.Claim != nil && !t.Claim.Expired && t.Claim.Holder != fl["--holder"] {
			return fail("claimed", 4)
		}
		s.NextToken++
		t.Claim = &fakeClaim{Holder: fl["--holder"], Token: s.NextToken}
		t.Version++
		return record(t)
	case "heartbeat":
		if t.Claim == nil || t.Claim.Expired || t.Claim.Holder != fl["--holder"] || fmt.Sprint(t.Claim.Token) != fl["--token"] {
			return fail("lease_lost", 4)
		}
		return out(t)
	case "release":
		if t.Claim != nil && t.Claim.Holder == fl["--holder"] && fmt.Sprint(t.Claim.Token) == fl["--token"] {
			t.Claim = nil
			t.Version++
		}
		return record(t)
	case "comment":
		if c := leaseOK(); c != 0 {
			return c
		}
		cm := fakeComment{ID: len(t.Comments) + 1, Author: fl["--actor"], Body: fl["--body"]}
		t.Comments = append(t.Comments, cm)
		return record(cm)
	case "transition":
		if c := leaseOK(); c != 0 {
			return c
		}
		if f := fl["--from"]; f != "" && f != t.Status {
			return fail("status_conflict", 4)
		}
		if r := fl["--role"]; r != "" && !contains(playbook.Roles, r) {
			return fail("invalid", 2) // the store's: unknown role
		}
		to := fl["--to"]
		if to == "blocked" {
			if t.Status == "blocked" {
				return fail("invalid_transition", 2)
			}
			t.BlockedFrom = t.Status
		}
		if to == "in_progress" && t.Status == "in_review" {
			t.Tries++
		}
		t.Status = to
		if r := fl["--role"]; r != "" {
			t.Role = r
		}
		// Moving to done or cancelled clears the lease, like --release.
		if fl["--release"] == "true" || to == "done" || to == "cancelled" {
			t.Claim = nil
		}
		t.Version++
		return record(t)
	case "update":
		if c := leaseOK(); c != 0 {
			return c
		}
		t.Version++
		return record(t)
	}
	return fail("usage", 2)
}

// fakeConfig scripts the fakes. Harness replies feed claude and codex in
// order; Replies are by command ("gh pr view", "git rev-parse", "check",
// "check unit"): the first is used and dropped, the last one sticks.
type fakeConfig struct {
	Harness []map[string]any            `json:"harness"`
	Replies map[string][]map[string]any `json:"replies"`
	PRs     []fakePR                    `json:"prs"`
}

type fakePR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Head   string `json:"head"`
	Base   string `json:"base"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	Fork   bool   `json:"fork"`
}

func loadFake() *fakeConfig {
	var cfg fakeConfig
	loadJSON("fake.json", &cfg)
	return &cfg
}

func (cfg *fakeConfig) pop(name string) map[string]any {
	list := cfg.Replies[name]
	if len(list) == 0 {
		return nil
	}
	r := list[0]
	if len(list) > 1 {
		cfg.Replies[name] = list[1:]
	}
	return r
}

func (cfg *fakeConfig) popHarness() map[string]any {
	if len(cfg.Harness) == 0 {
		return map[string]any{"reply": map[string]any{"status": "failed", "summary": "no scripted reply"}}
	}
	h := cfg.Harness[0]
	if len(cfg.Harness) > 1 {
		cfg.Harness = cfg.Harness[1:]
	}
	return h
}

func cwd() string { d, _ := os.Getwd(); return d }

// noPrompt is the call's GIT_TERMINAL_PROMPT, GH_PROMPT_DISABLED and
// GIT_SSH_COMMAND, joined by |.
func noPrompt() string {
	return os.Getenv("GIT_TERMINAL_PROMPT") + "|" + os.Getenv("GH_PROMPT_DISABLED") + "|" + os.Getenv("GIT_SSH_COMMAND")
}

// tmpDir is the call's TMPDIR, or "missing" when it isn't a folder.
func tmpDir() string {
	if fi, err := os.Stat(os.Getenv("TMPDIR")); err != nil || !fi.IsDir() {
		return "missing"
	}
	return os.Getenv("TMPDIR")
}

// signalEngine is an engine whose command, a shell, writes its pid to
// dir/ready, then waits; an interrupt makes it write dir/got and exit.
// TestForwardSignals interrupts the engine. It returns only if the
// interrupt didn't stop it.
func signalEngine(dir string) int {
	e := &Engine{cfg: Config{Environ: os.Environ()}}
	script := `trap 'echo INT > got; exit 0' INT; echo $$ > ready; while :; do sleep 0.1; done`
	e.exec(dir, "sh", nil, "", -1, "-c", script)
	return 0
}

// hang runs a scripted reply's sleep: it starts a child that sleeps a
// minute, writes its pid to $FAKE_DIR/child.pid, then sleeps itself for
// the duration given. A timeout must kill both.
func hang(r map[string]any) {
	d, err := time.ParseDuration(fmt.Sprint(r["sleep"]))
	if err != nil {
		return
	}
	child := exec.Command("sleep", "60")
	if child.Start() == nil {
		os.WriteFile(filepath.Join(fakeDir(), "child.pid"), []byte(fmt.Sprint(child.Process.Pid)), 0o644)
	}
	time.Sleep(d)
}

// sideEffects runs a harness reply's test hooks: sleep hangs, steal takes
// the lease, stop_file makes the stop file, commit makes a commit in the
// session's directory and puts its sha in the reply.
func sideEffects(h map[string]any, rep map[string]any) {
	if h["sleep"] != nil {
		hang(h)
	}
	if h["steal"] == true {
		var s fakeStore
		loadJSON("tickets.json", &s)
		for _, t := range s.Tickets {
			if t.Claim != nil {
				s.NextToken++
				t.Claim = &fakeClaim{Holder: "thief/1", Token: s.NextToken}
			}
		}
		saveJSON("tickets.json", &s)
	}
	if h["stop_file"] == true {
		os.WriteFile(filepath.Join(os.Getenv("FAKE_COMMON"), "STOP"), nil, 0o644)
	}
	if msg, ok := h["commit"].(string); ok && rep != nil {
		os.WriteFile(filepath.Join(cwd(), "change.txt"), []byte(msg+"\n"), 0o644)
		git := os.Getenv("FAKE_GIT")
		exec.Command(git, "add", "change.txt").Run()
		exec.Command(git, "commit", "-q", "-m", msg).Run()
		out, _ := exec.Command(git, "rev-parse", "HEAD").Output()
		rep["commit"] = strings.TrimSpace(string(out))
	}
}

// fakeClaude answers claude -p --output-format json the way claude does:
// one JSON result with the structured reply in structured_output.
func fakeClaude(args []string) int {
	prompt, _ := io.ReadAll(os.Stdin)
	logCall("claude", args, map[string]any{"prompt": string(prompt), "cwd": cwd(), "run_env": os.Getenv(RunEnv), "tmp": tmpDir()})
	cfg := loadFake()
	h := cfg.popHarness()
	saveJSON("fake.json", cfg)
	_, fl := fakeFlags(args)
	id := fl["--session-id"] + fl["--resume"]
	rep, _ := h["reply"].(map[string]any)
	sideEffects(h, rep)
	out := map[string]any{"type": "result", "subtype": "success", "is_error": h["is_error"] == true,
		"result": fmt.Sprint(h["result"]), "session_id": id, "structured_output": rep, "total_cost_usd": 0.01}
	if h["text_reply"] == true { // the reply as text, not structured
		b, _ := json.Marshal(rep)
		out["result"], out["structured_output"] = "Here it is:\n```json\n"+string(b)+"\n```", nil
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	if h["is_error"] == true {
		return 1
	}
	return 0
}

// fakeCodex answers codex exec --json -o <file> like codex: events on
// stdout, the last message's text in the file. Like codex-cli 0.162, it
// refuses an --output-schema that isn't strict.
func fakeCodex(args []string) int {
	prompt, _ := io.ReadAll(os.Stdin)
	logCall("codex", args, map[string]any{"prompt": string(prompt), "cwd": cwd(), "run_env": os.Getenv(RunEnv), "tmp": tmpDir()})
	_, fl := fakeFlags(args)
	if f := fl["--output-schema"]; f != "" {
		var schema any
		b, _ := os.ReadFile(f)
		if err := json.Unmarshal(b, &schema); err != nil {
			fmt.Fprintln(os.Stderr, "invalid_json_schema:", err)
			return 1
		}
		if msg := strictSchema("schema", schema); msg != "" {
			fmt.Fprintln(os.Stderr, "invalid_json_schema: "+msg)
			return 1
		}
	}
	cfg := loadFake()
	h := cfg.popHarness()
	saveJSON("fake.json", cfg)
	rep, _ := h["reply"].(map[string]any)
	sideEffects(h, rep)
	thread := "codex-thread-1"
	if len(args) > 2 && args[1] == "resume" {
		thread = args[2]
	}
	fmt.Printf(`{"type":"thread.started","thread_id":%q}`+"\n", thread)
	b, _ := json.Marshal(rep)
	os.WriteFile(fl["-o"], []byte("Done.\n"+string(b)+"\n"), 0o644)
	return 0
}

// strictSchema is what strict mode refuses in a schema, or "": an object
// without additionalProperties false or with a key not in required, or a
// field with no type.
func strictSchema(at string, v any) string {
	m, _ := v.(map[string]any)
	if m == nil {
		return at + ": want an object"
	}
	if m["type"] == nil && m["enum"] == nil && m["anyOf"] == nil {
		return at + ": no type"
	}
	if props, ok := m["properties"].(map[string]any); ok || m["type"] == "object" {
		if m["additionalProperties"] != false {
			return "'" + at + ".additionalProperties' is required to be supplied and to be false when strict is true"
		}
		req, _ := m["required"].([]any)
		for k, p := range props {
			if !contains(anyStrings(req), k) {
				return at + ": " + k + " is not in required"
			}
			if msg := strictSchema(at+"."+k, p); msg != "" {
				return msg
			}
		}
	}
	if items, ok := m["items"]; ok {
		return strictSchema(at+".items", items)
	}
	return ""
}

func anyStrings(list []any) []string {
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = fmt.Sprint(v)
	}
	return out
}

// fakeGh is gh pr list, create and view over the PRs in fake.json.
func fakeGh(args []string) int {
	logCall("gh", args, map[string]any{"cwd": cwd(), "run_env": os.Getenv(RunEnv), "no_prompt": noPrompt()})
	cfg := loadFake()
	defer saveJSON("fake.json", cfg)
	if len(args) < 2 || args[0] != "pr" {
		return 2
	}
	pos, fl := fakeFlags(args[2:])
	print := func(v any) int {
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
		return 0
	}
	if r := cfg.pop("gh pr " + args[1]); r != nil {
		if n := toInt(r["exit"]); n != 0 {
			fmt.Fprintln(os.Stderr, r["stderr"])
			return n
		}
		return print(r)
	}
	switch args[1] {
	case "list":
		out := []any{}
		for _, p := range cfg.PRs {
			if p.Head == fl["--head"] && p.State == "OPEN" {
				out = append(out, map[string]any{"number": p.Number, "url": p.URL, "isCrossRepository": p.Fork})
			}
		}
		return print(out)
	case "create":
		n := 7 + len(cfg.PRs)
		p := fakePR{Number: n, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", n), Head: fl["--head"], Base: fl["--base"],
			Title: fl["--title"], Body: fl["--body"], State: "OPEN"}
		cfg.PRs = append(cfg.PRs, p)
		fmt.Println(p.URL)
		return 0
	case "view":
		for _, p := range cfg.PRs {
			if len(pos) > 0 && (fmt.Sprint(p.Number) == pos[0] || p.URL == pos[0] || p.Head == pos[0]) {
				return print(map[string]any{"state": p.State, "headRefOid": "", "mergeCommit": nil, "url": p.URL})
			}
		}
		fmt.Fprintln(os.Stderr, "no pull requests found")
		return 1
	}
	return 2
}

// fakeGit logs the call, fails it if fake.json says so, else runs git.
func fakeGit(args []string) int {
	logCall("git", args, map[string]any{"cwd": cwd(), "run_env": os.Getenv(RunEnv), "no_prompt": noPrompt()})
	if len(args) > 0 {
		cfg := loadFake()
		r := cfg.pop("git " + args[0])
		saveJSON("fake.json", cfg)
		if r != nil && r["sleep"] != nil {
			hang(r)
		}
		if r != nil {
			fmt.Fprintln(os.Stderr, r["stderr"])
			return toInt(r["exit"])
		}
	}
	git := os.Getenv("FAKE_GIT")
	if err := syscall.Exec(git, append([]string{git}, args...), os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return 1
}

// fakeCheck is a project check: it passes unless fake.json scripts
// "check <name>" or "check" with exit and output.
func fakeCheck(args []string) int {
	logCall("check", args, map[string]any{"cwd": cwd(), "run_env": os.Getenv(RunEnv), "tmp": tmpDir()})
	cfg := loadFake()
	defer saveJSON("fake.json", cfg)
	r := cfg.pop("check " + strings.Join(args, " "))
	if r == nil {
		r = cfg.pop("check")
	}
	if r == nil {
		fmt.Println("ok")
		return 0
	}
	if r["sleep"] != nil {
		saveJSON("fake.json", cfg)
		hang(r)
	}
	fmt.Println(r["output"])
	return toInt(r["exit"])
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
