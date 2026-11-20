package mongostore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var proofHead = regexp.MustCompile(`^[0-9a-f]{40}$`)

type CriteriaRequest struct {
	Verb        string
	ID          ticket.ID
	Actor       string
	Holder      string `json:"holder"`
	Token       int64  `json:"token"`
	Brief       int    `json:"brief"`
	AC          int    `json:"ac"`
	Head        string `json:"head"`
	Request     string `json:"request"`
	Evidence    string `json:"evidence"`
	Proposal    string `json:"proposal"`
	Criterion   string `json:"criterion"`
	Checkpoint  string `json:"checkpoint"`
	Disposition string `json:"disposition"`
	Body        string `json:"body"`
}

func (s *Store) ChangeCriteria(ctx context.Context, r CriteriaRequest, key string) (ticket.Criteria, error) {
	var out ticket.Criteria
	err := s.write(ctx, func(tx *txn) error {
		var err error
		out, err = idempotent(tx, key, "criteria", r, func() (ticket.Criteria, error) { return tx.changeCriteria(r) }, nil)
		if err != nil || r.Verb != "merge" {
			return err
		}
		// A saved receipt is evidence of an earlier authorization. Check that
		// it is still in force before the caller can act on GitHub.
		t, err := tx.loadForWrite(r.ID)
		if err != nil {
			return err
		}
		if t.Criteria == nil || t.Criteria.Merge != r.Head || t.Criteria.AC != r.AC || t.Criteria.Brief != r.Brief || t.Status != ticket.StatusInReview || t.LiveClaim() == nil {
			return errors.New("merge authorization has ended")
		}
		return t.CheckFence(r.Holder, r.Token)
	})
	return out, err
}

func (tx *txn) changeCriteria(r CriteriaRequest) (ticket.Criteria, error) {
	t, err := tx.loadForWrite(r.ID)
	if err != nil {
		return ticket.Criteria{}, err
	}
	if t.Criteria == nil {
		return ticket.Criteria{}, errors.New("ticket has no accepted brief")
	}
	state := *t.Criteria
	if r.Verb == "closed-unmerged" || r.Verb == "head-moved" || r.Verb == "merge-rejected" {
		if state.Merge == "" || state.Merge != r.Head || t.Status != ticket.StatusInReview {
			return state, errors.New("merge reservation changed")
		}
		if r.Verb == "merge-rejected" {
			if r.Actor != "qa-lead" || ticket.HolderBot(r.Holder) != r.Actor || t.LiveClaim() == nil || state.AC != r.AC || state.Brief != r.Brief {
				return state, ticket.ErrLeaseLost
			}
			if err := t.CheckFence(r.Holder, r.Token); err != nil {
				return state, err
			}
		} else if r.Actor != "planner" || t.LiveClaim() != nil {
			return state, ticket.ErrLeaseLost
		}
		if err := tx.releaseMerge(t); err != nil {
			return state, err
		}
		state.Merge = ""
		return state, tx.appendEvent(t.ID, r.Verb, r.Actor, map[string]any{"head": r.Head})
	}
	if r.Verb == "merge" && state.Merge != "" && state.Merge == r.Head && state.AC == r.AC && state.Brief == r.Brief && r.Actor == "qa-lead" && ticket.HolderBot(r.Holder) == r.Actor {
		return state, t.CheckFence(r.Holder, r.Token)
	}
	if state.AC != r.AC || t.Status.Terminal() || state.Merge != "" {
		return state, errors.New("criteria changed, ticket closed or merge in flight")
	}
	var marker struct{ Revision int }
	if err := tx.coll("goal_revisions").findOne(bson.M{"_id": t.Goal}, &marker); err != nil {
		return state, err
	}
	set := bson.M{}
	switch r.Verb {
	case "assess":
		if r.Actor != "planner" || r.Brief != marker.Revision || r.Evidence == "" || r.Request != state.Request {
			return state, errors.New("assessment needs the current brief, request and planner rationale")
		}
		switch r.Disposition {
		case "fits":
			if r.Body != "" && r.Body != t.Body {
				return state, errors.New("use changed to replace criteria")
			}
		case "changed":
			if r.Body == "" || r.Body == t.Body {
				return state, errors.New("write the revised ticket criteria")
			}
			state.AC++
			state.Build, state.Review, state.QA = "", "", ""
			set["body"] = r.Body
		case "deferred", "replaced":
		default:
			return state, errors.New("unknown ticket disposition")
		}
		state.Brief, state.Disposition, state.Request = r.Brief, r.Disposition, ""
		hold := r.Disposition == "deferred" || r.Disposition == "replaced"
		if hold && t.Status != ticket.StatusBlocked {
			req := ticket.TransitionRequest{ID: t.ID, From: t.Status, To: ticket.StatusBlocked, Actor: r.Actor, Note: r.Evidence, Release: true}
			if claim := t.LiveClaim(); claim != nil {
				req.Holder, req.Token = claim.Holder, claim.Token
			}
			if _, err := tx.applyTransition(req); err != nil {
				return state, err
			}
		} else if r.Disposition == "changed" && t.Status == ticket.StatusInReview {
			req := ticket.TransitionRequest{ID: t.ID, From: t.Status, To: ticket.StatusInProgress, Actor: r.Actor, Note: r.Evidence, Release: true}
			if cl := t.LiveClaim(); cl != nil {
				req.Holder, req.Token = cl.Holder, cl.Token
			}
			if _, err := tx.moveTicket(req, true); err != nil {
				return state, err
			}
		} else if !hold && t.Status == ticket.StatusBlocked && (r.Request != "" || t.Criteria.Disposition == "deferred" || t.Criteria.Disposition == "replaced") {
			if _, err := tx.coll(cTickets).update(byID(t.ID), bson.M{"$set": bson.M{"criteria.request": "", "criteria.disposition": r.Disposition}}); err != nil {
				return state, err
			}
			if _, err := tx.applyTransition(ticket.TransitionRequest{ID: t.ID, From: t.Status, To: t.BlockedFrom, Actor: r.Actor, Note: r.Evidence}); err != nil {
				return state, err
			}
		}
	case "request":
		if (r.Actor != "junior-engineer" && r.Actor != "senior-engineer") || r.Brief != state.Brief || r.Request == "" || r.Evidence == "" || r.Proposal == "" || r.Criterion == "" || r.Checkpoint == "" || state.Request != "" || t.Status != ticket.StatusInProgress {
			return state, errors.New("revision request needs the disputed criterion, evidence, proposal and checkpoint on a live build")
		}
		if r.Holder == "" || ticket.HolderBot(r.Holder) != r.Actor || t.LiveClaim() == nil {
			return state, ticket.ErrLeaseLost
		}
		if _, err := tx.applyTransition(ticket.TransitionRequest{ID: t.ID, From: t.Status, To: ticket.StatusBlocked, Actor: r.Actor, Note: "Criteria request " + r.Request, Holder: r.Holder, Token: r.Token, Release: true}); err != nil {
			return state, err
		}
		state.Request, state.Checkpoint, state.Disposition = r.Request, r.Checkpoint, "clarification"
		if _, err := tx.applyCriteriaComment(t.ID, r); err != nil {
			return state, err
		}
	case "review-withdraw", "qa-withdraw", "rereview":
		if r.Brief != state.Brief || t.Status != ticket.StatusInReview || t.LiveClaim() == nil || r.Holder == "" || ticket.HolderBot(r.Holder) != r.Actor {
			return state, ticket.ErrLeaseLost
		}
		if err := t.CheckFence(r.Holder, r.Token); err != nil {
			return state, err
		}
		if r.Verb == "review-withdraw" && r.Actor != "reviewer" || r.Verb != "review-withdraw" && r.Actor != "qa-lead" {
			return state, ticket.ErrForbidden
		}
		state.QA = ""
		if r.Verb != "qa-withdraw" {
			state.Review = ""
		}
	case "build", "review", "qa", "merge":
		if !proofHead.MatchString(r.Head) || r.Brief != state.Brief || state.Request != "" || state.Disposition != "fits" && state.Disposition != "changed" {
			return state, errors.New("proof requires an exact head and cleared criteria")
		}
		if err := t.CheckFence(r.Holder, r.Token); err != nil {
			return state, err
		}
		if r.Holder == "" || ticket.HolderBot(r.Holder) != r.Actor || t.LiveClaim() == nil {
			return state, ticket.ErrLeaseLost
		}
		switch r.Verb {
		case "build":
			if t.Status != ticket.StatusInProgress || (r.Actor != "junior-engineer" && r.Actor != "senior-engineer") {
				return state, ticket.ErrForbidden
			}
			state.Build, state.Review, state.QA = r.Head, "", ""
		case "review":
			if r.Actor != "reviewer" || state.Build != r.Head || t.Status != ticket.StatusInReview {
				return state, errors.New("review needs the current build at this head")
			}
			state.Review, state.QA = r.Head, ""
		case "qa":
			if r.Actor != "qa-lead" || state.Review != r.Head || t.Status != ticket.StatusInReview {
				return state, errors.New("QA needs the current review at this head")
			}
			state.QA = r.Head
		case "merge":
			if r.Actor != "qa-lead" || state.Review != r.Head || state.QA != r.Head || t.Status != ticket.StatusInReview {
				return state, errors.New("merge needs the current review and QA proof")
			}
			if marker.Revision != state.Brief {
				return state, errors.New("planner has not cleared this ticket for the latest brief")
			}
			res, err := tx.coll("goal_merges").update(bson.M{"_id": t.Goal}, bson.M{"$inc": bson.M{"merging": 1}}, options.UpdateOne().SetUpsert(true))
			if err != nil {
				return state, err
			}
			if res.MatchedCount+res.UpsertedCount != 1 {
				return state, errors.New("planner has not cleared this ticket for the latest brief")
			}
			state.Merge = r.Head
		}
	default:
		return state, errors.New("unknown criteria operation")
	}
	set["criteria"] = state
	set["updated_at"] = dt(tx.now)
	_, err = tx.coll(cTickets).update(byID(t.ID), bson.M{"$set": set, "$inc": bson.M{"version": int64(1)}})
	if err != nil {
		return state, err
	}
	return state, tx.appendEvent(t.ID, "criteria_"+r.Verb, r.Actor, map[string]any{"ac": state.AC, "brief": state.Brief, "head": r.Head, "request": r.Request, "evidence": r.Evidence, "checkpoint": r.Checkpoint, "previous_body": func() string {
		if r.Disposition == "changed" {
			return t.Body
		}
		return ""
	}()})
}

// A clarification reaches the planner with its branch and the exact question.
func (tx *txn) applyCriteriaComment(id ticket.ID, r CriteriaRequest) (int64, error) {
	body := fmt.Sprintf("Escalation: criteria request %s\nCriterion: %s\nEvidence: %s\nProposal: %s\nCheckpoint: %s\nReturn-role: engineer", r.Request, r.Criterion, r.Evidence, r.Proposal, r.Checkpoint)
	note, err := tx.addComment(ticket.CommentRequest{ID: id, Actor: r.Actor, Body: body})
	return note.ID, err
}

func (tx *txn) finishCriteria(t ticket.Ticket, req ticket.TransitionRequest) error {
	if t.Criteria == nil {
		return nil
	}
	st := t.Criteria
	if st.Merge != "" {
		if req.To != ticket.StatusDone {
			return errors.New("merge in flight; reconcile the PR first")
		}
		if !strings.Contains(req.Note, st.Merge) {
			return errors.New("closure must name the head authorized to merge")
		}
		return tx.releaseMerge(t)
	}
	if req.To == ticket.StatusDone && t.Type == ticket.TypeShip {
		return errors.New("obtain criteria merge authorization first")
	}
	if (st.Request != "" || st.Disposition == "deferred" || st.Disposition == "replaced") && t.Status == ticket.StatusBlocked {
		return errors.New("planner must resolve the pending criteria request")
	}
	return nil
}

func (tx *txn) releaseMerge(t ticket.Ticket) error {
	res, err := tx.coll("goal_merges").update(bson.M{"_id": t.Goal, "merging": bson.M{"$gt": 0}}, bson.M{"$inc": bson.M{"merging": -1}})
	if err != nil {
		return err
	}
	if res.MatchedCount != 1 {
		return errors.New("merge reservation missing")
	}
	_, err = tx.coll(cTickets).update(byID(t.ID), bson.M{"$set": bson.M{"criteria.merge": ""}})
	return err
}

func (tx *txn) newCriteria(goal string, revision int) (*ticket.Criteria, error) {
	var marker struct{ Revision int }
	err := tx.coll("goal_revisions").findOne(bson.M{"_id": goal}, &marker)
	if errors.Is(err, mongo.ErrNoDocuments) || err == nil && marker.Revision != revision {
		return nil, errors.New("create needs the current accepted brief revision")
	}
	if err != nil {
		return nil, err
	}
	return &ticket.Criteria{Brief: revision, AC: 1, Disposition: "fits"}, nil
}

// ProofNote commits a verdict with the exact criteria it covered. A changed
// revision cannot slip between posting a GO and recording its authority.
func (s *Store) ProofNote(ctx context.Context, r CriteriaRequest, note ticket.CommentRequest) (ticket.Comment, error) {
	var out ticket.Comment
	err := s.write(ctx, func(tx *txn) error {
		var err error
		out, err = idempotent(tx, note.IdempotencyKey, "proof-note", struct {
			Proof CriteriaRequest
			Note  ticket.CommentRequest
		}{r, note}, func() (ticket.Comment, error) {
			if _, err := tx.changeCriteria(r); err != nil {
				return ticket.Comment{}, err
			}
			return tx.addComment(note)
		}, nil)
		return err
	})
	return out, err
}
