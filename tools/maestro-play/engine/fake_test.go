package engine

// Fake maestro-ticket and runner binaries. The test binary is symlinked as
// maestro-ticket and maestro-runner in a temp dir; TestMain dispatches on
// the name it was started as. Both keep their state in $FAKE_DIR:
//
//	tickets.json  the store: tickets with claims and comments, idempotency keys
//	runner.json   scripted runner replies, by request name (first is used and
//	              dropped, the last one sticks); "session" replies come from
//	              the fake harness list
//	calls.jsonl   every call: the binary, its args, and the prompt it sent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	switch filepath.Base(os.Args[0]) {
	case "maestro-ticket":
		os.Exit(fakeTicket(os.Args[1:]))
	case "maestro-runner":
		os.Exit(fakeRunner(os.Args[1:]))
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
	// WaitingOnDeps makes claim fail with waiting_on_deps.
	WaitingOnDeps bool `json:"waiting_on_deps,omitempty"`
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
	bools := map[string]bool{"--json": true, "--unclaimed": true, "--claimed": true, "--release": true, "--next": true}
	flags := map[string]string{}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
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
	if sub == "list" {
		var list []*fakeTicketT
		statuses := strings.Split(fl["--status"], ",")
		for _, t := range s.Tickets {
			if fl["--status"] != "" && !contains(statuses, t.Status) {
				continue
			}
			if fl["--role"] != "" && t.Role != fl["--role"] {
				continue
			}
			if fl["--unclaimed"] == "true" && t.Claim != nil && !t.Claim.Expired {
				continue
			}
			latest := ""
			if len(t.Comments) > 0 {
				latest = t.Comments[len(t.Comments)-1].Body
			}
			if p := fl["--latest-prefix"]; p != "" && !strings.HasPrefix(latest, p) {
				continue
			}
			if p := fl["--not-latest-prefix"]; p != "" && strings.HasPrefix(latest, p) {
				continue
			}
			c := *t
			c.Comments = nil
			list = append(list, &c)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Priority != list[j].Priority {
				return list[i].Priority < list[j].Priority
			}
			a, _ := strconv.Atoi(strings.TrimPrefix(list[i].ID, "T-"))
			b, _ := strconv.Atoi(strings.TrimPrefix(list[j].ID, "T-"))
			return a < b
		})
		if list == nil {
			list = []*fakeTicketT{}
		}
		return out(map[string]any{"tickets": list})
	}
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
		if t.WaitingOnDeps {
			return fail("waiting_on_deps", 4)
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
		to := fl["--to"]
		if to == "blocked" {
			if t.Status == "blocked" {
				return fail("invalid_transition", 2)
			}
			t.BlockedFrom = t.Status
		}
		if to == "in_progress" && (t.Status == "in_review" || t.Status == "in_qa") {
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

type fakeRunnerConfig struct {
	Replies map[string][]map[string]any `json:"replies"`
	// Harness replies for session start and resume, in order.
	Harness []map[string]any `json:"harness"`
}

var twoWord = map[string]bool{
	"session": true, "usage": true, "worktree": true, "install": true, "open": true, "pr": true, "memory": true,
}

func fakeRunner(args []string) int {
	var bot, role, ticketID string
	i := 0
	for ; i < len(args); i++ {
		switch args[i] {
		case "--json":
		case "--bot":
			i++
			bot = args[i]
		case "--role":
			i++
			role = args[i]
		case "--ticket":
			i++
			ticketID = args[i]
		case "--run":
			i++
		default:
			goto done
		}
	}
done:
	words := args[i:]
	name := words[0]
	if twoWord[name] && len(words) > 1 {
		name += " " + words[1]
	}
	_, fl := fakeFlags(words)
	extra := map[string]any{"bot": bot, "role": role, "run_env": os.Getenv(RunEnv)}
	if p := fl["--prompt-file"]; p != "" {
		b, _ := os.ReadFile(p)
		extra["prompt"] = string(b)
	}
	logCall("runner", words, extra)

	var cfg fakeRunnerConfig
	loadJSON("runner.json", &cfg)
	defer saveJSON("runner.json", &cfg)
	pop := func(list *[]map[string]any) map[string]any {
		if len(*list) == 0 {
			return nil
		}
		r := (*list)[0]
		if len(*list) > 1 {
			*list = (*list)[1:]
		}
		return r
	}
	var reply map[string]any
	if name == "session start" || name == "session resume" {
		h := pop(&cfg.Harness)
		if h == nil {
			h = map[string]any{"reply": map[string]any{"status": "failed", "summary": "no scripted reply"}}
		}
		reply = map[string]any{"status": "ok", "exit_code": 0, "output": "", "family": "claude"}
		for k, v := range h {
			reply[k] = v
		}
	} else if list, ok := cfg.Replies[name]; ok {
		reply = pop(&list)
		cfg.Replies[name] = list
	}
	if reply == nil {
		reply = map[string]any{"status": "ok", "exit_code": 0, "output": ""}
		switch name {
		case "preflight":
			reply["harness"], reply["family"] = "claude-code", "claude"
			reply["project"] = map[string]any{"main_branch": "main"}
			reply["limits"] = map[string]any{"step_visits": 3, "retries": 0}
		case "worktree add":
			reply["path"] = filepath.Join(fakeDir(), "wt", ticketID)
			reply["branch"] = "maestro/" + ticketID + "-x"
		case "check":
			reply["summary"] = "3 passed"
			reply["output_path"] = ""
		}
	}
	if reply["steal"] == true {
		var s fakeStore
		loadJSON("tickets.json", &s)
		if t := s.Tickets[ticketID]; t != nil {
			s.NextToken++
			t.Claim = &fakeClaim{Holder: "thief/1", Token: s.NextToken}
		}
		saveJSON("tickets.json", &s)
		delete(reply, "steal")
	}
	if reply["stop_file"] == true {
		os.WriteFile(filepath.Join(os.Getenv("FAKE_COMMON"), "STOP"), nil, 0o644)
		delete(reply, "stop_file")
	}
	b, _ := json.Marshal(reply)
	fmt.Println(string(b))
	return 0
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
