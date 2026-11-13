package cli

import (
	"fmt"
	"strings"
	"testing"
)

// reply is a claude -p --output-format json reply, cut to the fields
// log-run reads plus a few it ignores, as Claude Code prints them.
func reply(session string, usd float64, models ...string) string {
	var mu []string
	for _, m := range models {
		mu = append(mu, fmt.Sprintf(`%q: {"inputTokens": 100, "outputTokens": 20, "cacheReadInputTokens": 3000, "cacheCreationInputTokens": 400, "costUSD": 0.1}`, m))
	}
	return fmt.Sprintf(`{"type": "result", "subtype": "success", "is_error": false, "result": "Done.\nEND_x", "session_id": %q,
  "total_cost_usd": %v, "usage": {"input_tokens": 2, "output_tokens": 4}, "modelUsage": {%s}}
`, session, usd, strings.Join(mu, ", "))
}

func TestLogRunPassesTheReplyThroughAndRecordsIt(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.env["MAESTRO_TICKET_ACTOR"] = "junior-engineer"
	in := reply("s1", 1.25, "claude-sonnet-5-5", "claude-haiku-5-5")
	code, out, errs := h.run(in, "log-run", "--ticket", "T-1")
	if code != 0 || out != in || errs != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
	// The resume reports the session's total so far.
	if code, out, _ := h.run(reply("s1", 2, "claude-sonnet-5-5"), "log-run", "--actor", "senior-engineer"); code != 0 || !strings.Contains(out, `"s1"`) {
		t.Fatalf("resume: exit %d, %q", code, out)
	}
	var exp struct {
		Runs []struct {
			Session, Ticket, Actor        string
			CostUSD                       float64 `json:"cost_usd"`
			InputTokens, CacheWriteTokens int64
		}
	}
	h.ok(&exp, "export")
	if len(exp.Runs) != 1 {
		t.Fatalf("runs = %+v", exp.Runs)
	}
	r := exp.Runs[0]
	if r.Session != "s1" || r.Ticket != "T-1" || r.Actor != "junior-engineer" || r.CostUSD != 2 {
		t.Fatalf("run = %+v", r)
	}
}

func TestLogRunReadsStreamJSON(t *testing.T) {
	h := newHarness(t)
	in := `{"type": "system", "subtype": "init", "session_id": "s9"}` + "\n" + `{"type": "assistant", "session_id": "s9"}` + "\n" + reply("s9", 0.5, "m")
	if code, out, errs := h.run(in, "log-run", "--actor", "planner"); code != 0 || out != in {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
	var costs struct {
		Total struct {
			Runs        int
			USD         float64
			InputTokens int64 `json:"input_tokens"`
		}
	}
	h.ok(&costs, "costs")
	if costs.Total.Runs != 1 || costs.Total.USD != 0.5 || costs.Total.InputTokens != 100 {
		t.Fatalf("costs = %+v", costs)
	}
}

// A run that failed, or a bad flag, still hands the bot its reply; the
// error goes to stderr, never into the reply on stdout.
func TestLogRunNeverLosesTheReply(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "a")
	for _, c := range []struct {
		name, in string
		args     []string
		code     int
		err      string
	}{
		{"no reply", "", nil, 2, "no reply on stdin"},
		{"timeout's words", "Terminated\n", nil, 2, "isn't claude -p --output-format json"},
		{"no result", `{"type": "assistant"}`, nil, 2, "no result message"},
		{"no cost", `{"type": "result", "session_id": "s1"}`, nil, 2, "no session_id or total_cost_usd"},
		{"json flag", reply("s1", 1), []string{"--json"}, 2, "takes no --json"},
		{"bad flag", reply("s1", 1), []string{"--tikcet", "T-1"}, 2, "flag provided but not defined"},
	} {
		code, out, errs := h.run(c.in, append([]string{"log-run", "--actor", "qa-lead"}, c.args...)...)
		if code != c.code || out != c.in || !strings.Contains(errs, c.err) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d, the reply, and %q", c.name, code, out, errs, c.code, c.err)
		}
	}
	var costs struct{ Total struct{ Runs int } }
	h.ok(&costs, "costs")
	if costs.Total.Runs != 0 {
		t.Fatalf("a failed log-run recorded %d runs", costs.Total.Runs)
	}
}

func TestLogRunKeepsTheCostOfAWrongTicket(t *testing.T) {
	h := newHarness(t)
	in := reply("s1", 1, "m")
	if code, out, errs := h.run(in, "log-run", "--actor", "qa-lead", "--ticket", "T-7"); code != 0 || out != in || !strings.Contains(errs, "no ticket T-7") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
	var costs struct{ Rows []struct{ Key string } }
	h.ok(&costs, "costs", "--by", "ticket")
	if len(costs.Rows) != 1 || costs.Rows[0].Key != "(no ticket)" {
		t.Fatalf("rows = %+v", costs.Rows)
	}
}

// claude -p --output-format json --verbose prints every message as one array.
func TestLogRunReadsVerboseJSON(t *testing.T) {
	h := newHarness(t)
	in := `[{"type": "system", "session_id": "s3"}, ` + strings.TrimSpace(reply("s3", 0.75, "m")) + `]`
	if code, out, errs := h.run(in, "log-run", "--actor", "planner"); code != 0 || out != in {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
	var costs struct{ Total struct{ USD float64 } }
	h.ok(&costs, "costs")
	if costs.Total.USD != 0.75 {
		t.Fatalf("costs = %+v", costs)
	}
	if code, _, errs := h.run(`[{"type": "result"`, "log-run", "--actor", "planner"); code != 2 || !strings.Contains(errs, "isn't claude -p") {
		t.Fatalf("a cut array: exit %d, %q", code, errs)
	}
}

func TestCostsGroups(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "planned", "--goal", "speed")
	h.ok(nil, "create", "--title", "found", "--actor", "reviewer")
	logs := []struct {
		session, actor, ticket string
		usd                    float64
	}{
		{"e1", "junior-engineer", "T-1", 2},
		{"e2", "junior-engineer", "T-1", 4},
		{"e3", "junior-engineer", "T-2", 3},
		{"r1", "reviewer", "T-1", 6},
		{"p1", "planner", "", 1},
	}
	for _, l := range logs {
		args := []string{"log-run", "--actor", l.actor}
		if l.ticket != "" {
			args = append(args, "--ticket", l.ticket)
		}
		if code, _, errs := h.run(reply(l.session, l.usd, "m"), args...); code != 0 {
			t.Fatalf("%s: exit %d: %s", l.session, code, errs)
		}
	}
	type row struct {
		Key           string
		Runs, Tickets int
		USD, Percent  float64
		RunMedian     float64 `json:"run_median"`
		TicketMedian  float64 `json:"ticket_median"`
		TicketP90     float64 `json:"ticket_p90"`
	}
	var got struct {
		Rows  []row
		Total row
	}
	h.ok(&got, "costs")
	// junior-engineer: runs 2, 4, 3 (median 3); tickets T-1 6, T-2 3.
	want := []row{
		{Key: "junior-engineer", Runs: 3, Tickets: 2, USD: 9, Percent: 56.25, RunMedian: 3, TicketMedian: 4.5, TicketP90: 6},
		{Key: "reviewer", Runs: 1, Tickets: 1, USD: 6, Percent: 37.5, RunMedian: 6, TicketMedian: 6, TicketP90: 6},
		{Key: "planner", Runs: 1, Tickets: 0, USD: 1, Percent: 6.25, RunMedian: 1},
	}
	if fmt.Sprint(got.Rows) != fmt.Sprint(want) {
		t.Fatalf("by actor:\n got %+v\nwant %+v", got.Rows, want)
	}
	// All tickets: T-1 12, T-2 3.
	if tot := got.Total; tot.Key != "all" || tot.Runs != 5 || tot.USD != 16 || tot.Tickets != 2 || tot.TicketMedian != 7.5 || tot.TicketP90 != 12 {
		t.Fatalf("total = %+v", tot)
	}
	keys := func(by string) string {
		h.ok(&got, "costs", "--by", by)
		var k []string
		for _, r := range got.Rows {
			k = append(k, fmt.Sprintf("%s=%g", r.Key, r.USD))
		}
		return strings.Join(k, " ")
	}
	for by, want := range map[string]string{
		"ticket": "T-1=12 T-2=3 (no ticket)=1",
		"goal":   "speed=12 (no goal)=3 (no ticket)=1",
		"origin": "unknown=12 reviewer=3 (no ticket)=1",
		"tries":  "0=15 (no ticket)=1",
	} {
		if got := keys(by); got != want {
			t.Errorf("--by %s: %s; want %s", by, got, want)
		}
	}
	h.ok(&got, "costs", "--ticket", "T-2")
	if got.Total.Runs != 1 || got.Total.USD != 3 {
		t.Fatalf("--ticket T-2: %+v", got.Total)
	}
	if code, out, _ := h.run("", "costs", "--by", "ticket"); code != 0 || !strings.Contains(out, "MEDIAN/TICKET") || !strings.Contains(out, "(no ticket)") {
		t.Fatalf("text: exit %d\n%s", code, out)
	}
	h.fails(2, "costs", "--by", "model")
	h.fails(2, "costs", "--since", "yesterday")
	h.ok(&got, "costs", "--since", "2000-01-01")
	if got.Total.Runs != 5 {
		t.Fatalf("--since 2000-01-01 kept %d runs", got.Total.Runs)
	}
	h.ok(&got, "costs", "--since", "1h")
	if got.Total.Runs != 5 {
		t.Fatalf("--since 1h kept %d runs", got.Total.Runs)
	}
}
