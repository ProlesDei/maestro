package mongostore

import (
	"context"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDirectQAMergeNeedsCurrentQA(t *testing.T) {
	ctx := context.Background()
	s := coordStore(t, Options{})
	s.boardID = "scratch"
	if _, err := testDB(t, s).Collection("goal_revisions").InsertOne(ctx, bson.M{"_id": "copy", "revision": 1}); err != nil {
		t.Fatal(err)
	}
	row, err := s.Create(ctx, ticket.CreateRequest{Goal: "copy", Title: "Change label", BriefRevision: 1, DirectQA: true, Role: ticket.RoleEngineer, Actor: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: row.ID, To: ticket.StatusInProgress, Actor: "planner"}); err != nil {
		t.Fatal(err)
	}

	head := strings.Repeat("a", 40)
	claim := func(actor string) CriteriaRequest {
		t.Helper()
		cl, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: row.ID, Holder: actor + "/trial"})
		if err != nil {
			t.Fatal(err)
		}
		return CriteriaRequest{ID: row.ID, AC: 1, Brief: 1, Actor: actor, Holder: cl.Claim.Holder, Token: cl.Claim.Token, Head: head}
	}
	build := claim("junior-engineer")
	build.Verb = "build"
	if _, err := s.ChangeCriteria(ctx, build, "build"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: row.ID, To: ticket.StatusInReview, Actor: build.Actor, Holder: build.Holder, Token: build.Token, Release: true}); err != nil {
		t.Fatal(err)
	}
	qa := claim("qa-lead")
	qa.Verb = "merge"
	if _, err := s.ChangeCriteria(ctx, qa, "early"); err == nil {
		t.Fatal("reserved merge before independent QA")
	}
	qa.Verb = "qa"
	qa.Head = strings.Repeat("b", 40)
	if _, err := s.ChangeCriteria(ctx, qa, "wrong-head"); err == nil {
		t.Fatal("QA passed a different head from the handoff")
	}
	qa.Head = head
	if _, err := s.ChangeCriteria(ctx, qa, "passed"); err != nil {
		t.Fatal(err)
	}
	qa.Verb = "merge"
	stale := qa
	stale.Brief++
	if _, err := s.ChangeCriteria(ctx, stale, "different-brief"); err == nil {
		t.Fatal("direct QA reserved a merge for a different brief")
	}
	if _, err := s.ChangeCriteria(ctx, qa, "reserve"); err != nil {
		t.Fatal(err)
	}
	qa.Verb = "merge-rejected"
	if _, err := s.ChangeCriteria(ctx, qa, "release-reservation"); err != nil {
		t.Fatal(err)
	}
	qa.Verb = "rereview"
	if _, err := s.ChangeCriteria(ctx, qa, "scope-grew"); err != nil {
		t.Fatal(err)
	}
	qa.Verb = "merge"
	if _, err := s.ChangeCriteria(ctx, qa, "reserve-after-rereview"); err == nil {
		t.Fatal("requested review left the direct QA pass valid")
	}
}
