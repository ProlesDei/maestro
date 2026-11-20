package mongostore

import (
	"context"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCriteriaChangeAndMerge(t *testing.T) {
	ctx := context.Background()
	s := coordStore(t, Options{})
	s.boardID = "scratch"
	markers := testDB(t, s).Collection("goal_revisions")
	if _, err := markers.InsertOne(ctx, bson.M{"_id": "export", "revision": 1}); err != nil {
		t.Fatal(err)
	}
	makeTicket := func(title string) ticket.ID {
		t.Helper()
		row, err := s.Create(ctx, ticket.CreateRequest{Goal: "export", Title: title, BriefRevision: 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: row.ID, To: ticket.StatusInProgress, Actor: "planner"}); err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	csv, excel := makeTicket("CSV"), makeTicket("Excel")
	claim := func(id ticket.ID, role string) ticket.Claim {
		t.Helper()
		row, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: id, Holder: role + "/trial"})
		if err != nil {
			t.Fatal(err)
		}
		return *row.Claim
	}
	change := func(r CriteriaRequest, key string) ticket.Criteria {
		t.Helper()
		c, err := s.ChangeCriteria(ctx, r, key)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	head := strings.Repeat("a", 40)
	eng := claim(csv, "junior-engineer")
	proof := CriteriaRequest{ID: csv, AC: 1, Brief: 1, Actor: "junior-engineer", Verb: "build", Head: head, Holder: eng.Holder, Token: eng.Token}
	change(proof, "build")
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: csv, To: ticket.StatusInReview, Actor: proof.Actor, Holder: eng.Holder, Token: eng.Token, Release: true}); err != nil {
		t.Fatal(err)
	}
	rev := claim(csv, "reviewer")
	proof.Verb, proof.Actor, proof.Holder, proof.Token = "review", "reviewer", rev.Holder, rev.Token
	change(proof, "review")
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: csv, Holder: rev.Holder, Token: rev.Token, Actor: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	qa := claim(csv, "qa-lead")
	proof.Verb, proof.Actor, proof.Holder, proof.Token = "qa", "qa-lead", qa.Holder, qa.Token
	change(proof, "qa")
	if _, err := markers.UpdateOne(ctx, bson.M{"_id": "export"}, bson.M{"$set": bson.M{"revision": 2}}); err != nil {
		t.Fatal(err)
	}
	proof.Verb = "merge"
	if _, err := s.ChangeCriteria(ctx, proof, "old"); err == nil {
		t.Fatal("stale brief authorized a merge")
	}
	change(CriteriaRequest{ID: csv, AC: 1, Brief: 2, Verb: "assess", Actor: "planner", Disposition: "fits", Evidence: "CSV remains in scope"}, "csv-cleared")
	proof.Brief = 2
	change(proof, "merge")
	if res, err := testDB(t, s).Collection("goal_merges").UpdateOne(ctx, bson.M{"_id": "export", "merging": bson.M{"$in": bson.A{nil, 0}}}, bson.M{"$set": bson.M{"revision": 3}}); err != nil || res.MatchedCount != 0 {
		t.Fatal("publication crossed a merge", err)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: csv, To: ticket.StatusDone, Actor: "qa-lead", ActorRole: "qa-lead", Holder: qa.Holder, Token: qa.Token, Note: "Merged at " + head}); err != nil {
		t.Fatal(err)
	}
	cl := claim(excel, "senior-engineer")
	req := CriteriaRequest{Verb: "request", ID: excel, Actor: "senior-engineer", Holder: cl.Holder, Token: cl.Token, AC: 1, Brief: 1, Request: "access", Criterion: "owner only vs team access", Evidence: "two ACs conflict", Proposal: "remove Excel", Checkpoint: "saved-branch"}
	change(req, "request-access")
	change(req, "request-access")
	change(CriteriaRequest{ID: excel, AC: 1, Brief: 2, Verb: "assess", Actor: "planner", Request: "access", Disposition: "changed", Evidence: "CSV covers the release", Body: "CSV menu only"}, "resolved")
	got, err := s.Get(ctx, excel)
	if err != nil || got.Status != ticket.StatusInProgress || got.Tries != 0 || got.Criteria.AC != 2 || got.Criteria.Checkpoint != "saved-branch" {
		t.Fatalf("resolved: %+v %v", got, err)
	}
	proof.ID, proof.Verb = excel, "review"
	if _, err := s.ChangeCriteria(ctx, proof, "stale"); err == nil {
		t.Fatal("old AC certified new criteria at same head")
	}
}
