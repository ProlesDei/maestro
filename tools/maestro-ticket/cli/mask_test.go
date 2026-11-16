package cli

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// update rewrites the pin goldens from what the code prints now:
// go test ./cli -run Pin -update. Review the diff before you commit it:
// a golden that changes is a byte a bot or a session reads that changed.
var update = flag.Bool("update", false, "rewrite the goldens in testdata/pins")

// duration is a time.Duration as short prints one: 2h, 1m30s, 400ms.
const duration = `\d[0-9.]*(?:h|ms|µs|m|s)[0-9.hmsµ]*`

// What differs from one test run to the next. Run ids and session ids
// become R-1, U-1 and so on in the order they first show, so a golden
// still pins which run is which.
var (
	maskRunID = regexp.MustCompile(`\bR-[a-z2-7]{8}\b`)
	maskUUID  = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	maskTime  = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
	maskAgo   = regexp.MustCompile(`\b` + duration + `\s+ago\b`)
	// An elapsed time after "after", "for" or in a running list's
	// "(T-1, 5s)": the clock moves between a run's end and the read.
	maskElapsed = regexp.MustCompile(`\b(after|for) ` + duration)
	maskRunning = regexp.MustCompile(`(\(\S+, )` + duration + `\)`)
	// A live run's age in a refusal: "2h54m into its 2h limit".
	maskInto = regexp.MustCompile(`\b` + duration + ` into its\b`)
	maskSHA  = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	maskPID  = regexp.MustCompile(`"pid": \d+`)
	maskHost = regexp.MustCompile(`"host": "[^"]*"`)
)

// fixedIDs are run ids the code and the tests write as they are: the
// example in a usage line, and a run that doesn't exist.
var fixedIDs = map[string]bool{"R-abcdefgh": true, "R-zzzzzzzz": true}

// masker masks one golden's text. Paths come first, longest first, so a
// temp dir inside another masks as the inner one.
type masker struct {
	paths [][2]string // path, label
	ids   map[string]string
	uuids map[string]string
	shas  map[string]string
}

func newMasker(paths ...string) *masker {
	m := &masker{ids: map[string]string{}, uuids: map[string]string{}, shas: map[string]string{}}
	for i := 0; i+1 < len(paths); i += 2 {
		m.path(paths[i], paths[i+1])
	}
	return m
}

// path masks p, and the same path with its links read, as label.
func (m *masker) path(p, label string) {
	if p == "" {
		return
	}
	m.paths = append(m.paths, [2]string{p, label})
	if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
		m.paths = append(m.paths, [2]string{r, label})
	}
}

func number(seen map[string]string, prefix string) func(string) string {
	return func(s string) string {
		if v, ok := seen[s]; ok {
			return v
		}
		v := prefix + strconv.Itoa(len(seen)+1)
		seen[s] = v
		return v
	}
}

func (m *masker) mask(s string) string {
	ps := append([][2]string{}, m.paths...)
	for i := range ps {
		for j := i + 1; j < len(ps); j++ {
			if len(ps[j][0]) > len(ps[i][0]) {
				ps[i], ps[j] = ps[j], ps[i]
			}
		}
	}
	for _, p := range ps {
		s = strings.ReplaceAll(s, p[0], p[1])
	}
	runID := number(m.ids, "R-")
	s = maskRunID.ReplaceAllStringFunc(s, func(id string) string {
		if fixedIDs[id] {
			return id
		}
		return runID(id)
	})
	s = maskUUID.ReplaceAllStringFunc(s, number(m.uuids, "U-"))
	s = maskSHA.ReplaceAllStringFunc(s, number(m.shas, "SHA-"))
	s = maskTime.ReplaceAllString(s, "T")
	s = maskAgo.ReplaceAllString(s, "D ago")
	s = maskElapsed.ReplaceAllString(s, "$1 D")
	s = maskRunning.ReplaceAllString(s, "${1}D)")
	s = maskInto.ReplaceAllString(s, "D into its")
	s = maskPID.ReplaceAllString(s, `"pid": N`)
	s = maskHost.ReplaceAllString(s, `"host": "<host>"`)
	return s
}

// golden compares got with testdata/pins/<name>.golden, or rewrites the
// file under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", "pins", name+".golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v: run with -update to write it", err)
	}
	if got != string(want) {
		t.Errorf("%s changed: a byte a bot or a session reads moved.\n--- want\n%s\n--- got\n%s", p, want, got)
	}
}

// result is one command's exit code and output, as a golden holds it.
func result(args []string, stdin string, code int, stdout, stderr string) string {
	var b strings.Builder
	b.WriteString("$ maestro-ticket " + strings.Join(args, " ") + "\n")
	if stdin != "" {
		b.WriteString("stdin: " + strconv.Quote(stdin) + "\n")
	}
	b.WriteString("exit " + strconv.Itoa(code) + "\n")
	if stdout != "" {
		b.WriteString("stdout:\n" + stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteString("\\ no newline\n")
		}
	}
	if stderr != "" {
		b.WriteString("stderr:\n" + stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteString("\\ no newline\n")
		}
	}
	return b.String()
}

func TestMask(t *testing.T) {
	m := newMasker("/tmp/x", "<DIR>")
	got := m.mask(`R-abcdefgk R-zzzzzzz2 R-abcdefgk R-abcdefgh /tmp/x/runs 2026-11-15T10:00:00.123Z 5s ago after 1m30s (T-1, 2h54m) for 2 rows "pid": 42 "host": "box" 0e1d2c3b-1111-4222-8333-444455556666`)
	want := `R-1 R-2 R-1 R-abcdefgh <DIR>/runs T D ago after D (T-1, D) for 2 rows "pid": N "host": "<host>" U-1`
	if got != want {
		t.Fatalf("mask:\n%s\nwant:\n%s", got, want)
	}
}
