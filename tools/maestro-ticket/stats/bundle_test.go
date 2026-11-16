package stats

import (
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// bundleLists are testLists with the kinds a case's reviews and streams
// use.
var bundleLists = func() Lists {
	l := maps.Clone(testLists)
	l["why"] = []string{"tier_senior", "route_full", "resume"}
	l["tier"] = []string{"senior"}
	l["verdict"] = []string{"go", "nogo"}
	l["finding"] = []string{"finding", "follow_up", "dropped"}
	l["sev"] = []string{"blocker", "major"}
	l["prior"] = []string{"new"}
	l["action"] = []string{"auto-fix"}
	l["step"] = []string{"Bash:go", "Edit", "Read"}
	return l
}()

// caseDB holds ticket 5's build, review and the build's resume, and a
// run on ticket 6.
func caseDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), DBName), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Prepare(t.Context(), db, bundleLists); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	at := func(r Run, off time.Duration) Run {
		r.Ticket, r.StartedAt, r.EndedAt = 5, base.Add(off), base.Add(off+time.Minute)
		return r
	}
	build := at(testRun(runID(1)), 0)
	build.Why, build.Tier, build.Turns = "tier_senior", "senior", ptrTo(int64(38))
	build.Tail = []TailCall{{Code: "Bash:go"}, {Code: "Edit", Fail: true}}
	build.Steps = []Step{{Code: "Bash:go", Calls: 4, Fails: 1, MsSum: 9000, MsMax: 5000}, {Code: "Read", Calls: 9}}
	build.CtxPeak = ptrTo(int64(120000))
	review := at(testRun(runID(2)), 20*time.Minute)
	review.Session, review.Job, review.Why, review.Verdict = "22222222-2222-4222-8222-222222222222", "review", "route_full", "nogo"
	review.Findings = []Finding{{Fid: "F1", Kind: "finding", Sev: "blocker", Prior: "new", Pass: ptrTo(int64(2))}, {Kind: "follow_up", N: 2}}
	resume := at(testRun(runID(3)), 40*time.Minute)
	resume.ResumeOf, resume.Why = runID(1), "resume"
	three := 3.5
	resume.Models[0].CostCum, resume.Models[0].InputCum = &three, 30
	other := testRun(runID(4))
	other.Ticket = 6
	for _, r := range []Run{build, review, resume, other} {
		if err := Write(t.Context(), db, r); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func ptrTo[T any](v T) *T { return &v }

// Ids become labels in run order, clock times 15-minute offsets from
// the first run, and the run that resumed points at its parent's label.
func TestBundleRelabel(t *testing.T) {
	db := caseDB(t)
	b, err := CaseBundle(t.Context(), db, 5, bundleLists, false)
	if err != nil {
		t.Fatal(err)
	}
	if b.Case.s != "T1" || len(b.Runs) != 3 || b.SpanDays != 0 || b.Schema != BundleSchema || int(b.Stats) != Version() {
		t.Fatalf("header: %+v", b)
	}
	for i, want := range []struct {
		label, resumeOf string
		start           Offset15m
		job             string
	}{{"R1", "", 0, "build"}, {"R2", "", 15, "review"}, {"R3", "R1", 30, "build"}} {
		r := b.Runs[i]
		if r.Run.s != want.label || r.ResumeOf.s != want.resumeOf || r.Start == nil || *r.Start != want.start || r.Job.name != want.job {
			t.Errorf("run %d: %s resume_of %q start %v job %s", i, r.Run.s, r.ResumeOf.s, r.Start, r.Job.name)
		}
	}
	r1 := b.Runs[0]
	if len(r1.Tail) != 2 || r1.Tail[1].Code.name != "Edit" || !r1.Tail[1].Fail || len(r1.Steps) != 2 || r1.Steps[0].MsMax != 5000 ||
		*r1.Turns != 38 || *r1.WallMs != 60000 || r1.Role.name != "planner" {
		t.Errorf("R1: %+v", r1)
	}
	if f := b.Runs[1].Findings; len(f) != 2 || *f[0].Fid != 1 || *f[0].Pass != 2 || f[0].Sev.name != "blocker" || f[1].N != 2 {
		t.Errorf("R2 findings: %+v", f)
	}
	body, err := b.Encode(bundleLists)
	if err != nil {
		t.Fatal(err)
	}
	for _, local := range []string{runID(1), runID(2), "11111111-1111-4111-8111-111111111111", "22222222", `"ticket"`, `"session"`, "2026"} {
		if strings.Contains(string(body), local) {
			t.Errorf("bundle holds %q", local)
		}
	}
	if _, err := CaseBundle(t.Context(), db, 7, bundleLists, false); !errors.Is(err, ErrNoRuns) {
		t.Errorf("a ticket with no runs: %v", err)
	}
}

// Spend, tokens and the context peak go in only with cost, and only
// under the cost section.
func TestBundleCostOptIn(t *testing.T) {
	db := caseDB(t)
	plain, err := CaseBundle(t.Context(), db, 5, bundleLists, false)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := plain.Encode(bundleLists)
	for _, k := range []string{`"cost"`, `"usd"`, `"tokens"`, `"ctx_peak"`, "120000", "3.5", "1.5"} {
		if strings.Contains(string(body), k) {
			t.Errorf("a bundle without cost holds %s", k)
		}
	}
	if !strings.Contains(string(body), `"with_cost": false`) {
		t.Error("the header doesn't say cost is out")
	}
	b, err := CaseBundle(t.Context(), db, 5, bundleLists, true)
	if err != nil {
		t.Fatal(err)
	}
	c := b.Cost
	if !b.WithCost || c == nil || len(c.Runs) != 3 || c.Runs[0].Run.s != "R1" || *c.Runs[0].CtxPeak != 120000 {
		t.Fatalf("cost section: %+v", c)
	}
	// The resume counts only what it added to its session: 3.5 - 1.5.
	if m := c.Runs[2].Models; len(m) != 1 || m[0].Model.name != "opus-5.5" || *m[0].USD != 2 || *m[0].Tokens != 20 {
		t.Errorf("R3 spend: %+v", m)
	}
}

// lintBundle lists the fields of t whose type is off the bundle's list,
// or spend outside the cost section, or a field with no json name.
func lintBundle(t reflect.Type, path string, cost bool) []string {
	leaf := map[reflect.Type]bool{}
	for _, l := range []reflect.Type{reflect.TypeFor[Label](), reflect.TypeFor[Count](), reflect.TypeFor[Ms](),
		reflect.TypeFor[Bytes](), reflect.TypeFor[Pct](), reflect.TypeFor[Offset15m](), reflect.TypeFor[Day](),
		reflect.TypeFor[Enum](), reflect.TypeFor[Ver](), reflect.TypeFor[bool]()} {
		leaf[l] = true
	}
	switch {
	case t == usdType || t == tokensType:
		if !cost {
			return []string{path + ": " + t.Name() + " outside CostSection"}
		}
		return nil
	case leaf[t]:
		return nil
	case t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice:
		return lintBundle(t.Elem(), path, cost)
	case t.Kind() == reflect.Struct && t.PkgPath() == reflect.TypeFor[Bundle]().PkgPath() && t.Name() != "":
		var bad []string
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" || !f.IsExported() {
				bad = append(bad, path+"."+f.Name+": no json name")
				continue
			}
			bad = append(bad, lintBundle(f.Type, path+"."+name, cost || t == costType)...)
		}
		return bad
	}
	return []string{path + ": " + t.String()}
}

// Every bundle field has a bundle type; the lint and the value check
// both catch each kind of slip.
func TestBundleTypesLint(t *testing.T) {
	if bad := lintBundle(reflect.TypeFor[Bundle](), "bundle", false); len(bad) > 0 {
		t.Fatalf("bundle fields off the list:\n%s", strings.Join(bad, "\n"))
	}
	type bare struct {
		N int64 `json:"n"`
	}
	type float struct {
		F float64 `json:"f"`
	}
	type text struct {
		S string `json:"s"`
	}
	type spend struct {
		U USD `json:"u"`
	}
	type tokens struct {
		T []*Tokens `json:"t"`
	}
	type keyed struct {
		M map[string]Count `json:"m"`
	}
	type clock struct {
		T time.Time `json:"t"`
	}
	type untagged struct {
		N Count
	}
	type named struct {
		B myBool `json:"b"`
	}
	for _, v := range []any{bare{1}, float{1}, text{"x"}, spend{1}, tokens{[]*Tokens{ptrTo(Tokens(1))}}, keyed{map[string]Count{"x": 1}},
		clock{time.Now()}, untagged{1}, named{true}} {
		rt := reflect.TypeOf(v)
		if bad := lintBundle(rt, "x", false); len(bad) == 0 {
			t.Errorf("the lint passes %s", rt)
		}
		if err := checkValue(reflect.ValueOf(v), "x", bundleLists, false); err == nil {
			t.Errorf("the value check passes %s", rt)
		}
	}
	// Spend under the cost section passes both.
	c := CostSection{Runs: []RunCost{{Run: Label{"R1"}, CtxPeak: ptrTo(Tokens(5)), Models: []ModelCost{{Model: Enum{"model", "opus"}, USD: ptrTo(USD(1))}}}}}
	if err := checkValue(reflect.ValueOf(c), "cost", bundleLists, false); err != nil {
		t.Error(err)
	}
}

type myBool bool

// The value check refuses a value off its type's shape, list or bounds,
// and names the field.
func TestBundleValueCheck(t *testing.T) {
	db := caseDB(t)
	fresh := func() *Bundle {
		b, err := CaseBundle(t.Context(), db, 5, bundleLists, true)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := fresh().Check(bundleLists); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		field string
		bad   func(*Bundle)
	}{
		{"bundle.case", func(b *Bundle) { b.Case = Label{"T917351"} }},
		{"bundle.runs[0].run", func(b *Bundle) { b.Runs[0].Run = Label{"R0"} }},
		{"bundle.runs[0].run", func(b *Bundle) { b.Runs[0].Run = Label{"X1"} }},
		{"bundle.runs[1].resume_of", func(b *Bundle) { b.Runs[1].ResumeOf = Label{"r1"} }},
		{"bundle.runs[0].run", func(b *Bundle) { b.Runs[0].Run = Label{"R-aaaaaaab"} }},
		{"bundle.runs[0].job", func(b *Bundle) { b.Runs[0].Job = Enum{"job", "zqmark7"} }},
		{"bundle.runs[0].job", func(b *Bundle) { b.Runs[0].Job = Enum{"nokind", "build"} }},
		{"bundle.runs[0].tail[0].code", func(b *Bundle) { b.Runs[0].Tail[0].Code = Enum{"step", "Bash:zqmark7"} }},
		{"bundle.runs[0].wall_ms", func(b *Bundle) { b.Runs[0].WallMs = ptrTo(Ms(maxMs)) }},
		{"bundle.runs[0].wall_ms", func(b *Bundle) { b.Runs[0].WallMs = ptrTo(Ms(1999999999123)) }},
		{"bundle.runs[0].claude_ms", func(b *Bundle) { b.Runs[0].ClaudeMs = ptrTo(Ms(-1)) }},
		{"bundle.runs[0].start_min", func(b *Bundle) { b.Runs[0].Start = ptrTo(Offset15m(7)) }},
		{"bundle.runs[0].disk_free_pct", func(b *Bundle) { b.Runs[0].DiskFree = ptrTo(Pct(101)) }},
		{"bundle.runs[0].turns", func(b *Bundle) { b.Runs[0].Turns = ptrTo(Count(-1)) }},
		{"bundle.runs[0].v_maestro", func(b *Bundle) { b.Runs[0].VMaestro = Ver{"zqmark7"} }},
		{"bundle.runs[0].v_claude", func(b *Bundle) { b.Runs[0].VClaude = Ver{"2.1.0-beta"} }},
		{"bundle.span_days", func(b *Bundle) { b.SpanDays = maxDays }},
		{"bundle.runs[0].steps[0].ms_sum", func(b *Bundle) { b.Runs[0].Steps[0].MsSum = -5 }},
		{"bundle.cost.runs[2].models[0].usd", func(b *Bundle) { b.Cost.Runs[2].Models[0].USD = ptrTo(USD(math.NaN())) }},
		{"bundle.cost.runs[0].ctx_peak", func(b *Bundle) { b.Cost.Runs[0].CtxPeak = ptrTo(Tokens(-1)) }},
	} {
		b := fresh()
		c.bad(b)
		err := b.Check(bundleLists)
		if err == nil || !strings.HasPrefix(err.Error(), c.field+": ") {
			t.Errorf("%s: %v", c.field, err)
		}
		if _, err := b.Encode(bundleLists); err == nil {
			t.Errorf("%s: Encode wrote it", c.field)
		}
	}
}

// A bad number in stats.db drops that value, not the bundle.
func TestBundleDropsOffBounds(t *testing.T) {
	db := caseDB(t)
	if _, err := db.Exec(`UPDATE run_facts SET claude_ms = 1999999999123, disk_free_pct = 400, exit = -1 WHERE run = ?`, runID(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE run_steps SET ms_max = -1 WHERE run = ? AND calls = 9`, runID(1)); err != nil {
		t.Fatal(err)
	}
	b, err := CaseBundle(t.Context(), db, 5, bundleLists, false)
	if err != nil {
		t.Fatal(err)
	}
	if r := b.Runs[0]; r.ClaudeMs != nil || r.DiskFree != nil || r.Exit != nil || len(r.Steps) != 1 {
		t.Errorf("R1 kept values off bounds: %+v", r)
	}
}

// A bundle goes whole into a new name and never over a file.
func TestSaveBundle(t *testing.T) {
	dir := t.TempDir()
	p1, err := SaveBundle(dir, []byte("{}\n"))
	if err != nil {
		t.Fatal(err)
	}
	p2, err := SaveBundle(dir, []byte("{}\n"))
	if err != nil || p1 == p2 || !strings.HasPrefix(filepath.Base(p1), "bundle-case-") || filepath.Ext(p1) != ".json" {
		t.Fatalf("%s %s %v", p1, p2, err)
	}
	if fi, _ := os.Stat(p1); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	named := filepath.Join(dir, "mine.json")
	if p, err := SaveBundle(named, []byte("{}\n")); err != nil || p != named {
		t.Fatalf("a named file: %s %v", p, err)
	}
	if _, err := SaveBundle(named, []byte("[]\n")); err == nil {
		t.Error("wrote over a file")
	}
	if b, _ := os.ReadFile(named); string(b) != "{}\n" {
		t.Errorf("the file changed: %q", b)
	}
	if ents, _ := filepath.Glob(filepath.Join(dir, ".tmp-*")); len(ents) != 0 {
		t.Errorf("left %v", ents)
	}
}

// The header holds these keys and no more.
func TestBundleJSONNames(t *testing.T) {
	b, err := CaseBundle(t.Context(), caseDB(t), 5, bundleLists, true)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := b.Encode(bundleLists)
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"bundle_schema", "stats_schema", "case", "with_cost", "span_days", "runs", "cost"} {
		if _, ok := m[k]; !ok {
			t.Errorf("no %s", k)
		}
	}
	if len(m) != 7 {
		t.Errorf("keys %v", slices.Collect(maps.Keys(m)))
	}
}
