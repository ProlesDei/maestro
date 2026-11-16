package cli

import (
	"cmp"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
)

// Review facts for stats.db, from the reply a run's end already holds:
// the head its first line names and, for a review, the verdict, tier,
// mode and each finding's id, pass, severity, action and prior from its
// findings JSON (maestro-code-review's passes.md). Nothing else of the
// reply is decoded or kept: no sha, file, line, evidence, impact or fix.

// replyHeads are the first lines the shipped skills ask a reply to open
// with, "<word>: <status>", by word: the statuses each word may take.
var replyHeads = func() map[string][]string {
	qa := []string{"pass", "fail", "blocked", "refused", "agree", "disagree", "reproduced", "not reproduced", "done"}
	planner := []string{"done", "draft", "stopped", "refused"}
	m := map[string][]string{
		"review":      {"stopped", "refused"},
		"design":      {"ready", "blocked", "refused"},
		"infra block": {"disk full", "low_disk"},
	}
	for _, w := range []string{"live", "test", "second-look", "repro", "testable"} {
		m[w] = qa
	}
	for _, w := range []string{"rule", "finish", "check", "board", "judge"} {
		m[w] = planner
	}
	for _, w := range []string{"plan", "answer"} { // both a QA job and a planner one
		m[w] = append(slices.Clone(qa), "draft", "stopped")
	}
	return m
}()

// reviewVerdicts are a review report's Verdict: line values, longest
// first, so REPORT-ONLY GO isn't read as GO.
var reviewVerdicts = []struct{ text, head string }{
	{"REPORT-ONLY NO-GO", "review:report_only_nogo"},
	{"REPORT-ONLY GO", "review:report_only_go"},
	{"NO-GO", "review:nogo"},
	{"ESCALATE", "review:escalate"},
	{"GO", "review:go"},
}

func headName(word, status string) string {
	return strings.ReplaceAll(word, " ", "_") + ":" + strings.ReplaceAll(status, " ", "_")
}

// headNames is every head headCode can return but other.
func headNames() []string {
	var out []string
	for w, sts := range replyHeads {
		for _, st := range sts {
			out = append(out, headName(w, st))
		}
	}
	for _, v := range reviewVerdicts {
		out = append(out, v.head)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// cutWord reports whether s opens with w as a whole word: followed by
// its end, a space or punctuation, never a letter, digit or hyphen.
func cutWord(s, w string) bool {
	if !strings.HasPrefix(s, w) {
		return false
	}
	if len(s) == len(w) {
		return true
	}
	c := s[len(w)]
	return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_')
}

// headCode maps a reply's first line to a head on the list, by prefix:
// "" for no reply, other for a line off the list. A review report opens
// with its title, so its head comes from the Verdict: line under it.
func headCode(reply string) string {
	lines := strings.SplitN(strings.TrimLeft(lineBreaks.Replace(reply), " \t\n"), "\n", 12)
	for len(lines) > 1 && strings.HasPrefix(lines[0], "```") {
		lines = lines[1:] // a report in a fenced block
	}
	first := strings.TrimRight(lines[0], " \t")
	if first == "" {
		return ""
	}
	if strings.HasPrefix(first, "# Code review:") {
		for _, l := range lines[1:] {
			if v, ok := strings.CutPrefix(l, "Verdict: "); ok {
				for _, rv := range reviewVerdicts {
					if cutWord(v, rv.text) {
						return rv.head
					}
				}
				break
			}
		}
		return stats.Other
	}
	word, rest, ok := strings.Cut(first, ": ")
	if !ok {
		return stats.Other
	}
	sts := slices.Clone(replyHeads[word])
	// The longest first, so "not reproduced" isn't cut short.
	slices.SortFunc(sts, func(a, b string) int { return len(b) - len(a) })
	for _, st := range sts {
		if cutWord(rest, st) {
			return headName(word, st)
		}
	}
	return stats.Other
}

// reviewDoc is all stats decodes of a review's findings JSON. Every field
// is loose, so a field a session misspells or retypes costs only itself.
type reviewDoc struct {
	Verdict  looseStr      `json:"verdict"`
	Tier     looseNum      `json:"tier"`
	Mode     looseStr      `json:"mode"`
	Findings looseFindings `json:"findings"`
}

// reviewFinding is all stats decodes of one finding.
type reviewFinding struct {
	ID       looseStr `json:"id"`
	Severity looseStr `json:"severity"`
	Action   looseStr `json:"action"`
	Pass     looseNum `json:"pass"`
	Prior    looseStr `json:"prior"`
}

// looseFindings is a JSON array of findings, or none: its decode never
// fails, and skips an element that isn't an object.
type looseFindings []reviewFinding

// maxReviewFindings caps the findings one reply gives.
const maxReviewFindings = 200

func (f *looseFindings) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	for _, r := range raw[:min(len(raw), maxReviewFindings)] {
		var x reviewFinding
		if json.Unmarshal(r, &x) == nil {
			*f = append(*f, x)
		}
	}
	return nil
}

// docStart is how a findings JSON opens: a brace, then one of its keys.
var docStart = regexp.MustCompile(`^\{\s*"(head|verdict|tier|mode|findings)"\s*:`)

// reviewJSON finds the findings JSON in a reply: the last object that
// opens with one of its keys and decodes with a verdict. The report
// comes first and the JSON after, so it reads from the end.
func reviewJSON(reply string) (reviewDoc, bool) {
	for i, tries := len(reply), 0; tries < 8; {
		if i = strings.LastIndexByte(reply[:i], '{'); i < 0 {
			break
		}
		s := reply[i:]
		if !docStart.MatchString(s[:min(len(s), 80)]) {
			continue
		}
		tries++
		var d reviewDoc
		if json.NewDecoder(strings.NewReader(s)).Decode(&d) == nil && d.Verdict.V != "" {
			return d, true
		}
	}
	return reviewDoc{}, false
}

// listName is s when it is on names, other for any other text, "" for
// none.
func listName(s string, names ...string) string {
	switch {
	case s == "":
		return ""
	case slices.Contains(names, s):
		return s
	}
	return stats.Other
}

var (
	reviewVerdict = map[string]string{"GO": "go", "NO-GO": "nogo", "ESCALATE": "escalate"}
	// What a review's escalation says when its PR merged first (Phase A
	// step 4), and when a gate needs an outside step (working principles).
	mergedText  = regexp.MustCompile(`Escalation: review at [0-9a-f]{7,40}: PR \S+ merged as `)
	outsideText = regexp.MustCompile(`\boutside step: `)
	// The report's out-of-scope section.
	outOfScope = regexp.MustCompile(`^#{1,3} Out of scope\b`)
	tableRule  = regexp.MustCompile(`^\|[\s|:-]*$`)
)

// escalateCauses are the reasons escalateCause gives.
var escalateCauses = []string{"repeat", "outside_step", "merged", "approval"}

// escalateCause says why a review escalated, worked out from what it
// already wrote: its PR merged first, a gate needs an outside step,
// every blocker and major it found was raised before (repeat), or else
// it needs the owner's yes (approval).
func escalateCause(reply string, fs []stats.Finding) string {
	switch {
	case mergedText.MatchString(reply):
		return "merged"
	case outsideText.MatchString(reply):
		return "outside_step"
	}
	blocking, repeat := 0, true
	for _, f := range fs {
		if f.Kind == "finding" && (f.Sev == "blocker" || f.Sev == "major") {
			blocking++
			repeat = repeat && f.Prior == "still_present"
		}
	}
	if blocking > 0 && repeat {
		return "repeat"
	}
	return "approval"
}

// outOfScopeLines counts the items under the report's Out of scope
// heading: one per line, bar a table's header and rule, and none.
func outOfScopeLines(reply string) int64 {
	lines := strings.Split(lineBreaks.Replace(reply), "\n")
	var n int64
	for i := 0; i < len(lines); i++ {
		if !outOfScope.MatchString(lines[i]) {
			continue
		}
		for i++; i < len(lines); i++ {
			l := strings.TrimSpace(lines[i])
			if strings.HasPrefix(l, "#") || strings.HasPrefix(l, "```") {
				break
			}
			item := strings.TrimSpace(strings.TrimLeft(l, "-*+ "))
			switch {
			case item == "", strings.EqualFold(strings.TrimRight(item, "."), "none"), tableRule.MatchString(l):
			case strings.HasPrefix(l, "|") && i+1 < len(lines) && tableRule.MatchString(strings.TrimSpace(lines[i+1])):
			default:
				n++
			}
		}
		break
	}
	return n
}

// wholeIn is n as a whole number from lo to hi, or nil.
func wholeIn(n looseNum, lo, hi float64) *int64 {
	if !n.OK || n.V != math.Trunc(n.V) || n.V < lo || n.V > hi {
		return nil
	}
	return ptr(int64(n.V))
}

// reviewFacts sets the row's head from the reply and, on a review, its
// verdict, tier, mode, findings and the findings --drop named, and why it
// escalated when nothing else says why it ended.
func reviewFacts(row *stats.Run, job, reply string, drops []string) {
	row.Head = headCode(reply)
	if job != "review" {
		return
	}
	if d, ok := reviewJSON(reply); ok {
		row.Verdict = cmp.Or(reviewVerdict[d.Verdict.V], stats.Other)
		row.VTier = wholeIn(d.Tier, 0, 2)
		row.Mode = listName(d.Mode.V, "full", "incremental", "ruling")
		for _, f := range d.Findings {
			prior := f.Prior.V
			if prior == "still present" {
				prior = "still_present"
			}
			row.Findings = append(row.Findings, stats.Finding{Fid: f.ID.V, Kind: "finding", Pass: wholeIn(f.Pass, 1, 10),
				Sev:    listName(f.Severity.V, "blocker", "major", "minor", "nit"),
				Action: listName(f.Action.V, "auto-fix", "ask-user", "no-op"),
				Prior:  listName(prior, "new", "still_present"), N: 1})
		}
	}
	if n := outOfScopeLines(reply); n > 0 {
		row.Findings = append(row.Findings, stats.Finding{Kind: "out_of_scope", N: n})
	}
	for _, id := range drops {
		row.Findings = append(row.Findings, stats.Finding{Fid: id, Kind: "dropped", N: 1})
	}
	if row.Reason == "" && (row.Verdict == "escalate" || row.Head == "review:escalate") {
		row.Reason = escalateCause(reply, row.Findings)
	}
}
