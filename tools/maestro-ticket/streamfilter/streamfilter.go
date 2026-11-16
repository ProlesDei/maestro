// Package streamfilter reads what claude -p --output-format stream-json
// --verbose printed for one run, once the run is over, and keeps two
// things: the bytes a run's .out held before stream-json (text before the
// first JSON line, then the last whole result line), and a Summary of
// counts and step codes from fixed lists. It keeps no prompt, reply, tool
// input or output, path or name: a field it reads to pick a code is
// dropped once the code is taken, and a line it can't place is counted,
// never kept. It imports no other package of this module, and touches no
// file, network or process: the caller hands it a reader.
package streamfilter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Caps. A result line may be as long as runs.OutputWindow, as today; any
// other line past MaxLine is skipped and counted.
const (
	MaxLine   = 1 << 20
	MaxResult = 8 << 20
	// MaxPre caps the text kept from before the first JSON line, such as
	// a crash message: its last whole lines.
	MaxPre = 64 << 10
	// TailLen is how many of the last tool calls a Summary keeps.
	TailLen = 24
)

// Bounds on what one scan holds in memory, so a hostile or broken stream
// can't grow it without end.
const (
	maxOpen     = 4096
	maxParents  = 4096
	maxMessages = 1 << 16
)

// Names are the lists Scan takes from its caller.
type Names struct {
	// Sub are maestro-ticket's subcommands: a Bash call of one is
	// mt:<sub>.
	Sub []string
	// Model maps a model id to its family on the caller's list; nil maps
	// every id to other.
	Model func(id string) string
}

// Step is one step code's calls in a run: how many, how many failed, and
// their time from the call to its result.
type Step struct {
	Code         string
	Calls, Fails int64
	MsSum, MsMax int64
}

// Call is one of the last tool calls: its code, and whether its result
// was an error.
type Call struct {
	Code string
	Fail bool
}

// Usage is the tokens a run's turns on one model family used, from each
// turn's own usage: the cost of a run that gave no result.
type Usage struct {
	Model                                string
	Input, Output, CacheRead, CacheWrite int64
}

// Summary is what a scan keeps: numbers, bools, a time, step codes from
// Codes, model families from Names.Model and Claude Code's version.
type Summary struct {
	// Lines counts whole JSON lines; Cut, lines past their cap and a
	// last line with no newline; Bad, lines after the first JSON line
	// that weren't JSON.
	Lines, Cut, Bad int64
	// Result says a whole result line was found and kept.
	Result bool
	Steps  []Step // by code
	Tail   []Call // the last TailLen calls, oldest first
	// OpenCode is the oldest call that had no result when the stream
	// ended, and OpenAt when it began: for a killed run, the call that
	// hung.
	OpenCode             string
	OpenAt               time.Time
	ToolCalls, ToolFails int64
	// CtxPeak is the most context one main-thread turn sent: its input,
	// cache read and cache write tokens.
	CtxPeak int64
	// Retries counts API retries, Compactions context compactions, and
	// Subagents the subagents whose turns the stream showed.
	Retries, Compactions, Subagents int64
	Usage                           []Usage // by model
	// Version is Claude Code's, from the init line, as digits and dots.
	Version string
}

// The decoders. Each names only the fields a code or count needs; every
// other field of a line, such as the init line's cwd, MCP servers, slash
// commands and plugins, a hook's stdout and stderr, a message's text or a
// tool's result, is never decoded.
type line struct {
	Type      string   `json:"type"`
	Subtype   string   `json:"subtype"`
	Version   string   `json:"claude_code_version"`
	Timestamp string   `json:"timestamp"`
	Parent    string   `json:"parent_tool_use_id"`
	Message   *message `json:"message"`
}

type message struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content blocks `json:"content"`
	Usage   *usage `json:"usage"`
}

type block struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
	Input     input  `json:"input"`
}

// input is the part of a tool call's input a step code needs.
type input struct {
	Command  string `json:"command"`
	FilePath string `json:"file_path"`
	Skill    string `json:"skill"`
}

type usage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

// blocks is a message's content when it is a list; a user message's
// content may be one string, which holds no call.
type blocks []block

func (b *blocks) UnmarshalJSON(data []byte) error {
	var v []block
	if json.Unmarshal(data, &v) == nil {
		*b = v
	}
	return nil
}

func (in *input) UnmarshalJSON(data []byte) error {
	type plain input
	var v plain
	if json.Unmarshal(data, &v) == nil {
		*in = input(v)
	}
	return nil
}

type pending struct {
	code string
	at   time.Time
	seq  int64
}

type scan struct {
	n        Names
	s        Summary
	seenJSON bool
	pre      []byte // text before the first JSON line
	result   []byte // the last whole result line
	steps    map[string]*Step
	tail     []Call
	calls    int64 // calls seen, so tail[i] is call calls-len(tail)+i
	open     map[string]pending
	parents  map[string]bool
	msgs     map[string]Usage // each message's usage, by id: a message comes as a line per block
	order    []string
	extra    map[string]*Usage // usage of messages past maxMessages, or with no id
}

// Scan reads a run's stream to its end and returns its Summary and the
// bytes its .out holds: text before the first JSON line, then the last
// whole result line. A line past its cap, a line cut off at the end and,
// after the first JSON line, a line that isn't JSON are counted and
// dropped.
func Scan(r io.Reader, n Names) (Summary, []byte) {
	f := &scan{n: n, steps: map[string]*Step{}, open: map[string]pending{}, parents: map[string]bool{},
		msgs: map[string]Usage{}, extra: map[string]*Usage{}}
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	over := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !over {
			if len(buf)+len(chunk) > MaxResult+1 {
				over, buf = true, buf[:0]
			} else {
				buf = append(buf, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil { // the end: what is left had no newline
			if over || len(buf) > 0 {
				f.s.Cut++
			}
			break
		}
		if over {
			f.s.Cut++
		} else {
			f.line(buf)
		}
		buf, over = buf[:0], false
	}
	return f.finish()
}

func (f *scan) line(raw []byte) {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 {
		return
	}
	if b[0] != '{' || !json.Valid(b) {
		if f.seenJSON {
			f.s.Bad++
			return
		}
		f.pre = append(f.pre, bytes.TrimRight(raw, "\r\n")...)
		f.pre = append(f.pre, '\n')
		if len(f.pre) > MaxPre {
			cut := f.pre[len(f.pre)-MaxPre:]
			if i := bytes.IndexByte(cut, '\n'); i >= 0 && i < len(cut)-1 {
				cut = cut[i+1:]
			}
			f.pre = slices.Clone(cut)
		}
		return
	}
	f.seenJSON = true
	var l line
	json.Unmarshal(b, &l) // a field of another type is left empty
	if l.Type == "result" {
		f.s.Lines++
		f.s.Result = true
		f.result = append(f.result[:0], b...)
		return
	}
	if len(b) > MaxLine {
		f.s.Cut++
		return
	}
	f.s.Lines++
	at, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
	switch l.Type {
	case "system":
		switch l.Subtype {
		case "init":
			if f.s.Version == "" && version.MatchString(l.Version) {
				f.s.Version = l.Version
			}
		case "api_retry":
			f.s.Retries++
		case "compact_boundary":
			f.s.Compactions++
		}
	case "assistant":
		if l.Message == nil {
			return
		}
		if l.Parent != "" && !f.parents[l.Parent] && len(f.parents) < maxParents {
			f.parents[l.Parent] = true
		}
		if u := l.Message.Usage; u != nil {
			if l.Parent == "" {
				f.s.CtxPeak = max(f.s.CtxPeak, u.Input+u.CacheRead+u.CacheWrite)
			}
			f.usage(l.Message, *u)
		}
		for _, c := range l.Message.Content {
			if c.Type == "tool_use" {
				f.call(c, at)
			}
		}
	case "user":
		if l.Message == nil {
			return
		}
		for _, c := range l.Message.Content {
			if c.Type == "tool_result" {
				f.done(c, at)
			}
		}
	}
}

// usage keeps a message's latest usage: Claude Code prints a message
// with several blocks as a line per block, each with the same usage.
func (f *scan) usage(m *message, u usage) {
	fam := Other
	if f.n.Model != nil && m.Model != "" {
		fam = f.n.Model(m.Model)
	}
	row := Usage{Model: fam, Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
	if _, ok := f.msgs[m.ID]; m.ID != "" && (ok || len(f.msgs) < maxMessages) {
		if !ok {
			f.order = append(f.order, m.ID)
		}
		f.msgs[m.ID] = row
		return
	}
	e := f.extra[fam]
	if e == nil {
		e = &Usage{Model: fam}
		f.extra[fam] = e
	}
	e.Input += row.Input
	e.Output += row.Output
	e.CacheRead += row.CacheRead
	e.CacheWrite += row.CacheWrite
}

func (f *scan) call(c block, at time.Time) {
	code := f.n.code(c)
	st := f.steps[code]
	if st == nil {
		st = &Step{Code: code}
		f.steps[code] = st
	}
	st.Calls++
	f.s.ToolCalls++
	if c.ID != "" && len(f.open) < maxOpen {
		f.open[c.ID] = pending{code: code, at: at, seq: f.calls}
	}
	f.calls++
	f.tail = append(f.tail, Call{Code: code})
	if len(f.tail) > TailLen {
		f.tail = slices.Clone(f.tail[len(f.tail)-TailLen:])
	}
}

func (f *scan) done(c block, at time.Time) {
	p, ok := f.open[c.ToolUseID]
	if !ok {
		return
	}
	delete(f.open, c.ToolUseID)
	st := f.steps[p.code]
	if !p.at.IsZero() && !at.IsZero() {
		if ms := at.Sub(p.at).Milliseconds(); ms >= 0 {
			st.MsSum += ms
			st.MsMax = max(st.MsMax, ms)
		}
	}
	if c.IsError {
		st.Fails++
		f.s.ToolFails++
		if i := p.seq - (f.calls - int64(len(f.tail))); i >= 0 {
			f.tail[i].Fail = true
		}
	}
}

func (f *scan) finish() (Summary, []byte) {
	s := f.s
	for _, st := range f.steps {
		s.Steps = append(s.Steps, *st)
	}
	slices.SortFunc(s.Steps, func(a, b Step) int { return strings.Compare(a.Code, b.Code) })
	s.Tail = f.tail
	var oldest *pending
	for _, p := range f.open {
		if oldest == nil || p.seq < oldest.seq {
			oldest = &p
		}
	}
	if oldest != nil {
		s.OpenCode, s.OpenAt = oldest.code, oldest.at
	}
	s.Subagents = int64(len(f.parents))
	sums := map[string]*Usage{}
	add := func(u Usage) {
		t := sums[u.Model]
		if t == nil {
			t = &Usage{Model: u.Model}
			sums[u.Model] = t
		}
		t.Input += u.Input
		t.Output += u.Output
		t.CacheRead += u.CacheRead
		t.CacheWrite += u.CacheWrite
	}
	for _, id := range f.order {
		add(f.msgs[id])
	}
	for _, e := range f.extra {
		add(*e)
	}
	for _, u := range sums {
		if u.Input+u.Output+u.CacheRead+u.CacheWrite > 0 {
			s.Usage = append(s.Usage, *u)
		}
	}
	slices.SortFunc(s.Usage, func(a, b Usage) int { return strings.Compare(a.Model, b.Model) })
	out := slices.Clone(f.pre)
	if f.result != nil {
		out = append(append(out, f.result...), '\n')
	}
	return s, out
}

var version = regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$`)
