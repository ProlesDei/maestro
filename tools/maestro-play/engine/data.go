package engine

import (
	"regexp"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

// The template data. Every name a template may use exists from the start,
// with a zero value, so a template can name a step that hasn't run yet and
// lint can tell a misspelled name from one that's just empty:
//
//	.ticket   the ticket as maestro-ticket get --json prints it, plus kind
//	          (the body's "Type:" line), latest (the latest comment's body)
//	          and trailers (every "Key: value" comment line, latest wins)
//	.project  project-setup.md: name, summary, main_branch, setup (its
//	          path) and checks ([{name, command}])
//	.env      harness (claude-code or codex) and family, the model family
//	          of the last session
//	.limits   step_visits and retries
//	.run      this run (id, date, branch, worktree, scratch, evidence_dir, ...)
//	.steps    one record per step id (zeroRecord)
//	.step     this step: id, mode, with (the rendered with: or vars:), vars
//	.item     the list item of an each: step, .index its position
//	.input    {text}: run --input
//	.cursor   the playbook's stored cursor (0 the first time)
//	.retry    nil, or {reason, n} on a retried prompt
//	.mode     the prompt step's mode:

func zeroTicket() map[string]any {
	return map[string]any{
		"id": "", "title": "", "body": "", "type": "", "status": "", "role": "", "priority": 0,
		"created_by": "", "owner": "", "tries": 0, "lease_expiries": 0, "depends_on": nil, "areas": nil,
		"blocked_from": "", "version": 0, "claim": nil, "created_at": "", "updated_at": "",
		"comments": nil, "kind": "", "latest": "", "trailers": map[string]any{},
	}
}

func defaultProject() map[string]any {
	return map[string]any{"main_branch": "main", "name": "", "summary": "", "setup": SetupPath, "checks": nil}
}

func defaultEnv() map[string]any {
	return map[string]any{"harness": "", "family": ""}
}

func defaultLimits() map[string]any {
	return map[string]any{"step_visits": 5, "retries": 1}
}

func zeroRun() map[string]any {
	return map[string]any{
		"id": "", "date": "", "playbook": "", "role": "", "bot": "", "holder": "", "token": 0, "session": "",
		"branch": "", "worktree": "", "scratch": "", "scratch_dir": "", "evidence_dir": "", "skipped": "",
		"stopped_at": "", "stop_reason": "",
	}
}

// zeroRecord is a step's record before it runs. Every step has every
// common field, so a template can read .outcome or .summary of any step.
func zeroRecord(s *playbook.Step) map[string]any {
	r := map[string]any{
		"status": "", "outcome": "", "visits": 0, "skipped": false,
		"exit_code": 0, "output": "", "output_path": "", "summary": "", "json": nil, "result": nil, "error": nil,
		"reply": map[string]any{}, "choice": "", "text": "", "question": "", "results": []any{},
		"value": false, "polls": 0, "session": "", "tier": "", "family": "",
	}
	switch s.Kind() {
	case playbook.KindRun:
		for _, line := range s.Run {
			if words, err := splitWords(line); err == nil {
				if req := playbook.FindRequest(plainWords(words)); req != nil {
					for _, f := range req.Fields {
						if _, ok := r[f]; !ok {
							r[f] = nil
						}
					}
				}
			}
		}
	case playbook.KindPrompt:
		r["reply"] = zeroReply(s.Out)
	}
	return r
}

func zeroReply(out []string) map[string]any {
	reply := map[string]any{}
	for _, f := range playbook.BaseReplyFields {
		reply[f] = nil
	}
	for _, f := range out {
		reply[f] = nil
	}
	return reply
}

// plainWords is the words before the first template action, enough to find
// the request a line names.
func plainWords(words []word) []string {
	var out []string
	for _, w := range words {
		if strings.Contains(w.text, "{{") {
			break
		}
		out = append(out, w.text)
	}
	return out
}

// zeroSteps is a record for every step of p.
func zeroSteps(p *playbook.Playbook) map[string]any {
	steps := map[string]any{}
	for i := range p.Steps {
		steps[p.Steps[i].ID] = zeroRecord(&p.Steps[i])
	}
	return steps
}

// zeroStep is .step for s, with its with: keys empty.
func zeroStep(s *playbook.Step) map[string]any {
	with := map[string]any{}
	if s != nil {
		for k := range s.Params() {
			with[k] = ""
		}
	}
	st := map[string]any{"id": "", "mode": "", "with": with, "vars": with}
	if s != nil {
		st["id"], st["mode"] = s.ID, s.Mode
	}
	return st
}

// merge copies src's keys over dst and returns dst.
func merge(dst, src map[string]any) map[string]any {
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

var trailerRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*):[ \t]*(\S.*)$`)

// trailers are latestLine for every key that a "Key: value" comment line
// names.
func trailers(t map[string]any) map[string]any {
	out := map[string]any{}
	cs := comments(t)
	for _, body := range cs {
		for _, line := range strings.Split(body, "\n") {
			if m := trailerRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil && out[m[1]] == nil {
				out[m[1]] = latestLine(cs, m[1])
			}
		}
	}
	return out
}

// lintData is the data lint renders against: every name, zero values.
func lintData(p *playbook.Playbook, s *playbook.Step) map[string]any {
	d := map[string]any{
		"ticket": zeroTicket(), "project": defaultProject(), "env": defaultEnv(), "limits": defaultLimits(),
		"run": zeroRun(), "steps": zeroSteps(p), "retry": nil, "mode": "", "step": zeroStep(s),
		"item": nil, "index": 0, "input": map[string]any{"text": ""}, "cursor": 0,
	}
	if s != nil {
		d["mode"] = s.Mode
	}
	return d
}

// lintSchema is lintData with .retry's fields, so {{if .retry}}{{.retry.reason}}
// checks out.
func lintSchema(p *playbook.Playbook, s *playbook.Step) map[string]any {
	d := lintData(p, s)
	d["retry"] = map[string]any{"reason": "", "n": 0}
	return d
}
