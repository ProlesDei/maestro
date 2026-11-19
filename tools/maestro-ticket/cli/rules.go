package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// Standing values live beside the store, so code reads them with no bot
// typing them: board.json holds the planner's limit and the QA slots,
// rules.md the standing rules, one per line with its setter, and
// rules.log one line per change. Each write takes board.lock for a few
// milliseconds and replaces its file whole, so a reader that takes no
// lock still never sees half a file.
const (
	boardFile = "board.json"
	rulesFile = "rules.md"
	rulesLog  = "rules.log"
	boardLock = "board.lock"
)

// board is board.json. LimitVia is "look" while the planner's old look
// --limit seeds the limit, and "rules" once rules set limit took over.
type board struct {
	Limit    *int   `json:"limit,omitempty"`
	LimitVia string `json:"limit_via,omitempty"`
	QASlots  *int   `json:"qa_slots,omitempty"`
	// The admission floors (floor.go): free inodes as a whole percent,
	// and the 1-minute load average. Unset, none holds.
	FloorInodes *int     `json:"floor_inodes,omitempty"`
	FloorLoad   *float64 `json:"floor_load,omitempty"`
	SetBy       string   `json:"set_by,omitempty"`
	At          string   `json:"at,omitempty"`
}

// rulesWriters are the bots that may set each standing value; a person
// may set any. Text covers rules text, add and drop.
var rulesWriters = map[string][]string{
	"limit":    {"planner", "chief-of-staff"},
	"qa_slots": {"qa-lead", "chief-of-staff"},
	"floor":    {"planner", "chief-of-staff"},
	"text":     {"planner", "chief-of-staff"},
}

// refuseRulesWrite runs before any write of a standing value, and before
// any store opens: a run's session changes none, nor does a bot the
// value isn't for, whether the flag or the environment names it.
func (c *call) refuseRulesWrite(key string) error {
	if c.env.Getenv(runs.RunIDEnv) != "" {
		return fmt.Errorf("%w: rules: %s: %s is set; standing values change only outside a run", ticket.ErrForbidden, key, runs.RunIDEnv)
	}
	for _, bot := range c.botActors() {
		if !slices.Contains(rulesWriters[key], bot) {
			return fmt.Errorf("%w: rules: %s: %s is a bot, and only %s or a person set it", ticket.ErrForbidden, key, bot, strings.Join(rulesWriters[key], " or "))
		}
	}
	return c.needActor("rules")
}

// lockBoard takes board.lock beside the store, waiting up to 30s. It
// guards only the standing files, and is held for one read and write.
func lockBoard(dir string) (unlock func(), err error) {
	f, err := os.OpenFile(filepath.Join(dir, boardLock), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	end, wait := time.Now().Add(30*time.Second), time.Millisecond
	for {
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			return func() { f.Close() }, nil
		}
		if time.Now().After(end) {
			f.Close()
			return nil, fmt.Errorf("%w: %s held for 30s: try again", ticket.ErrBusy, boardLock)
		}
		time.Sleep(wait)
		wait = min(2*wait, 20*time.Millisecond)
	}
}

// writeWhole replaces p with b through a temporary file in its folder.
func writeWhole(p string, b []byte) error {
	tmp := p + ".tmp" + newSessionID()[:8]
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func readBoard(dir string) (board, bool, error) {
	var b board
	raw, err := os.ReadFile(filepath.Join(dir, boardFile))
	if errors.Is(err, fs.ErrNotExist) {
		return b, false, nil
	}
	if err != nil {
		return b, false, err
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, false, fmt.Errorf("%s: %v", boardFile, err)
	}
	return b, true, nil
}

func (c *call) writeBoard(dir string, b board) error {
	b.SetBy, b.At = c.actor, c.now().UTC().Format(time.RFC3339)
	raw, _ := json.Marshal(b)
	return writeWhole(filepath.Join(dir, boardFile), append(raw, '\n'))
}

// rulesHead is rules.md's first line, which code writes and strips: who
// may still seed the file, look or rules.
var rulesHead = regexp.MustCompile(`^# via: (look|rules)$`)

// setterTag is the setter a rule line ends with; code owns it.
var setterTag = regexp.MustCompile(`\s*\(set by [^()]*\)\s*$`)

// readRules reads rules.md: who seeds it and its rule lines.
func readRules(dir string) (via string, lines []string, ok bool, err error) {
	raw, err := os.ReadFile(filepath.Join(dir, rulesFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	for i, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if m := rulesHead.FindStringSubmatch(l); i == 0 && m != nil {
			via = m[1]
		} else if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return via, lines, true, nil
}

// tagRules gives each line its setter: a line the file already holds,
// tags aside, keeps its own; every other line gets this actor's, today,
// whatever tag it came with, so no writer picks whose rule it is.
func (c *call) tagRules(in, old []string) []string {
	kept := map[string]string{}
	for _, l := range old {
		kept[setterTag.ReplaceAllString(l, "")] = l
	}
	who := strings.NewReplacer("(", "", ")", "").Replace(c.actor)
	tag := fmt.Sprintf(" (set by %s, %s)", who, c.now().UTC().Format("2006-01-02"))
	out := make([]string, 0, len(in))
	for _, l := range in {
		l = strings.TrimSpace(setterTag.ReplaceAllString(l, ""))
		if l == "" {
			continue
		}
		if o, ok := kept[l]; ok {
			out = append(out, o)
		} else {
			out = append(out, l+tag)
		}
	}
	return out
}

func writeRules(dir, via string, lines []string) error {
	text := "# via: " + via + "\n"
	for _, l := range lines {
		text += l + "\n"
	}
	return writeWhole(filepath.Join(dir, rulesFile), []byte(text))
}

// logRules appends one change to rules.log; the caller holds the lock.
// note, when there is one, ends the line: what was added or dropped, and
// why, so a retro reads why each rule came and went, not just hashes.
func (c *call) logRules(dir, key, old, new, note string) error {
	f, err := os.OpenFile(filepath.Join(dir, rulesLog), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if note != "" {
		note = " " + note
	}
	_, err = fmt.Fprintf(f, "%s %s %s %s -> %s%s\n", c.now().UTC().Format(time.RFC3339), c.actor, key, old, new, note)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func linesSum(lines []string) string {
	if len(lines) == 0 {
		return "none"
	}
	s := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return fmt.Sprintf("%x", s[:6])
}

func intText(p *int) string {
	if p == nil {
		return "none"
	}
	return strconv.Itoa(*p)
}

// standingDir is the folder beside the store that holds the standing
// files.
func (c *call) standingDir() (string, error) {
	p, err := c.besideStore(boardFile)
	return filepath.Dir(p), err
}

// boardQASlots is QA runs at once as rules set qa_slots set it, or 0.
func (c *call) boardQASlots() int {
	dir, err := c.standingDir()
	if err != nil {
		return 0
	}
	if b, _, _ := readBoard(dir); b.QASlots != nil {
		return *b.QASlots
	}
	return 0
}

// standingRules is rules.md's lines, as a plan, rule or judge prompt
// carries them, and whether the file is there.
func (c *call) standingRules() (string, bool) {
	dir, err := c.standingDir()
	if err != nil {
		return "", false
	}
	_, lines, ok, err := readRules(dir)
	return strings.Join(lines, "\n"), ok && err == nil
}

// lookLimit is the build limit a look hands out by. The planner's
// --limit, from its old block, seeds board.json until rules set limit
// takes over; anyone else's is a one-off that writes nothing.
func (c *call) lookLimit(flag int) (int, []string, error) {
	dir, err := c.standingDir()
	if err != nil {
		return 0, nil, err
	}
	if flag >= 0 {
		if err := c.refuseRulesWrite("limit"); err != nil {
			return 0, nil, err
		}
		if c.actor != "planner" {
			b, _, err := readBoard(dir)
			if err != nil {
				return 0, nil, err
			}
			return flag, []string{fmt.Sprintf("--limit %d used once; standing limit %s", flag, intText(b.Limit))}, nil
		}
		unlock, err := lockBoard(dir)
		if err != nil {
			return 0, nil, err
		}
		defer unlock()
		b, _, err := readBoard(dir)
		if err != nil {
			return 0, nil, err
		}
		if b.Limit != nil && b.LimitVia == "rules" {
			return *b.Limit, []string{fmt.Sprintf("--limit %d ignored: limit %d set by %s with rules; drop --limit", flag, *b.Limit, b.SetBy)}, nil
		}
		if b.Limit == nil || *b.Limit != flag {
			old := intText(b.Limit)
			b.Limit, b.LimitVia = &flag, "look"
			if err := c.writeBoard(dir, b); err != nil {
				return 0, nil, err
			}
			if err := c.logRules(dir, "limit", old, strconv.Itoa(flag), ""); err != nil {
				return 0, nil, err
			}
		}
		return flag, nil, nil
	}
	b, _, err := readBoard(dir)
	if err != nil {
		return 0, nil, err
	}
	if b.Limit == nil {
		return 0, nil, usagef("look: no limit: run MAESTRO_TICKET_ACTOR=planner maestro-ticket rules set limit <n>")
	}
	return *b.Limit, nil, nil
}

// seedRules writes a look's judge stdin, the planner's old block's
// memories, to rules.md until rules text takes over. A line naming
// --limit is the runs-at-once memory: board.json holds that number.
func (c *call) seedRules(stdin string) ([]string, error) {
	if strings.TrimSpace(stdin) == "" {
		return nil, nil
	}
	if err := c.refuseRulesWrite("text"); err != nil {
		return nil, err
	}
	dir, err := c.standingDir()
	if err != nil {
		return nil, err
	}
	unlock, err := lockBoard(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	via, old, ok, err := readRules(dir)
	if err != nil {
		return nil, err
	}
	if via == "rules" {
		return []string{"judge stdin ignored: rules set with rules text; drop the heredoc"}, nil
	}
	var in, alerts []string
	for _, l := range strings.Split(stdin, "\n") {
		if strings.Contains(l, "--limit") {
			alerts = []string{"dropped the runs-at-once memory line: the limit lives in rules"}
			continue
		}
		in = append(in, l)
	}
	if err := checkRuleLines(in); err != nil {
		return nil, err
	}
	lines := c.tagRules(in, old)
	if ok && slices.Equal(lines, old) {
		return alerts, nil
	}
	if err := writeRules(dir, "look", lines); err != nil {
		return nil, err
	}
	return alerts, c.logRules(dir, "text", linesSum(old), linesSum(lines), "")
}

func checkRuleLines(lines []string) error {
	for _, l := range lines {
		if ticket.HasControl(l) {
			return usagef("rules: a rule must be one line with no control characters")
		}
		if rulesHead.MatchString(strings.TrimSpace(l)) {
			return usagef("rules: %q is the line code writes; leave it out", strings.TrimSpace(l))
		}
	}
	return nil
}

// The rule budget: every look, plan, rule and judge session reads all of
// rules.md, so past either a write warns.
const (
	ruleBudget      = 20
	ruleBytesBudget = 8 << 10
)

// liftsClause is what a new rule must say lifts it: "Lifts when ..." or
// "Lifts: ...", "never" included. A rule that names no lift stays for
// good, and the list only grows.
var liftsClause = regexp.MustCompile(`(?i)\blifts(\s+when\b|\s*:)`)

// runRules shows or sets the standing values: show (the default), set
// limit|qa_slots <n>, text (the whole list on stdin), add (lines on
// stdin) and drop <n>.
func runRules(ctx context.Context, c *call) error {
	whyFlag := c.fs.String("why", "", "why the change, kept beside it in rules.log (drop needs it)")
	if err := c.parse(); err != nil {
		return err
	}
	why := oneLineText(*whyFlag)
	if ticket.HasControl(why) {
		return usagef("rules: --why must be one line with no control characters")
	}
	verb := "show"
	if len(c.args) > 0 {
		verb = c.args[0]
	}
	switch verb {
	case "show":
		if len(c.args) > 1 {
			return usagef("rules show: unexpected argument %q", c.args[1])
		}
		return c.showRules()
	case "set":
		if len(c.args) > 1 && c.args[1] == "floor" {
			return c.setFloor(c.args[2:], why)
		}
		if len(c.args) != 3 || (c.args[1] != "limit" && c.args[1] != "qa_slots") {
			return usagef("rules set: want limit <n>, qa_slots <n>, floor inodes <pct> or floor load <n>")
		}
		n, err := strconv.Atoi(c.args[2])
		if err != nil || n < 0 || (n == 0 && c.args[1] == "qa_slots") {
			return usagef("rules set %s: want a whole number, 0 or more for limit, 1 or more for qa_slots", c.args[1])
		}
		if err := c.refuseRulesWrite(c.args[1]); err != nil {
			return err
		}
		return c.setBoard(c.args[1], n, why)
	case "text", "add", "drop":
		want := 1
		if verb == "drop" {
			want = 2
		}
		if len(c.args) != want {
			return usagef("rules %s: want %s", verb, map[string]string{"text": "the whole list on stdin, one rule a line", "add": "the new rules on stdin, one a line", "drop": "the number rules show gives the rule"}[verb])
		}
		if err := c.refuseRulesWrite("text"); err != nil {
			return err
		}
		if verb == "drop" && why == "" {
			return usagef("rules drop: say why with --why, such as --why \"lifted: lint.go refuses it since T-412\"; rules.log keeps it")
		}
		return c.editRules(verb, why)
	}
	return usagef("rules: unknown %q: want show, set, text, add or drop", verb)
}

func (c *call) setBoard(key string, n int, why string) error {
	dir, err := c.standingDir()
	if err != nil {
		return err
	}
	unlock, err := lockBoard(dir)
	if err != nil {
		return err
	}
	defer unlock()
	b, _, err := readBoard(dir)
	if err != nil {
		return err
	}
	var old string
	if key == "limit" {
		old, b.Limit, b.LimitVia = intText(b.Limit), &n, "rules"
	} else {
		old, b.QASlots = intText(b.QASlots), &n
	}
	if err := c.writeBoard(dir, b); err != nil {
		return err
	}
	if err := c.logRules(dir, key, old, strconv.Itoa(n), whyNote(why)); err != nil {
		return err
	}
	return c.out(map[string]any{"key": key, "value": n, "was": old}, func(w io.Writer) {
		fmt.Fprintf(w, "%s: %d (was %s)\n", key, n, old)
	})
}

func whyNote(why string) string {
	if why == "" {
		return ""
	}
	return "why: " + why
}

// ruleBudgetWarn says when a list is past the rule budget, or "".
func ruleBudgetWarn(lines []string) string {
	n := len(strings.Join(lines, "\n"))
	if len(lines) <= ruleBudget && n <= ruleBytesBudget {
		return ""
	}
	return fmt.Sprintf("%d rules, %.1f KB: past the budget of %d rules or %d KB that every look, plan and judge session reads; drop the rules that lifted, and ask job retro which to turn into code", len(lines), float64(n)/1024, ruleBudget, ruleBytesBudget>>10)
}

func (c *call) editRules(verb, why string) error {
	var in []string
	drop := 0
	if verb == "drop" {
		n, err := strconv.Atoi(c.args[1])
		if err != nil || n < 1 {
			return usagef("rules drop: want the number rules show gives the rule")
		}
		drop = n
	} else {
		b, err := c.readStdin()
		if err != nil {
			return err
		}
		in = strings.Split(string(b), "\n")
		if err := checkRuleLines(in); err != nil {
			return err
		}
		for _, l := range in {
			if verb == "add" && strings.TrimSpace(l) != "" && !liftsClause.MatchString(l) {
				return usagef("rules add: %q says nothing lifts it: end it with \"Lifts when <what ends it>\", or \"Lifts when: never\" for a rule that stays", strings.TrimSpace(l))
			}
		}
	}
	dir, err := c.standingDir()
	if err != nil {
		return err
	}
	unlock, err := lockBoard(dir)
	if err != nil {
		return err
	}
	defer unlock()
	_, old, _, err := readRules(dir)
	if err != nil {
		return err
	}
	var lines []string
	var notes []string
	switch verb {
	case "text":
		lines = c.tagRules(in, old)
	case "add":
		added := c.tagRules(in, nil)
		lines = append(slices.Clone(old), added...)
		for _, l := range added {
			notes = append(notes, fmt.Sprintf("added: %q", l))
		}
	case "drop":
		if drop > len(old) {
			return fmt.Errorf("%w: rules drop %d: there are %d rules", ticket.ErrNotFound, drop, len(old))
		}
		lines = slices.Delete(slices.Clone(old), drop-1, drop)
		notes = append(notes, fmt.Sprintf("dropped: %q", old[drop-1]))
	}
	if why != "" {
		notes = append(notes, whyNote(why))
	}
	if err := writeRules(dir, "rules", lines); err != nil {
		return err
	}
	if err := c.logRules(dir, "text", linesSum(old), linesSum(lines), strings.Join(notes, " ")); err != nil {
		return err
	}
	warn := ruleBudgetWarn(lines)
	res := map[string]any{"rules": cmpNil(lines)}
	if warn != "" {
		res["warn"] = warn
	}
	return c.out(res, func(w io.Writer) {
		printRules(w, lines)
		if warn != "" {
			fmt.Fprintln(w, "warn: "+warn)
		}
	})
}

func cmpNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func printRules(w io.Writer, lines []string) {
	if len(lines) == 0 {
		fmt.Fprintln(w, "rules: none")
	}
	for i, l := range lines {
		fmt.Fprintf(w, "%d. %s\n", i+1, l)
	}
}

func (c *call) showRules() error {
	dir, err := c.standingDir()
	if err != nil {
		return err
	}
	b, _, err := readBoard(dir)
	if err != nil {
		return err
	}
	via, lines, ok, err := readRules(dir)
	if err != nil {
		return err
	}
	res := map[string]any{"limit": b.Limit, "limit_via": b.LimitVia, "qa_slots": b.QASlots, "set_by": b.SetBy, "at": b.At,
		"floor_inodes": b.FloorInodes, "floor_load": b.FloorLoad, "rules": cmpNil(lines), "rules_file": ok, "rules_via": via}
	return c.out(res, func(w io.Writer) {
		lim := intText(b.Limit)
		if b.Limit != nil {
			lim += " (via " + b.LimitVia + ")"
		}
		fmt.Fprintf(w, "limit: %s\nqa_slots: %s\nfloor: %s\n", lim, intText(b.QASlots), floorText(b))
		if b.SetBy != "" {
			fmt.Fprintf(w, "set by %s at %s\n", b.SetBy, b.At)
		}
		if !ok {
			fmt.Fprintln(w, "rules: no rules.md: set them with rules text")
			return
		}
		printRules(w, lines)
	})
}

// rulesCheck is doctor's warning for a standing file not set yet.
func (c *call) rulesCheck() ticket.Check {
	dir, err := c.standingDir()
	if err != nil {
		return ticket.Check{Name: "rules", OK: false, Detail: err.Error()}
	}
	var miss []string
	if b, _, err := readBoard(dir); err != nil {
		miss = append(miss, err.Error())
	} else if b.Limit == nil {
		miss = append(miss, "no limit in board.json: look needs --limit until rules set limit")
	}
	if _, _, ok, err := readRules(dir); err != nil {
		miss = append(miss, err.Error())
	} else if !ok {
		miss = append(miss, "no rules.md: a judge session starts only once rules text sets the standing rules")
	}
	if len(miss) > 0 {
		return ticket.Check{Name: "rules", OK: true, Warn: true, Detail: strings.Join(miss, "; ")}
	}
	return ticket.Check{Name: "rules", OK: true, Detail: "board.json and rules.md set"}
}
