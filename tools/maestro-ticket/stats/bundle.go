package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A case bundle is one ticket's runs as a file a person may send to
// maintainers once the owner says yes to it (decision 23). Writing one
// sends nothing. Every field has one of the types below, which a test
// holds every bundle struct to: ids become labels in run order, clock
// times become offsets from the case's first run, names stay on their Go
// list, and no session id goes in. Spend and tokens go only in
// CostSection, which only --cost fills.

// BundleSchema is the shape of a bundle file: a change to its fields
// bumps it.
const BundleSchema = 1

// The number types a bundle field may have.
type (
	Count     int64   // a count or small number, 0 or more
	Ms        int64   // a duration in ms, under 400 days
	Bytes     int64   // a size in bytes
	Pct       int64   // a percentage, 0 to 100
	Offset15m int64   // minutes from the case's first run start, a multiple of 15
	Day       int64   // whole days, under 400
	USD       float64 // dollars, only under CostSection
	Tokens    int64   // tokens, only under CostSection
)

// Label names the case (T1) or one of its runs (R1, R2, ... in run
// order). Only the relabeler builds one.
type Label struct{ s string }

// Enum is a name on its kind's Go list, or other.
type Enum struct{ kind, name string }

// Ver is a checked version: a commit sha of 7 to 40 hex, or Claude
// Code's digits and dots.
type Ver struct{ s string }

// The ids and times a bundle is read from. They are opaque: they convert
// only to a Label or an Offset15m, so no raw id or clock time can be
// copied into a bundle field.
type (
	RunID    struct{ v string }
	TicketID struct{ v int64 }
	EpochMs  struct{ v int64 } // 0 for none
)

func (l Label) IsZero() bool                 { return l.s == "" }
func (l Label) MarshalJSON() ([]byte, error) { return json.Marshal(l.s) }
func (e Enum) IsZero() bool                  { return e.name == "" }
func (e Enum) MarshalJSON() ([]byte, error)  { return json.Marshal(e.name) }
func (v Ver) IsZero() bool                   { return v.s == "" }
func (v Ver) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.s)
}

// Bundle is a case bundle.
type Bundle struct {
	Schema   Count        `json:"bundle_schema"`
	Stats    Count        `json:"stats_schema"`
	Case     Label        `json:"case"`
	WithCost bool         `json:"with_cost"`
	SpanDays Day          `json:"span_days"`
	Runs     []BundleRun  `json:"runs"`
	Cost     *CostSection `json:"cost,omitempty"`
}

// BundleRun is one run: its run_facts row less its ids, times and
// session, with its tail, steps and findings.
type BundleRun struct {
	Run      Label      `json:"run"`
	ResumeOf Label      `json:"resume_of,omitzero"`
	AgainOf  Label      `json:"again_of,omitzero"`
	Start    *Offset15m `json:"start_min,omitempty"`

	Role       Enum `json:"role,omitzero"`
	Job        Enum `json:"job,omitzero"`
	Model      Enum `json:"model,omitzero"`
	Effort     Enum `json:"effort,omitzero"`
	Why        Enum `json:"why,omitzero"`
	Tier       Enum `json:"tier,omitzero"`
	Nudge      Enum `json:"nudge,omitzero"`
	Stop       Enum `json:"stop,omitzero"`
	State      Enum `json:"state,omitzero"`
	Outcome    Enum `json:"outcome,omitzero"`
	Reason     Enum `json:"reason,omitzero"`
	Subtype    Enum `json:"subtype,omitzero"`
	StopReason Enum `json:"stop_reason,omitzero"`
	Head       Enum `json:"head,omitzero"`
	Verdict    Enum `json:"verdict,omitzero"`
	Mode       Enum `json:"mode,omitzero"`
	Stream     Enum `json:"stream,omitzero"`
	OpenCode   Enum `json:"open_code,omitzero"`
	RingErr    Enum `json:"ring_err,omitzero"`
	OS         Enum `json:"os,omitzero"`
	Arch       Enum `json:"arch,omitzero"`

	Lean       bool  `json:"lean"`
	AfterDeath bool  `json:"after_death"`
	IsError    *bool `json:"is_error,omitempty"`
	WaitCapped *bool `json:"wait_capped,omitempty"`
	Rang       *bool `json:"rang,omitempty"`

	Exit        *Count `json:"exit,omitempty"`
	Tries       *Count `json:"tries,omitempty"`
	Drops       *Count `json:"drops,omitempty"`
	Turns       *Count `json:"turns,omitempty"`
	ToolCalls   *Count `json:"tool_calls,omitempty"`
	ToolFails   *Count `json:"tool_fails,omitempty"`
	Compactions *Count `json:"compactions,omitempty"`
	Retries     *Count `json:"retries,omitempty"`
	Subagents   *Count `json:"subagents,omitempty"`
	Escaped     *Count `json:"escaped,omitempty"`
	VTier       *Count `json:"v_tier,omitempty"`

	WallMs   *Ms `json:"wall_ms,omitempty"`
	ClaudeMs *Ms `json:"claude_ms,omitempty"`
	APIMs    *Ms `json:"api_ms,omitempty"`
	WaitMs   *Ms `json:"wait_ms,omitempty"`
	OpenMs   *Ms `json:"open_ms,omitempty"`

	PromptBytes *Bytes `json:"prompt_bytes,omitempty"`
	ReplyBytes  *Bytes `json:"reply_bytes,omitempty"`
	DiskFree    *Pct   `json:"disk_free_pct,omitempty"`
	InodeFree   *Pct   `json:"inode_free_pct,omitempty"`

	VMaestro Ver `json:"v_maestro,omitzero"`
	VClaude  Ver `json:"v_claude,omitzero"`
	// ToolAtPin says whether maestro-ticket was built from the Maestro
	// commit install.yaml pins; the binary's own commit stays out.
	ToolAtPin *bool `json:"tool_at_pin,omitempty"`

	Tail     []BundleCall    `json:"tail,omitempty"`
	Steps    []BundleStep    `json:"steps,omitempty"`
	Findings []BundleFinding `json:"findings,omitempty"`
}

// BundleCall is one of a run's last tool calls.
type BundleCall struct {
	Code Enum `json:"code"`
	Fail bool `json:"fail"`
}

// BundleStep is a run_steps row.
type BundleStep struct {
	Code  Enum  `json:"code"`
	Calls Count `json:"calls"`
	Fails Count `json:"fails"`
	MsSum Ms    `json:"ms_sum"`
	MsMax Ms    `json:"ms_max"`
}

// BundleFinding is a findings row; Fid is the number of its F id.
type BundleFinding struct {
	Fid    *Count `json:"fid,omitempty"`
	Kind   Enum   `json:"kind,omitzero"`
	Sev    Enum   `json:"sev,omitzero"`
	Action Enum   `json:"action,omitzero"`
	Prior  Enum   `json:"prior,omitzero"`
	Pass   *Count `json:"pass,omitempty"`
	N      Count  `json:"n"`
}

// CostSection is each run's own spend and its context peak: only with
// --cost, since spend shows a team's size and budget.
type CostSection struct {
	Runs []RunCost `json:"runs"`
}

// RunCost is one run's spend per model family; a nil USD can't be known.
type RunCost struct {
	Run     Label       `json:"run"`
	CtxPeak *Tokens     `json:"ctx_peak,omitempty"`
	Models  []ModelCost `json:"models,omitempty"`
}

// ModelCost is a run's own use of one model family.
type ModelCost struct {
	Model  Enum    `json:"model"`
	USD    *USD    `json:"usd,omitempty"`
	Tokens *Tokens `json:"tokens,omitempty"`
}

// ErrNoRuns is a case with no run in stats.db.
var ErrNoRuns = errors.New("no runs in stats.db")

// Bounds the value check holds each number to.
const (
	maxBundleRuns = 9999 // labels run to R9999
	maxMs         = 400 * 24 * 60 * 60 * 1000
	maxDays       = 400
	maxCount      = 10_000_000
	maxBytes      = 1 << 40
	maxUSD        = 1e7
	maxTokens     = 1 << 40
)

var (
	labelShape   = regexp.MustCompile(`^[TR][1-9][0-9]{0,3}$`)
	versionShape = regexp.MustCompile(`^([0-9a-f]{7,40}|[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6})$`)
)

// bundleValue is a type the value check reads.
type bundleValue interface{ check(Lists) error }

func inRange(n, lo, hi int64) error {
	if n < lo || n > hi {
		return fmt.Errorf("%d is outside %d to %d", n, lo, hi)
	}
	return nil
}

func (n Count) check(Lists) error  { return inRange(int64(n), 0, maxCount) }
func (n Ms) check(Lists) error     { return inRange(int64(n), 0, maxMs-1) }
func (n Bytes) check(Lists) error  { return inRange(int64(n), 0, maxBytes) }
func (n Pct) check(Lists) error    { return inRange(int64(n), 0, 100) }
func (n Day) check(Lists) error    { return inRange(int64(n), 0, maxDays-1) }
func (n Tokens) check(Lists) error { return inRange(int64(n), 0, maxTokens) }

func (n Offset15m) check(Lists) error {
	if n%15 != 0 {
		return fmt.Errorf("%d is no multiple of 15", n)
	}
	return inRange(int64(n), 0, maxMs/60000-1)
}

func (n USD) check(Lists) error {
	if f := float64(n); math.IsNaN(f) || f < 0 || f > maxUSD {
		return fmt.Errorf("%v dollars is outside 0 to %v", f, maxUSD)
	}
	return nil
}

func (l Label) check(Lists) error {
	if l.s != "" && !labelShape.MatchString(l.s) {
		return fmt.Errorf("%q is no label", l.s)
	}
	return nil
}

func (e Enum) check(lists Lists) error {
	if e.name == "" {
		return nil
	}
	names, ok := lists[e.kind]
	if !ok {
		return fmt.Errorf("no list for %q", e.kind)
	}
	if e.name != Other && !slices.Contains(names, e.name) {
		return fmt.Errorf("%q is not on the %s list", e.name, e.kind)
	}
	return nil
}

func (v Ver) check(Lists) error {
	if v.s != "" && !versionShape.MatchString(v.s) {
		return fmt.Errorf("%q is no version", v.s)
	}
	return nil
}

var (
	costType   = reflect.TypeFor[CostSection]()
	usdType    = reflect.TypeFor[USD]()
	tokensType = reflect.TypeFor[Tokens]()
	checkType  = reflect.TypeFor[bundleValue]()
	boolType   = reflect.TypeFor[bool]()
)

// Check holds every value of b to its type: a label's shape, a name on
// its list, a number in its bounds, spend only under CostSection, and no
// type off the bundle's list. It names the first field that fails.
func (b *Bundle) Check(lists Lists) error {
	return checkValue(reflect.ValueOf(b).Elem(), "bundle", lists, false)
}

func checkValue(v reflect.Value, path string, lists Lists, cost bool) error {
	t := v.Type()
	if t.Kind() != reflect.Pointer && t.Implements(checkType) {
		if (t == usdType || t == tokensType) && !cost {
			return fmt.Errorf("%s: spend or tokens outside the cost section", path)
		}
		if err := v.Interface().(bundleValue).check(lists); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	switch v.Kind() {
	case reflect.Bool:
		if t == boolType {
			return nil
		}
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return checkValue(v.Elem(), path, lists, cost)
	case reflect.Slice:
		for i := range v.Len() {
			if err := checkValue(v.Index(i), fmt.Sprintf("%s[%d]", path, i), lists, cost); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		cost = cost || t == costType
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if !f.IsExported() || name == "" {
				return fmt.Errorf("%s.%s: a bundle field needs a json name", path, f.Name)
			}
			if err := checkValue(v.Field(i), path+"."+name, lists, cost); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("%s: a %s is no bundle type", path, t)
}

// caseRow is one run as the bundle reads it: its ids and times opaque,
// the rest already in bundle types.
type caseRow struct {
	run, resumeOf, againOf RunID
	ticket                 TicketID
	started, ended         EpochMs
	ctxPeak                *Tokens
	b                      BundleRun
}

// relabeler gives the case and its runs their labels.
type relabeler struct{ runs map[RunID]Label }

func (r *relabeler) add(id RunID) {
	r.runs[id] = Label{"R" + strconv.Itoa(len(r.runs)+1)}
}

// run is id's label, or none for a run outside the case.
func (r *relabeler) run(id RunID) Label { return r.runs[id] }

// caseLabel is the case's label: a bundle holds one ticket.
func caseLabel(TicketID) Label { return Label{"T1"} }

// offset is t's minutes past base, rounded down to 15.
func offset(t, base EpochMs) *Offset15m {
	if t.v == 0 || t.v < base.v {
		return nil
	}
	o := Offset15m((t.v - base.v) / 60000 / 15 * 15)
	if o.check(nil) != nil {
		return nil
	}
	return &o
}

// bounded is n as a T when it is in its type's bounds, else nil.
func bounded[T interface {
	~int64
	bundleValue
}](n sql.NullInt64) *T {
	if !n.Valid {
		return nil
	}
	v := T(n.Int64)
	if v.check(nil) != nil {
		return nil
	}
	return &v
}

func boolOf(n sql.NullInt64) *bool {
	if !n.Valid {
		return nil
	}
	b := n.Int64 == 1
	return &b
}

// enumOf is name as an Enum of kind: other when it is off the list, and
// none for a kind with no list.
func enumOf(lists Lists, kind, name string) Enum {
	names, ok := lists[kind]
	if name == "" || !ok {
		return Enum{}
	}
	if name != Other && !slices.Contains(names, name) {
		name = Other
	}
	return Enum{kind, name}
}

// bundleEnums are the run_facts enum columns a bundle reads, in the
// order of BundleRun's Enum fields.
var bundleEnums = []string{"role", "job", "model", "effort", "why", "tier", "nudge", "stop", "state", "outcome", "reason",
	"subtype", "stop_reason", "head", "verdict", "mode", "stream", "open_code", "ring_err", "os", "arch"}

func (b *BundleRun) enums() []*Enum {
	return []*Enum{&b.Role, &b.Job, &b.Model, &b.Effort, &b.Why, &b.Tier, &b.Nudge, &b.Stop, &b.State, &b.Outcome, &b.Reason,
		&b.Subtype, &b.StopReason, &b.Head, &b.Verdict, &b.Mode, &b.Stream, &b.OpenCode, &b.RingErr, &b.OS, &b.Arch}
}

// enumKind is the codes kind of an enum column.
func enumKind(col string) string {
	if col == "open_code" {
		return "step"
	}
	return col
}

// readCase reads the runs of the ticket f picks, oldest first.
func readCase(ctx context.Context, tx *sql.Tx, f Filter, names map[int64]string, lists Lists) ([]caseRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT run, resume_of, again_of, ticket, started_at, ended_at, tail, v_tool, v_maestro, v_claude,
		lean, after_death, is_error, wait_capped, rang,
		exit, tries, drops, turns, tool_calls, tool_fails, compactions, retries, subagents, escaped, v_tier,
		wall_ms, claude_ms, api_ms, wait_ms, open_ms, prompt_bytes, reply_bytes, disk_free_pct, inode_free_pct, ctx_peak,
		`+strings.Join(bundleEnums, ", ")+`
		FROM run_facts WHERE run IN (`+picked+`) ORDER BY started_at, run`, f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []caseRow
	for rows.Next() {
		var r caseRow
		var run string
		var resumeOf, againOf, tail, vTool, vMaestro, vClaude sql.NullString
		var ticket, started, ended sql.NullInt64
		var bools [5]sql.NullInt64
		var counts [11]sql.NullInt64
		var ms [5]sql.NullInt64
		var bytes, pcts [2]sql.NullInt64
		var ctxPeak sql.NullInt64
		e := make([]sql.NullInt64, len(bundleEnums))
		dst := []any{&run, &resumeOf, &againOf, &ticket, &started, &ended, &tail, &vTool, &vMaestro, &vClaude}
		for _, p := range [][]sql.NullInt64{bools[:], counts[:], ms[:], bytes[:], pcts[:]} {
			for i := range p {
				dst = append(dst, &p[i])
			}
		}
		dst = append(dst, &ctxPeak)
		for i := range e {
			dst = append(dst, &e[i])
		}
		if err := rows.Scan(dst...); err != nil {
			return nil, err
		}
		r.run, r.resumeOf, r.againOf = RunID{run}, RunID{resumeOf.String}, RunID{againOf.String}
		r.ticket, r.started, r.ended = TicketID{ticket.Int64}, EpochMs{started.Int64}, EpochMs{ended.Int64}
		r.ctxPeak = bounded[Tokens](ctxPeak)
		b := &r.b
		for i, p := range b.enums() {
			*p = enumOf(lists, enumKind(bundleEnums[i]), nameOf(names, e[i]))
		}
		b.Lean, b.AfterDeath = bools[0].Int64 == 1, bools[1].Int64 == 1
		b.IsError, b.WaitCapped, b.Rang = boolOf(bools[2]), boolOf(bools[3]), boolOf(bools[4])
		for i, p := range []**Count{&b.Exit, &b.Tries, &b.Drops, &b.Turns, &b.ToolCalls, &b.ToolFails, &b.Compactions,
			&b.Retries, &b.Subagents, &b.Escaped, &b.VTier} {
			*p = bounded[Count](counts[i])
		}
		for i, p := range []**Ms{&b.WallMs, &b.ClaudeMs, &b.APIMs, &b.WaitMs, &b.OpenMs} {
			*p = bounded[Ms](ms[i])
		}
		b.PromptBytes, b.ReplyBytes = bounded[Bytes](bytes[0]), bounded[Bytes](bytes[1])
		b.DiskFree, b.InodeFree = bounded[Pct](pcts[0]), bounded[Pct](pcts[1])
		if v := (Ver{vMaestro.String}); v.check(nil) == nil {
			b.VMaestro = v
		}
		if v := (Ver{vClaude.String}); v.check(nil) == nil {
			b.VClaude = v
		}
		if vTool.Valid && b.VMaestro.s != "" {
			at := strings.HasPrefix(vTool.String, b.VMaestro.s)
			b.ToolAtPin = &at
		}
		for _, c := range parseTail(tail.String, names) {
			b.Tail = append(b.Tail, BundleCall{Code: enumOf(lists, "step", c.Code), Fail: c.Fail})
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// readSteps reads the run_steps rows of the runs f picks, by run.
func readSteps(ctx context.Context, tx *sql.Tx, f Filter, names map[int64]string, lists Lists) (map[RunID][]BundleStep, error) {
	rows, err := tx.QueryContext(ctx, `SELECT run, code, calls, fails, ms_sum, ms_max FROM run_steps
		WHERE run IN (`+picked+`) ORDER BY run, rowid`, f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[RunID][]BundleStep{}
	for rows.Next() {
		var run string
		var code, calls, fails, sum, most sql.NullInt64
		if err := rows.Scan(&run, &code, &calls, &fails, &sum, &most); err != nil {
			return nil, err
		}
		s := BundleStep{Code: enumOf(lists, "step", nameOf(names, code)), Calls: Count(calls.Int64), Fails: Count(fails.Int64),
			MsSum: Ms(sum.Int64), MsMax: Ms(most.Int64)}
		// A row off its bounds drops alone, not the whole bundle.
		if checkValue(reflect.ValueOf(s), "step", lists, false) != nil {
			continue
		}
		out[RunID{run}] = append(out[RunID{run}], s)
	}
	return out, rows.Err()
}

// CaseBundle builds the bundle of one ticket's runs from stats.db,
// with each run's spend when cost is set, and checks it. It refuses a
// stats.db at another schema (ErrNewer, ErrNoSchema) and a ticket with
// no runs (ErrNoRuns).
func CaseBundle(ctx context.Context, db *sql.DB, ticket int64, lists Lists, cost bool) (*Bundle, error) {
	if ticket <= 0 {
		return nil, ErrNoRuns
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := checkVersion(ctx, tx); err != nil {
		return nil, err
	}
	names, err := codeNames(ctx, tx)
	if err != nil {
		return nil, err
	}
	f := Filter{Ticket: ticket}
	rows, err := readCase(ctx, tx, f, names, lists)
	switch {
	case err != nil:
		return nil, err
	case len(rows) == 0:
		return nil, ErrNoRuns
	case len(rows) > maxBundleRuns:
		return nil, fmt.Errorf("%d runs: a bundle holds %d at most", len(rows), maxBundleRuns)
	}
	steps, err := readSteps(ctx, tx, f, names, lists)
	if err != nil {
		return nil, err
	}
	finds, err := readFindings(ctx, tx, f, names)
	if err != nil {
		return nil, err
	}
	var spend map[string][]Spend
	if cost {
		if spend, err = readSpend(ctx, tx, f, names); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	labels := relabeler{runs: map[RunID]Label{}}
	var base, last EpochMs
	for _, r := range rows {
		labels.add(r.run)
		if r.started.v != 0 && (base.v == 0 || r.started.v < base.v) {
			base = r.started
		}
		last.v = max(last.v, r.ended.v, r.started.v)
	}
	b := &Bundle{Schema: BundleSchema, Stats: Count(Version()), Case: caseLabel(rows[0].ticket), WithCost: cost}
	if base.v != 0 {
		b.SpanDays = min(Day((last.v-base.v)/(24*60*60*1000)), maxDays-1)
	}
	if cost {
		b.Cost = &CostSection{}
	}
	for _, r := range rows {
		run := r.b
		run.Run, run.ResumeOf, run.AgainOf = labels.run(r.run), labels.run(r.resumeOf), labels.run(r.againOf)
		run.Start = offset(r.started, base)
		run.Steps = steps[r.run]
		for _, x := range finds[r.run.v] {
			fd := BundleFinding{Kind: enumOf(lists, "finding", x.Kind), Sev: enumOf(lists, "sev", x.Sev),
				Action: enumOf(lists, "action", x.Action), Prior: enumOf(lists, "prior", x.Prior), N: Count(max(x.N, 1))}
			if fidShape.MatchString(x.Fid) {
				n, _ := strconv.ParseInt(x.Fid[1:], 10, 64)
				fd.Fid = bounded[Count](sql.NullInt64{Int64: n, Valid: true})
			}
			if x.Pass != nil {
				fd.Pass = bounded[Count](sql.NullInt64{Int64: *x.Pass, Valid: true})
			}
			run.Findings = append(run.Findings, fd)
		}
		b.Runs = append(b.Runs, run)
		if cost {
			rc := RunCost{Run: run.Run, CtxPeak: r.ctxPeak}
			for _, s := range spend[r.run.v] {
				mc := ModelCost{Model: enumOf(lists, "model", s.Model)}
				// To the millionth of a dollar: a delta's float noise
				// says nothing.
				if s.USD != nil {
					if u := USD(math.Round(*s.USD*1e6) / 1e6); u.check(nil) == nil {
						mc.USD = &u
					}
				}
				if s.Tokens != nil {
					mc.Tokens = bounded[Tokens](sql.NullInt64{Int64: *s.Tokens, Valid: true})
				}
				rc.Models = append(rc.Models, mc)
			}
			b.Cost.Runs = append(b.Cost.Runs, rc)
		}
	}
	if err := b.Check(lists); err != nil {
		return nil, err
	}
	return b, nil
}

// Encode checks b and returns its file's bytes.
func (b *Bundle) Encode(lists Lists) ([]byte, error) {
	if err := b.Check(lists); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// SaveBundle writes body to dest, whole or not at all, and never over a
// file that is there: in a folder dest names, as bundle-case-<rand4>.json;
// else at dest itself. It returns the file's path.
func SaveBundle(dest string, body []byte) (string, error) {
	dir, name := dest, ""
	if fi, err := os.Stat(dest); err != nil || !fi.IsDir() {
		dir, name = filepath.Dir(dest), filepath.Base(dest)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-bundle-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", err
	}
	for range 20 {
		n := name
		if n == "" {
			n = "bundle-case-" + rand4() + ".json"
		}
		p := filepath.Join(dir, n)
		err := os.Link(tmp.Name(), p)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			// A filesystem with no hard links: a rename, once no file
			// holds the name.
			if _, serr := os.Lstat(p); !errors.Is(serr, fs.ErrNotExist) {
				return "", fmt.Errorf("%s is there already", p)
			}
			return p, os.Rename(tmp.Name(), p)
		}
		if name != "" {
			return "", fmt.Errorf("%s is there already", p)
		}
	}
	return "", errors.New("no free bundle name in 20 tries")
}

// rand4 is four letters and digits for a bundle's name.
func rand4() string {
	const a = "abcdefghijklmnopqrstuvwxyz234567"
	b := make([]byte, 4)
	for i := range b {
		b[i] = a[rand.IntN(len(a))]
	}
	return string(b)
}
