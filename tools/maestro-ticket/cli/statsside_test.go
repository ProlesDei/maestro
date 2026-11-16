//go:build unix

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
)

// sideRow is a run's side facts as its row in stats.db holds them: why,
// tier, tries, again_of, drops and prompt_bytes, with - or -1 for NULL.
func sideRow(t *testing.T, dir, id string) string {
	t.Helper()
	var why, tier, againOf string
	var tries, drops, prompt int
	statsQuery(t, dir, `SELECT coalesce(w.name, '-'), coalesce(tr.name, '-'), coalesce(f.tries, -1), coalesce(f.again_of, '-'),
		coalesce(f.drops, -1), coalesce(f.prompt_bytes, -1)
		FROM run_facts f LEFT JOIN codes w ON w.id = f.why LEFT JOIN codes tr ON tr.id = f.tier WHERE f.run = '`+id+`'`,
		&why, &tier, &tries, &againOf, &drops, &prompt)
	return strings.Join([]string{why, tier, strconv.Itoa(tries), againOf, strconv.Itoa(drops), strconv.Itoa(prompt)}, " ")
}

// promptBytes is the size of run id's stdin.
func promptBytes(t *testing.T, j *jobHarness, id string) string {
	t.Helper()
	fi, err := os.Stat(filepath.Join(j.h.dir, "runs", id+".in"))
	if err != nil {
		t.Fatal(err)
	}
	return strconv.FormatInt(fi.Size(), 10)
}

// reviewScene makes git and a review-prep stand-in that routes T-1 light
// and every other ticket full.
func reviewScene(t *testing.T, j *jobHarness) {
	t.Helper()
	c := exec.Command("git", "init", "-q")
	c.Dir = j.h.dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	prep := filepath.Join(j.h.dir, ".claude", "skills", "maestro-code-review", "scripts")
	os.MkdirAll(prep, 0o755)
	os.WriteFile(filepath.Join(prep, "review-prep.sh"), []byte(`[ "$1" = --route ] || exit 9
case $2 in
T-1) echo pr=4; echo lines=10; echo route=light; echo "why=all docs" ;;
*) echo pr=5; echo lines=30; echo route=full; echo "why=code" ;;
esac
`), 0o755)
}

// moveTo walks a ticket through statuses.
func moveTo(j *jobHarness, id string, statuses ...string) {
	for _, st := range statuses {
		j.h.ok(nil, "transition", id, "--to", st)
	}
}

// A run whose command ends at once still has its side facts in its row:
// job writes them before the supervisor starts, so the command finds the
// whole file the moment it runs.
func TestSideFileBeforeSupervisor(t *testing.T) {
	t.Parallel()
	j := newJobHarness(t, "junior-engineer")
	copySide := strings.Replace(fakeClaude, "env >", `cp "$(dirname "$MAESTRO_TICKET_STORE")/runs/$MAESTRO_RUN_ID.stats" "$d/side"
env >`, 1)
	if err := os.WriteFile(filepath.Join(j.bin, "claude"), []byte(copySide), 0o755); err != nil {
		t.Fatal(err)
	}
	j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
	_, r := j.job("", "build", "T-1")
	j.waitSupervisors()
	seen, err := os.ReadFile(filepath.Join(j.out, r.ID, "side"))
	if err != nil {
		t.Fatalf("no side file when the command ran: %v", err)
	}
	if s, ok := readSide(filepath.Join(j.h.dir, "runs"), r.ID); !ok || s.Why != "tier_junior" || !strings.Contains(string(seen), `"why":"tier_junior"`) {
		t.Fatalf("side file when the command ran: %q", seen)
	}
	if got, want := sideRow(t, j.h.dir, r.ID), "tier_junior junior 0 - 0 "+promptBytes(t, j, r.ID); got != want {
		t.Fatalf("row %q; want %q", got, want)
	}
}

// Stats off, by the marker or the variable, writes no side file.
func TestStatsOffNoSideFile(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"marker", "env"} {
		j := newJobHarness(t, "junior-engineer")
		if how == "marker" {
			j.h.ok(nil, "stats", "off")
		} else {
			j.env = append(j.env, stats.OffEnv+"=off")
		}
		j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
		_, r := j.job("", "build", "T-1")
		j.waitSupervisors()
		if _, err := os.Lstat(filepath.Join(j.h.dir, "runs", r.ID+".stats")); err == nil {
			t.Errorf("stats off by %s: %s.stats written", how, r.ID)
		}
	}
}

// Each place startJob picks a model writes its why, and the ticket's tier
// and send-backs as it saw them.
func TestWhyEveryDecision(t *testing.T) {
	t.Parallel()
	j := newJobHarness(t, "planner")
	reviewScene(t, j)
	j.h.ok(nil, "create", "--title", "light", "--tier", "senior")  // T-1
	j.h.ok(nil, "create", "--title", "full")                       // T-2
	j.h.ok(nil, "create", "--title", "senior", "--tier", "senior") // T-3
	j.h.ok(nil, "create", "--title", "junior", "--tier", "junior") // T-4
	j.h.ok(nil, "create", "--title", "none")                       // T-5
	j.h.ok(nil, "create", "--title", "back", "--tier", "junior")   // T-6
	moveTo(j, "T-1", "in_progress", "in_review")
	moveTo(j, "T-2", "in_progress", "in_review")
	moveTo(j, "T-6", "in_progress", "in_review", "in_progress", "in_review", "in_progress")
	for _, c := range []struct {
		stdin string
		args  []string
		want  string // why tier tries again_of drops
	}{
		{"", []string{"build", "T-3"}, "tier_senior senior 0 - 0"},
		{"", []string{"build", "T-4"}, "tier_junior junior 0 - 0"},
		{"", []string{"build", "T-5"}, "tier_none none 0 - 0"},
		{"", []string{"build", "T-6"}, "sent_back junior 2 - 0"},
		{"", []string{"review", "T-1"}, "route_light senior 0 - 0"},
		{"", []string{"review", "T-1", "--full"}, "route_full senior 0 - 0"},
		{"F2 was wrong\n", []string{"review", "T-2", "--drop", "F2,F3", "--drop-file", "-"}, "route_full none 0 - 2"},
		{"", []string{"qa", "T-2"}, "fixed none 0 - 0"},
		{"ship the login page\n", []string{"plan"}, "fixed - -1 - 0"},
	} {
		_, r := j.job(c.stdin, c.args...)
		j.waitSupervisors()
		if got, want := sideRow(t, j.h.dir, r.ID), c.want+" "+promptBytes(t, j, r.ID); got != want {
			t.Errorf("job %v: row %q; want %q", c.args, got, want)
		}
	}
}

// A --model that changes the pick is an override; one that names the
// pick changes nothing. The tier stays either way.
func TestWhyOverride(t *testing.T) {
	t.Parallel()
	j := newJobHarness(t, "planner")
	reviewScene(t, j)
	j.h.ok(nil, "create", "--title", "light", "--tier", "junior")
	moveTo(j, "T-1", "in_progress", "in_review")
	j.h.ok(nil, "create", "--title", "build", "--tier", "junior")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"build", "T-2", "--model", "opus"}, "override junior 0"},
		{[]string{"build", "T-2", "--model", "sonnet"}, "tier_junior junior 0"},
		{[]string{"review", "T-1", "--model", "opus"}, "override junior 0"},
		{[]string{"review", "T-1", "--model", "claude-sonnet-4-5-20250929"}, "override junior 0"},
	} {
		_, r := j.job("", c.args...)
		j.waitSupervisors()
		if got := sideRow(t, j.h.dir, r.ID); !strings.HasPrefix(got, c.want+" ") {
			t.Errorf("job %v: row %q; want %q", c.args, got, c.want)
		}
	}
}

// A resume and a retry carry on the tier and send-backs of the run they
// follow; a retry names that run and keeps its prompt's drops; a resume
// of a run with no side file has none to carry.
func TestWhyAgainResume(t *testing.T) {
	t.Parallel()
	j := newJobHarness(t, "reviewer")
	reviewScene(t, j)
	j.h.ok(nil, "create", "--title", "a", "--tier", "senior")
	j.h.ok(nil, "create", "--title", "b", "--tier", "junior")
	moveTo(j, "T-2", "in_progress", "in_review", "in_progress", "in_review")
	_, first := j.job("F1 is moot\n", "review", "T-2", "--drop", "F1", "--drop-file", "-")
	_, res := j.resume("go on\n", first.ID)
	j.waitSupervisors()
	if got, want := sideRow(t, j.h.dir, first.ID), "route_full junior 1 - 1 "+promptBytes(t, j, first.ID); got != want {
		t.Fatalf("first: %q; want %q", got, want)
	}
	if got, want := sideRow(t, j.h.dir, res.ID), "resume junior 1 - 0 6"; got != want {
		t.Fatalf("resume: %q; want %q", got, want)
	}
	s, again := j.job("", "again", res.ID)
	j.waitSupervisors()
	if got, want := sideRow(t, j.h.dir, again.ID), "again junior 1 "+res.ID+" 1 "+promptBytes(t, j, again.ID); got != want {
		t.Fatalf("again: %q; want %q (%+v)", got, want, s)
	}

	moveTo(j, "T-1", "in_progress")
	_, b := j.job("", "build", "T-1")
	j.waitSupervisors()
	os.Remove(filepath.Join(j.h.dir, "runs", b.ID+sideExt))
	_, rb := j.resume("", b.ID, "--opus")
	j.waitSupervisors()
	if got, want := sideRow(t, j.h.dir, rb.ID), "resume - -1 - 0 "+promptBytes(t, j, rb.ID); got != want {
		t.Fatalf("resume of a run with no side file: %q; want %q", got, want)
	}
}

// The side file goes in whole or not at all, through a rename, and only
// a whole one is read; a plain launch writes none.
func TestSideFileAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	os.Mkdir(dir, 0o755)
	id := "R-aaaaaaaa"
	writeSide(dir, id, nil)
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("a nil side wrote %v", ents)
	}
	want := sideFacts{Why: "route_full", Tier: "senior", Tries: ptr(3), Drops: []string{"F1", "F2"}, PromptBytes: ptr(40)}
	writeSide(dir, id, &sideFacts{Why: "fixed", PromptBytes: ptr(9)})
	writeSide(dir, id, &want)
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || ents[0].Name() != id+sideExt {
		t.Fatalf("files %v; want only %s", ents, id+sideExt)
	}
	if got, ok := readSide(dir, id); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("read %+v, %v; want %+v", got, ok, want)
	}
	for name, b := range map[string][]byte{
		"cut":   []byte(`{"why":"fixed","prompt_`),
		"huge":  []byte(`{"why":"fixed"` + strings.Repeat(" ", sideMax) + `}`),
		"empty": nil,
	} {
		os.WriteFile(filepath.Join(dir, id+sideExt), b, 0o644)
		if got, ok := readSide(dir, id); ok {
			t.Errorf("%s side file read as %+v", name, got)
		}
		c := &call{env: Env{Getenv: func(string) string { return "" }, DiskFree: halfFree}}
		row := c.statsRow(filepath.Dir(dir), runs.Record{ID: id, State: runs.Done}, claudeReply{}, os.ErrNotExist)
		if row.Why != "" || row.Tier != "" || row.Tries != nil || row.PromptBytes != nil || row.Drops != nil {
			t.Errorf("%s side file gave the row %+v", name, row)
		}
	}

	// A plain launch's side file only marks stats on: no stream, no
	// prompt size; with stats off it has none.
	l := newLauncher(t, "planner")
	r := l.launch("", "--", "true")
	l.waitEnded(r.ID, 10*time.Second)
	if s, ok := readSide(filepath.Join(l.h.dir, "runs"), r.ID); !ok || !reflect.DeepEqual(s, sideFacts{}) {
		t.Fatalf("a plain launch's side file: %+v, %v", s, ok)
	}
	l.env = append(l.env, stats.OffEnv+"=off")
	r = l.launch("", "--", "true")
	l.waitEnded(r.ID, 10*time.Second)
	if _, err := os.Stat(filepath.Join(l.h.dir, "runs", r.ID+sideExt)); err == nil {
		t.Fatal("a plain launch with stats off wrote a side file")
	}
}
