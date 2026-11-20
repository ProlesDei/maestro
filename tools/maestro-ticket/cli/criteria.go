package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// The worker inherits the versions it started against. A new sample taken
// while posting a verdict could incorrectly certify changed criteria.
func (c *call) proofVersions(ac, brief int) (int, int, error) {
	for _, v := range []struct {
		name  string
		value *int
	}{{"MAESTRO_AC_REVISION", &ac}, {"MAESTRO_BRIEF_REVISION", &brief}} {
		if env := c.env.Getenv(v.name); env != "" {
			n, err := strconv.Atoi(env)
			if err != nil || (*v.value != 0 && *v.value != n) {
				return 0, 0, errors.New("proof differs from the worker's starting revision")
			}
			*v.value = n
		}
	}
	if ac <= 0 || brief <= 0 {
		return 0, 0, errors.New("supply the AC and brief revisions read before starting the work")
	}
	return ac, brief, nil
}

func runCriteria(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) != 2 {
		return usagef("criteria: give status, request, assess or merge and the ticket ID")
	}
	id, err := ticket.ParseID(c.args[1])
	if err != nil {
		return err
	}
	verb := c.args[0]
	if verb == "status" {
		d, err := c.detailForJob(ctx, id.String())
		if err != nil {
			return err
		}
		return json.NewEncoder(c.env.Stdout).Encode(d.Criteria)
	}
	if verb != "request" && verb != "assess" && verb != "merge" {
		return usagef("criteria: unknown operation")
	}
	if c.idemKey == "" {
		return usagef("criteria: supply an idempotency key for the action")
	}
	b, err := c.board()
	if err != nil {
		return err
	}
	var r mongostore.CriteriaRequest
	dec := json.NewDecoder(io.LimitReader(c.env.Stdin, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return usagef("criteria: invalid request")
	}
	if dec.Decode(new(any)) != io.EOF {
		return usagef("criteria: expected one request")
	}
	r.ID, r.Verb, r.Actor = id, verb, c.actor
	if verb != "assess" {
		r.AC, r.Brief, err = c.proofVersions(r.AC, r.Brief)
		if err != nil {
			return err
		}
	}
	state, err := b.ChangeCriteria(ctx, r, c.idemKey)
	if err != nil {
		return err
	}
	return json.NewEncoder(c.env.Stdout).Encode(state)
}

func (c *call) postProof(ctx context.Context, s ticket.Store, d ticket.Detail, k noteKind, note ticket.CommentRequest, ac, brief int) (ticket.Comment, error) {
	verb := map[string]string{"handoff": "build", "review-go": "review", "qa-pass": "qa", "review-kickback": "review-withdraw", "review-escalation": "review-withdraw", "qa-fail": "qa-withdraw", "qa-escalation": "qa-withdraw", "rereview": "rereview"}[k.name]
	if d.Criteria == nil || verb == "" {
		return s.Comment(ctx, note)
	}
	ac, brief, err := c.proofVersions(ac, brief)
	if err != nil {
		return ticket.Comment{}, err
	}
	b, ok := s.(*mongostore.Store)
	if !ok {
		return ticket.Comment{}, errors.New("versioned proof needs a runtime board")
	}
	return b.ProofNote(ctx, mongostore.CriteriaRequest{ID: d.ID, Verb: verb, Actor: c.actor, Head: shaIn(note.Body), Holder: note.Holder, Token: note.Token, AC: ac, Brief: brief}, note)
}

func setWorkerVersions(env []string, ac, brief int) []string {
	if ac > 0 {
		env = append(env, fmt.Sprintf("MAESTRO_AC_REVISION=%d", ac))
	}
	if brief > 0 {
		env = append(env, fmt.Sprintf("MAESTRO_BRIEF_REVISION=%d", brief))
	}
	return env
}
