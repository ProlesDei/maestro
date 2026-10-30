package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

// RenderOpts selects what render prints.
type RenderOpts struct {
	Ref    string // <role>/<job>
	Step   string // a step id, "done" for the note, or "escalate"
	Ticket string // read with maestro-ticket get
	// TicketFile is a get --json document to use instead of the store.
	TicketFile string
	// EnvFile is JSON laid over the run's .env, with .project and .limits
	// as its project and limits keys.
	EnvFile string
	Bot     string
	Input   string
}

// Render prints one step as the run would send it, without sending it or
// writing anything: a prompt step's full prompt, a ticket or run step's
// arguments, a gate's value. Steps that haven't run have empty records.
func (e *Engine) Render(o RenderOpts) (string, error) {
	p, err := playbook.Load(e.cfg.Playbooks, o.Ref)
	if err != nil {
		return "", err
	}
	env := e.environment()
	if o.EnvFile != "" {
		over := map[string]any{}
		if err := readJSON(o.EnvFile, &over); err != nil {
			return "", err
		}
		for _, k := range []string{"project", "limits"} {
			if m, ok := over[k].(map[string]any); ok {
				merge(env[k].(map[string]any), m)
				delete(over, k)
			}
		}
		merge(env, over)
	}
	bot := o.Bot
	if bot == "" {
		bot = "render"
	}
	st := e.newState(p, bot, o.Ticket, env)
	st.Input = o.Input
	switch {
	case o.TicketFile != "":
		var m map[string]any
		if err := readJSON(o.TicketFile, &m); err != nil {
			return "", err
		}
		st.TicketD = ticketData(m)
		if st.Ticket == "" {
			st.Ticket = str(m["id"])
		}
	case o.Ticket != "":
		if _, res := e.readTicket(st); res != nil {
			return "", fmt.Errorf("%s", res.Reason)
		}
	}
	switch o.Step {
	case "done":
		return renderTemplate("done.note", p.Done.Note, e.data(p, st, nil))
	case "escalate":
		return e.compose("_escalate", e.data(p, st, nil), str(env["harness"]))
	}
	s := p.Step(o.Step)
	if s == nil {
		return "", fmt.Errorf("%s has no step %q", p.Ref(), o.Step)
	}
	d := e.data(p, st, s)
	switch s.Kind() {
	case playbook.KindPrompt:
		return e.compose(s.Prompt, d, str(env["harness"]))
	case playbook.KindTicket:
		args, err := renderArgs(s.ID, s.Ticket, d)
		if err != nil {
			return "", err
		}
		if len(args) == 0 {
			return "", fmt.Errorf("step %s renders no arguments", s.ID)
		}
		if s.Body != "" {
			body, err := renderTemplate(s.ID+".body", s.Body, d)
			if err != nil {
				return "", err
			}
			if body = strings.TrimSpace(body); body != "" {
				flag := "--body"
				if args[0] == "transition" {
					flag = "--note"
				}
				args = append(args, flag, body)
			}
		}
		st.Visits[s.ID] = 1
		full, err := e.ticketArgs(st, args, key(st, s.ID))
		if err != nil {
			return "", err
		}
		return "maestro-ticket " + shellJoin(full) + "\n", nil
	case playbook.KindRun:
		args, err := renderArgs(s.ID, s.Run, d)
		if err != nil {
			return "", err
		}
		return shellJoin(args) + "\n", nil
	case playbook.KindGate:
		ok, err := renderBool(s.ID+".gate", s.Gate, d)
		return fmt.Sprintln(ok), err
	}
	return "", fmt.Errorf("step %s: no step type", s.ID)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// shellJoin quotes arguments that need it, for display.
func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = shellQuote(a)
	}
	return strings.Join(out, " ")
}
