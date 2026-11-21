package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/chief"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// cos runs the chief of staff's fixed verbs on the worker side of the
// board. Plan and ruling text comes from a delivery the chief published.
func runCos(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) != 1 {
		return usagef("cos: give one verb")
	}
	verb := c.args[0]
	switch verb {
	case "plan", "rule", "ask", "stop", "start", "rules", "pickup", "look", "goals", "eta", "ack":
	default:
		return usagef("cos: unknown verb")
	}
	var in struct {
		Goal     string `json:"goal"`
		Delivery string `json:"delivery"`
	}
	dec := json.NewDecoder(io.LimitReader(c.env.Stdin, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		return usagef("cos: invalid request")
	}
	if dec.Decode(new(any)) != io.EOF {
		return usagef("cos: expected one request")
	}
	// The chief's thread must hold the goal and the published delivery
	// the verb names before the board acts on it.
	s, err := func() (*chief.Service, error) {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		s, _, err := c.chiefService(cctx)
		return s, err
	}()
	if err != nil {
		if verb == "look" {
			return json.NewEncoder(c.env.Stdout).Encode(map[string]string{"store": "unavailable"})
		}
		return err
	}
	err = s.CheckCos(ctx, c.env.Getenv("T3CODE_THREAD_ID"), in.Goal, verb, in.Delivery)
	s.Close(ctx)
	if err != nil {
		return chief.DescribeError(err)
	}
	c.recoverAfterBoot()
	loc, err := c.location()
	if err != nil {
		return err
	}
	if loc.boardID == "" {
		return usagef("cos: select a T3 runtime board")
	}
	b, err := c.board()
	if err != nil {
		return err
	}
	var out any
	switch verb {
	case "look", "goals", "eta":
		if verb == "eta" {
			etas, e := b.ETAs(ctx)
			err = e
			if in.Goal != "" && in.Goal != "all" {
				filtered := etas[:0]
				for _, eta := range etas {
					if eta.Goal == in.Goal {
						filtered = append(filtered, eta)
					}
				}
				etas = filtered
			}
			out = etas
		} else {
			goal := in.Goal
			if goal == "all" {
				goal = ""
			}
			tickets, e := b.List(ctx, ticket.ListFilter{Goal: goal})
			if e != nil {
				return e
			}
			counts := map[string]map[ticket.Status]int{}
			for _, t := range tickets {
				if counts[t.Goal] == nil {
					counts[t.Goal] = map[ticket.Status]int{}
				}
				counts[t.Goal][t.Status]++
			}
			out = counts
			if verb == "look" {
				profiles, e := runtimeProfiles(loc.localDir)
				if e != nil {
					return e
				}
				out = map[string]any{"counts": counts, "profiles": profiles, "store": "available"}
			}
		}
	case "pickup":
		out, err = b.GoalItems(ctx, in.Goal)
	default:
		if in.Delivery == "" || in.Goal == "" || !strings.HasPrefix(in.Delivery, in.Goal+"/") {
			return usagef("cos: name the goal and its published delivery")
		}
		if verb == "ack" {
			var d mongostore.PlannerDelivery
			d, err = b.AcceptDelivery(ctx, in.Delivery, "ack")
			out = map[string]string{"id": d.ID, "receipt": d.Receipt, "state": d.State}
		} else {
			out, err = c.deliver(ctx, b, in.Delivery, verb)
		}
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(c.env.Stdout).Encode(out)
}

func (c *call) deliver(ctx context.Context, b *mongostore.Store, id, verb string) (mongostore.PlannerDelivery, error) {
	d, err := b.AcceptDelivery(ctx, id, verb)
	if err != nil {
		return d, err
	}
	if d.Run != "" || d.State == "completed" {
		return d, nil
	}
	if d.Job == "stop" || d.Job == "start" || d.Job == "rules" {
		if err = b.ApplyControl(ctx, d); err != nil {
			return d, err
		}
		if d.Job == "start" {
			loc, err := c.location()
			if err != nil {
				return d, err
			}
			if err := c.scheduleReconsider(loc.localDir, time.Now()); err != nil {
				return d, err
			}
		}
		return b.AcceptDelivery(ctx, id, verb)
	}
	if d.Job != "ask" && d.Job != "plan" {
		return d, errors.New("unsupported planner delivery")
	}
	dir, err := c.besideStore("runs")
	if err != nil {
		return d, err
	}
	notify := filepath.Join(filepath.Dir(dir), "notify")
	if !codeRole(notify, "planner") {
		return d, errors.New("accepted; planner is unserved")
	}
	co, err := c.coord()
	if err != nil {
		return d, err
	}
	action := "delivery-" + d.Hash()
	var run string
	m, err := co.s.read("receipts", action)
	switch {
	case err == nil:
		h, id, ok := strings.Cut(m.body, " ")
		if !ok || h != d.Hash() || !runs.ValidID(id) {
			return d, errors.New("planner handoff receipt conflict")
		}
		run = id
	case errors.Is(err, os.ErrNotExist):
		kind := "plan"
		if d.Job == "ask" {
			kind = "cos"
		}
		k, _ := findJob(kind)
		cc := *c
		cc.actor = "planner"
		prompt := fmt.Sprintf("Goal %s, brief revision %d, delivery %s.\n%s", d.Goal, d.Revision, d.ID, d.Text)
		r, _, _, err := cc.startJob(ctx, jobReq{briefRevision: d.Revision, kind: k, text: prompt, actionID: action, actionHash: d.Hash(), delivery: d.ID})
		if err != nil {
			var going alreadyGoing
			if !errors.As(err, &going) {
				return d, err
			}
			r, err = runs.Get(dir, going.run, time.Now())
			if err != nil {
				return d, err
			}
		}
		if r.PID == 0 || r.ActionID != action || r.ActionHash != d.Hash() {
			return d, errors.New("planner launch awaits a verified receipt")
		}
		if err := saveActionReceipt(co, r); err != nil {
			return d, err
		}
		run = r.ID
	default:
		return d, err
	}
	if err := b.DeliveryStarted(ctx, d, run); err != nil {
		return d, err
	}
	return b.AcceptDelivery(ctx, d.ID, verb)
}
