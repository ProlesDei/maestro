//go:build unix

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
)

// bundleOf runs stats bundle as a person, with args, and returns the
// file it wrote.
func bundleOf(t *testing.T, h *harness, args ...string) (bundleOut, string) {
	t.Helper()
	var o bundleOut
	h.ok(&o, append([]string{"stats", "bundle"}, args...)...)
	b, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	return o, string(b)
}

// bundleFiles are the bundle files in dir, and any temp file left.
func bundleFiles(dir string) []string {
	a, _ := filepath.Glob(filepath.Join(dir, "bundle-case-*"))
	b, _ := filepath.Glob(filepath.Join(dir, ".tmp-bundle-*"))
	return append(a, b...)
}

// Never markers planted everywhere text lives miss a bundle, with spend
// in it too. Local ids and times miss it as numbers and as text. Spend
// goes in only with --cost.
func TestBundleMarkers(t *testing.T) {
	t.Parallel()
	l, markers := markerScene(t)
	_, body := bundleOf(t, l.h, "--case", "T-1", "--cost")
	for _, m := range markers {
		if strings.Contains(body, m) {
			t.Errorf("the bundle holds %q", m)
		}
	}
	if !strings.Contains(body, `"case": "T1"`) {
		t.Fatalf("no case label:\n%s", body)
	}
	const epoch = 1999999999123
	start := time.UnixMilli(epoch)
	usd := 4242.4242
	r := statsRow("R-zqmarkzz", 917351, "build", start, "33333333-3333-4333-8333-333333333333",
		stats.Model{Model: "opus-5.5", InputCum: 9876543, CostCum: &usd, CostSrc: "result"})
	statsPut(t, l.h.dir, r)
	local := []string{"917351", "1999999999123", "1999999999", "R-zqmarkzz", "zqmarkzz", "33333333", "2033", start.UTC().Format(time.RFC3339)}
	cost := []string{"4242.4242", "9876543"}
	_, plain := bundleOf(t, l.h, "--case", "T-917351")
	for _, m := range append(local, cost...) {
		if strings.Contains(plain, m) {
			t.Errorf("a bundle without --cost holds %q", m)
		}
	}
	_, with := bundleOf(t, l.h, "--case", "T-917351", "--cost")
	for _, m := range local {
		if strings.Contains(with, m) {
			t.Errorf("a bundle with --cost holds %q", m)
		}
	}
	for _, m := range cost {
		if !strings.Contains(with, m) {
			t.Errorf("--cost left out %q:\n%s", m, with)
		}
	}
}

// A bundle is a person's: a bot, by either name, and any run, a session
// a bot started included, get forbidden and no file.
func TestBundleRefusals(t *testing.T) {
	h, _ := pinHarness(t, "")
	statsPut(t, h.dir, caseRows(time.Now().Add(-time.Hour))...)
	for _, bot := range botProfiles {
		h.env["MAESTRO_TICKET_ACTOR"] = bot
		for _, args := range [][]string{{"stats", "bundle", "--case", "T-1"}, {"stats", "bundle", "--case", "T-1", "--cost"}, {"stats", "bundle"}} {
			if got := h.fails(2, args...); got != "forbidden" {
				t.Errorf("%s %v: %s; want forbidden", bot, args, got)
			}
		}
	}
	delete(h.env, "MAESTRO_TICKET_ACTOR")
	if got := h.fails(2, "stats", "bundle", "--case", "T-1", "--actor", "qa-lead"); got != "forbidden" {
		t.Errorf("--actor qa-lead: %s", got)
	}
	for _, actor := range []string{"", "paul", "chief-of-staff"} {
		h.env["MAESTRO_TICKET_ACTOR"] = actor
		h.env[runs.RunIDEnv] = "R-aaaaaaaa"
		if got := h.fails(2, "stats", "bundle", "--case", "T-1"); got != "forbidden" {
			t.Errorf("a run, actor %q: %s", actor, got)
		}
	}
	_, _, errs := h.run("", "stats", "bundle", "--case", "T-1")
	if errs != "maestro-ticket: not allowed for this actor: stats bundle: MAESTRO_RUN_ID is set, and a bundle is for a person to write and read, never a run\n" {
		t.Errorf("refusal: %q", errs)
	}
	delete(h.env, runs.RunIDEnv)
	if got := bundleFiles(filepath.Join(h.dir, "bundles")); len(got) != 0 {
		t.Fatalf("a refusal wrote %v", got)
	}
	// A person gets it; a bad request or a case with nothing is no file.
	h.env["MAESTRO_TICKET_ACTOR"] = "paul"
	bundleOf(t, h, "--case", "T-1")
	for _, c := range []struct {
		code int
		args []string
	}{{2, nil}, {2, []string{"--case", "build"}}, {2, []string{"--case", "T-1", "T-2"}}, {2, []string{"--case", "R-aaaaaaab"}}, {3, []string{"--case", "T-9"}}} {
		code, out, errs := h.run("", append([]string{"stats", "bundle"}, c.args...)...)
		if code != c.code {
			t.Errorf("%v: exit %d; want %d %s%s", c.args, code, c.code, out, errs)
		}
	}
	if got := bundleFiles(filepath.Join(h.dir, "bundles")); len(got) != 1 {
		t.Fatalf("files %v; want the person's one", got)
	}
	stats.SetOff(h.dir, true)
	if code, out, _ := h.run("", "stats", "bundle", "--case", "T-1"); code != 0 ||
		out != "stats are off (STATS_OFF sits beside the store); stats health says more; nothing was written\n" {
		t.Errorf("off: exit %d %q", code, out)
	}
	stats.SetOff(h.dir, false)
	if got := bundleFiles(filepath.Join(h.dir, "bundles")); len(got) != 1 {
		t.Fatalf("files %v after a bundle with stats off", got)
	}
}

// stats bundle prints the file's path, size, sha256 and row counts, and
// that nothing was sent; never a value of the body.
func TestBundlePrintsOnly(t *testing.T) {
	h, _ := pinHarness(t, "")
	statsPut(t, h.dir, caseRows(time.Now().Add(-time.Hour))...)
	code, out, errs := h.run("", "stats", "bundle", "--case", "T-1")
	if code != 0 || errs != "" {
		t.Fatalf("exit %d: %s%s", code, out, errs)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	m := regexp.MustCompile(`^wrote (\S+): (\d+) bytes, sha256 ([0-9a-f]{64})$`).FindStringSubmatch(lines[0])
	if len(lines) != 3 || m == nil ||
		lines[1] != "rows: 3 runs, 3 findings, 0 steps; no spend or tokens (--cost adds them)" ||
		lines[2] != "nothing was sent: read the file first; it leaves this machine only by a person's hand, after the owner says yes. stats case T-1 shows the same runs" {
		t.Fatalf("output:\n%s", out)
	}
	body, err := os.ReadFile(m[1])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if filepath.Dir(m[1]) != filepath.Join(h.dir, "bundles") || m[2] != strconv.Itoa(len(body)) || m[3] != hex.EncodeToString(sum[:]) {
		t.Errorf("path, size or sha256 off: %v, %d bytes", m[1:], len(body))
	}
	// Never in the current folder, where git add -A would take it.
	if got := bundleFiles(h.dir); len(got) != 0 {
		t.Errorf("a bundle in the current folder: %v", got)
	}
	var b struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal(body, &b); err != nil || len(b.Runs) != 3 {
		t.Fatalf("body: %v %s", err, body)
	}
	for _, v := range []string{"R1", "R2", "T1", "build", "review", "tier_senior", "route_full", "nogo", "blocker", "opus"} {
		if !strings.Contains(string(body), `"`+v+`"`) {
			t.Errorf("the body lacks %q", v)
		}
		if strings.Contains(out, v) {
			t.Errorf("stdout shows %q", v)
		}
	}
	// --json holds the same, and --out names a folder or a file.
	sub := filepath.Join(h.dir, "out")
	os.Mkdir(sub, 0o755)
	var o map[string]any
	h.ok(&o, "stats", "bundle", "--case", "T-1", "--cost", "--out", "out")
	if keys := slices.Sorted(maps.Keys(o)); !slices.Equal(keys, []string{"bytes", "cost", "path", "rows", "sent", "sha256"}) || o["sent"] != false || o["cost"] != true ||
		filepath.Dir(o["path"].(string)) != sub {
		t.Errorf("--json: %v", o)
	}
	file := filepath.Join(sub, "mine.json")
	h.ok(&o, "stats", "bundle", "--case", "T-1", "--out", file)
	if o["path"] != file {
		t.Errorf("--out file: %v", o["path"])
	}
	if code, _, errs := h.run("", "stats", "bundle", "--case", "T-1", "--out", file); code == 0 || !strings.Contains(errs, "is there already") {
		t.Errorf("over a file: exit %d %s", code, errs)
	}
}
