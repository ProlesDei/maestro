package chief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var threadID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_%.-]{0,255}$`)

func (s *Service) decide(ctx context.Context, thread, verb string, r Request) (any, error) {
	if !identifier.MatchString(r.ID) {
		return nil, errors.New("decision needs a stable ID")
	}
	id := r.Goal + "/" + r.ID
	coll := s.db.Collection("goal_decisions")
	if verb == "propose" {
		if r.Text == "" {
			return nil, errors.New("decision needs evidence and the choice to settle")
		}
		filter := bson.M{"_id": id, "goal": r.Goal, "revision": r.Revision, "proposal": r.Text}
		if err := one(coll.UpdateOne(ctx, filter, bson.M{"$setOnInsert": bson.M{"state": "open", "generation": 0}}, options.UpdateOne().SetUpsert(true))); err != nil {
			return nil, err
		}
		return bson.M{"decision": id}, nil
	}
	var d decision
	if err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&d); err != nil {
		return nil, err
	}
	if d.Revision != r.Revision {
		return nil, conflict
	}
	if verb == "claim" {
		if d.Holder == thread {
			return bson.M{"generation": d.Generation, "state": d.State}, nil
		}
		if d.Generation != r.Generation || (d.Holder != "" && !r.Transfer) || d.State == "acknowledged" {
			return nil, conflict
		}
		return bson.M{"generation": d.Generation + 1}, one(coll.UpdateOne(ctx, bson.M{"_id": id, "generation": r.Generation}, bson.M{"$inc": bson.M{"generation": 1}, "$set": bson.M{"holder": thread}}))
	}
	if d.Holder != thread || d.Generation != r.Generation {
		return nil, conflict
	}
	filter := bson.M{"_id": id, "holder": thread, "generation": r.Generation, "state": d.State}
	update := bson.M{}
	switch verb {
	case "question":
		if r.Text == "" {
			return nil, errors.New("question needs exact wording")
		}
		if d.Question != "" {
			if d.Question != r.Text {
				return nil, conflict
			}
			return bson.M{"thread": d.Thread, "question": d.Question, "state": d.State}, nil
		}
		if d.State != "open" {
			return nil, conflict
		}
		update = bson.M{"state": "question", "question": r.Text, "thread": thread}
	case "answer":
		if !r.verified || r.Source == "" || r.origin != d.Thread || d.Question != r.question {
			return nil, errors.New("reply lacks a verified reference to the original question")
		}
		if d.Answer != "" {
			if d.Answer != r.Text || d.Source != r.Source {
				return nil, conflict
			}
			return bson.M{"answer": d.Answer, "source": d.Source}, nil
		}
		if d.State != "question" {
			return nil, conflict
		}
		update = bson.M{"state": "answered", "answer": r.Text, "source": r.Source}
	case "rule":
		if r.Text == "" || r.Rationale == "" {
			return nil, errors.New("ruling needs an instruction and reason")
		}
		key := id + "/ruling"
		if d.State == "ruled" || d.State == "acknowledged" {
			if d.Ruling != r.Text || d.Rationale != r.Rationale || d.Authority != r.Rule || d.RuleRevision != r.RuleRevision || d.RuleScope != r.RuleScope {
				return nil, conflict
			}
			return bson.M{"delivery": key}, nil
		}
		// Publication changes this same marker. A read-only snapshot would
		// allow a ruling and a conflicting publication both to commit.
		if err := one(s.db.Collection("goal_revisions").UpdateOne(ctx, bson.M{"_id": r.Goal, "revision": r.Revision}, bson.M{"$inc": bson.M{"rulings": 1}})); err != nil {
			return nil, err
		}
		switch {
		case r.Rule != "":
			if d.State != "open" || !identifier.MatchString(r.Rule) {
				return nil, conflict
			}
			scope := r.Goal
			if r.RuleScope != "" {
				if r.RuleScope != "all" && r.RuleScope != r.Goal {
					return nil, conflict
				}
				scope = r.RuleScope
			}
			f := bson.M{"goal": scope, "rule": r.Rule, "revision": r.RuleRevision, "current": true, "kind": "delegation", "$or": bson.A{bson.M{"expires": nil}, bson.M{"$expr": bson.M{"$gt": bson.A{"$expires", "$$NOW"}}}}}
			if err := one(s.db.Collection("owner_rules").UpdateOne(ctx, f, bson.M{"$inc": bson.M{"uses": 1}})); err != nil {
				return nil, err
			}
		case d.State != "answered" || d.Answer == "":
			return nil, errors.New("record an owner reply or cite a current delegation")
		}
		update = bson.M{"state": "ruled", "ruling": r.Text, "rationale": r.Rationale, "authority": r.Rule, "rule_revision": r.RuleRevision, "rule_scope": r.RuleScope, "delivery": bson.M{"id": key, "state": "pending"}}
		if err := s.publishDelivery(ctx, key, r.Goal, "plan", r.Text, r.Revision); err != nil {
			return nil, err
		}
	}
	return update, one(coll.UpdateOne(ctx, filter, bson.M{"$set": update}))
}

type Message struct{ Role, RunID, Text string }

// Parse only replies whose run follows the recorded question in this thread.
// A quiet or truncated transcript leaves the question pending.
func exactReply(messages []Message, question, id, source string) (string, error) {
	found, immediate := false, false
	for _, m := range messages {
		if m.Role == "assistant" && strings.Contains(m.Text, question) && strings.Contains(m.Text, id) {
			found, immediate = true, true
			continue
		}
		if !found {
			continue
		}
		if m.Role == "user" && m.RunID == source {
			tag, reply, tagged := strings.Cut(m.Text, "\n")
			if tagged && tag == "Owner reply to "+id+" (verbatim on the next line):" && reply != "" {
				return reply, nil
			}
			if immediate && m.Text != "" && m.Text != "ring: run pickup" {
				return m.Text, nil
			}
			break
		}
		immediate = false
	}
	return "", errors.New("original thread has no unambiguous owner reply at that run; decision stays open")
}

func (s *Service) Capture(ctx context.Context, base, thread string, r Request) (any, error) {
	var d decision
	if err := s.db.Collection("goal_decisions").FindOne(ctx, bson.M{"_id": r.Goal + "/" + r.ID, "holder": thread, "generation": r.Generation}).Decode(&d); err != nil {
		return nil, err
	}
	if !threadID.MatchString(d.Thread) || d.Question == "" || r.Source == "" {
		return nil, conflict
	}
	args := []string{"thread", "show", d.Thread, "--messages", "200", "--json"}
	if base != "" {
		args = append(args, "--base-dir", base)
	}
	output, err := exec.CommandContext(ctx, "t3", args...).Output()
	if err != nil {
		return nil, errors.New("original T3 history unavailable; decision stays open")
	}
	var history struct {
		Thread   struct{ ThreadID string }
		Messages []Message
	}
	if json.Unmarshal(output, &history) != nil || history.Thread.ThreadID != d.Thread {
		return nil, errors.New("T3 returned a different thread")
	}
	r.Text, err = exactReply(history.Messages, d.Question, r.ID, r.Source)
	if err != nil {
		return nil, err
	}
	r.verified, r.origin, r.question = true, d.Thread, d.Question
	return s.Apply(ctx, thread, "answer", r)
}

// Ack reads a worker's durable receipt on the same board. A caller cannot
// acknowledge a delivery by supplying a receipt of its own.
func (s *Service) ack(ctx context.Context, thread string, r Request) (any, error) {
	id := r.Source
	var receipt mongostore.PlannerDelivery
	err := s.db.Collection("planner_deliveries").FindOne(ctx, bson.M{"_id": id, "goal": r.Goal}).Decode(&receipt)
	if err != nil {
		return nil, err
	}
	if receipt.Receipt == "" || receipt.Receipt != receipt.Hash() {
		return nil, errors.New("planner has not accepted this delivery")
	}
	set := bson.M{"delivery.state": "acknowledged", "delivery.receipt": receipt.Receipt}
	filter := bson.M{"goal": r.Goal, "revision": receipt.Revision, "delivery.id": id}
	if r.ID != "" {
		filter["_id"], filter["holder"], filter["generation"] = r.Goal+"/"+r.ID, thread, r.Generation
		filter["ruling"] = receipt.Text
		set["state"] = "acknowledged"
		err = one(s.db.Collection("goal_decisions").UpdateOne(ctx, filter, bson.M{"$set": set}))
	} else {
		filter["text"] = receipt.Text
		err = one(s.db.Collection("goal_briefs").UpdateOne(ctx, filter, bson.M{"$set": set}))
	}
	return bson.M{"receipt": receipt.Receipt}, err
}

func DescribeError(err error) error {
	if err == nil {
		return nil
	}
	var server mongo.ServerError
	if errors.As(err, &server) {
		return fmt.Errorf("chief database operation failed; refresh the board and verify collection permissions")
	}
	return err
}
