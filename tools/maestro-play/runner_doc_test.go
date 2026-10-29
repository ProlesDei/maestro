package maestroplay_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

// TestRunnerDocMatchesRequests keeps playbook.Requests in step with the
// request table and the reply fields table in docs/runner.md, which lint
// checks every run: step against.
func TestRunnerDocMatchesRequests(t *testing.T) {
	data, err := os.ReadFile("../../docs/runner.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	requests := section(t, doc, "## Requests")
	fields := section(t, doc, "### Reply fields")
	code := regexp.MustCompile("`([^`]+)`")
	paren := regexp.MustCompile(`\([^)]*\)`)

	// Every request the engine knows starts a backticked span in the first
	// column of the request table, and every such span is a request.
	var named []string
	for _, row := range rows(requests) {
		for _, m := range code.FindAllStringSubmatch(row[0], -1) {
			named = append(named, m[1])
			if playbook.FindRequest(strings.Fields(m[1])) == nil {
				t.Errorf("docs/runner.md names %q, which playbook.Requests lacks", m[1])
			}
		}
	}
	for _, r := range playbook.Requests {
		found := false
		for _, n := range named {
			if n == r.Name || strings.HasPrefix(n, r.Name+" ") {
				found = true
			}
		}
		if !found {
			t.Errorf("playbook.Requests has %q, which the docs/runner.md request table lacks", r.Name)
		}
	}

	// The reply fields table lists each request's fields, no more, no less.
	documented := map[string]bool{}
	for _, row := range rows(fields) {
		var names []string
		for _, m := range code.FindAllStringSubmatch(row[0], -1) {
			names = append(names, m[1])
		}
		got := map[string]bool{}
		for _, m := range code.FindAllStringSubmatch(paren.ReplaceAllString(row[1], ""), -1) {
			if !strings.HasPrefix(m[1], "-") {
				got[m[1]] = true
			}
		}
		want := map[string]bool{}
		for _, n := range names {
			r := playbook.FindRequest(strings.Fields(n))
			if r == nil || r.Name != n {
				t.Errorf("reply fields: %q isn't a request", n)
				continue
			}
			documented[n] = true
			for _, f := range r.Fields {
				want[f] = true
			}
		}
		for f := range want {
			if !got[f] {
				t.Errorf("reply fields for %v: docs/runner.md lacks %q", names, f)
			}
		}
		for f := range got {
			if !want[f] {
				t.Errorf("reply fields for %v: playbook.Requests lacks %q", names, f)
			}
		}
	}
	for _, r := range playbook.Requests {
		if len(r.Fields) > 0 && !r.Engine && !documented[r.Name] {
			t.Errorf("%q has reply fields that docs/runner.md doesn't list", r.Name)
		}
	}
}

// section is the text from heading to the next heading.
func section(t *testing.T, doc, heading string) string {
	t.Helper()
	i := strings.Index(doc, "\n"+heading+"\n")
	if i < 0 {
		t.Fatalf("docs/runner.md has no %q", heading)
	}
	rest := doc[i+len(heading)+2:]
	if j := strings.Index(rest, "\n#"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// rows are a Markdown table's body rows, split into cells.
func rows(text string) [][]string {
	var out [][]string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "|---") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), " | ")
		if len(cells) < 2 || cells[0] == " Request" || strings.TrimSpace(cells[0]) == "Request" {
			continue
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		out = append(out, cells)
	}
	return out
}
