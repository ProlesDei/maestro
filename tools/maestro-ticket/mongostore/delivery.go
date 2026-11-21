package mongostore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type PlannerDelivery struct {
	ID       string `bson:"_id" json:"id"`
	Goal     string `json:"goal"`
	Job      string `json:"job"`
	Text     string `json:"text"`
	Revision int    `json:"revision"`
	State    string `json:"state"`
	Receipt  string `json:"receipt,omitempty"`
	Run      string `json:"run,omitempty"`
	Answer   string `json:"answer,omitempty"`
}

func (d PlannerDelivery) Hash() string {
	b, _ := json.Marshal([]any{d.ID, d.Goal, d.Job, d.Text, d.Revision})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (s *Store) PendingDeliveries(ctx context.Context, goal string) ([]PlannerDelivery, error) {
	db, done, err := s.conn()
	if err != nil {
		return nil, err
	}
	defer done()
	filter := bson.M{"state": bson.M{"$in": bson.A{"pending", "accepted"}}}
	if goal != "" {
		filter["goal"] = goal
	}
	cur, err := db.Collection("planner_deliveries").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "revision", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, s.markErr(err)
	}
	defer cur.Close(ctx)
	var out []PlannerDelivery
	err = cur.All(ctx, &out)
	return out, s.markErr(err)
}

// GoalItems returns the worker cards that still need a chief's ruling.
func (s *Store) GoalItems(ctx context.Context, goal string) ([]bson.M, error) {
	db, done, err := s.conn()
	if err != nil {
		return nil, err
	}
	defer done()
	filter := bson.M{"goal": goal, "state": bson.M{"$in": bson.A{"pending", "delivered"}}}
	cur, err := db.Collection("goal_inbox").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, s.markErr(err)
	}
	defer cur.Close(ctx)
	out := []bson.M{}
	err = cur.All(ctx, &out)
	return out, s.markErr(err)
}

// Receipt and instruction occupy one document. Pin the payload in the
// match so a concurrent change cannot acknowledge different wording.
func (s *Store) AcceptDelivery(ctx context.Context, id, verb string) (PlannerDelivery, error) {
	db, done, err := s.conn()
	if err != nil {
		return PlannerDelivery{}, err
	}
	defer done()
	coll := db.Collection("planner_deliveries")
	var d PlannerDelivery
	if err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&d); err != nil {
		return d, s.markErr(err)
	}
	expected := d.Job
	if expected == "plan" && strings.HasSuffix(d.ID, "/ruling") {
		expected = "rule"
	}
	if verb != "" && verb != "ack" && expected != verb {
		return d, errors.New("delivery belongs to a different verb")
	}
	if d.Receipt != "" {
		if d.Receipt != d.Hash() {
			return d, errors.New("delivery ID has conflicting payloads")
		}
		return d, nil
	}
	if verb == "ack" {
		return d, errors.New("planner has not accepted this delivery")
	}
	filter := bson.M{"_id": id, "state": "pending", "goal": d.Goal, "job": d.Job, "text": d.Text, "revision": d.Revision}
	update := bson.M{"$set": bson.M{"receipt": d.Hash(), "state": "accepted"}}
	err = coll.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = coll.FindOne(ctx, bson.M{"_id": id, "receipt": d.Hash()}).Decode(&d)
	}
	return d, s.markErr(err)
}

func (s *Store) DeliveryStarted(ctx context.Context, d PlannerDelivery, run string) error {
	db, done, err := s.conn()
	if err != nil {
		return err
	}
	defer done()
	filter := bson.M{"_id": d.ID, "receipt": d.Hash(), "$or": bson.A{bson.M{"run": bson.M{"$exists": false}}, bson.M{"run": run}}}
	// A delayed retry cannot move a completed delivery back to started.
	res, err := db.Collection("planner_deliveries").UpdateOne(ctx, filter, mongo.Pipeline{bson.D{{Key: "$set", Value: bson.M{"run": run, "state": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$state", "completed"}}, "completed", "started"}}}}}})
	if err == nil && res.MatchedCount != 1 {
		return errors.New("planner delivery already belongs to another run")
	}
	return s.markErr(err)
}

func (s *Store) CompleteDelivery(ctx context.Context, id, hash, run, completedBy, reply string) error {
	db, done, err := s.conn()
	if err != nil {
		return err
	}
	defer done()
	set := bson.M{"state": "completed", "run": run, "completed_by": completedBy}
	var d PlannerDelivery
	coll := db.Collection("planner_deliveries")
	if err := coll.FindOne(ctx, bson.M{"_id": id, "receipt": hash}).Decode(&d); err != nil {
		return s.markErr(err)
	}
	if d.Job == "ask" {
		set["answer"] = reply
	}
	res, err := coll.UpdateOne(ctx, bson.M{"_id": id, "receipt": hash, "completed_by": bson.M{"$in": bson.A{nil, completedBy}}, "$or": bson.A{bson.M{"run": bson.M{"$exists": false}}, bson.M{"run": run}}}, bson.M{"$set": set})
	if err == nil && res.MatchedCount != 1 {
		return errors.New("completion belongs to another planner run")
	}
	return s.markErr(err)
}

// PutGoalItem saves the card even if the fixed wake fails or no chief is online.
func (s *Store) PutGoalItem(ctx context.Context, id, goal, text string) error {
	db, done, err := s.conn()
	if err != nil {
		return err
	}
	defer done()
	_, err = db.Collection("goal_inbox").UpdateOne(ctx, bson.M{"_id": id, "goal": goal, "text": text}, bson.M{"$setOnInsert": bson.M{"state": "pending"}}, options.UpdateOne().SetUpsert(true))
	return s.markErr(err)
}

func (s *Store) HasPendingGoalItem(ctx context.Context) (bool, error) {
	db, done, err := s.conn()
	if err != nil {
		return false, err
	}
	defer done()
	err = db.Collection("goal_inbox").FindOne(ctx, bson.M{"state": "pending"}).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	return err == nil, s.markErr(err)
}

// ApplyControl records both the effect and completion. A later start cannot
// overtake a pending stop, even when two recovery calls run concurrently.
func (s *Store) ApplyControl(ctx context.Context, d PlannerDelivery) error {
	return s.write(ctx, func(tx *txn) error {
		if d.Goal != "all" || d.Receipt != d.Hash() {
			return errors.New("invalid board control")
		}
		var current PlannerDelivery
		if err := tx.coll("planner_deliveries").findOne(bson.M{"_id": d.ID, "receipt": d.Receipt}, &current); err != nil {
			return err
		}
		if current.State == "completed" {
			return nil
		}
		var position struct{ Revision int }
		err := tx.coll(cMeta).findOne(bson.M{"_id": "board-control"}, &position)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		if d.Revision != position.Revision+1 {
			return errors.New("waiting for the preceding board control")
		}
		set := bson.M{"revision": d.Revision}
		switch d.Job {
		case "stop", "start":
			if _, err := tx.coll(cMeta).update(bson.M{"_id": writerID}, bson.M{"$set": bson.M{"halt": d.Job == "stop"}}); err != nil {
				return err
			}
		case "rules":
			set["rules"] = d.Text
		default:
			return errors.New("unknown board control")
		}
		if _, err := tx.coll(cMeta).update(bson.M{"_id": "board-control"}, bson.M{"$set": set}, options.UpdateOne().SetUpsert(true)); err != nil {
			return err
		}
		_, err = tx.coll("planner_deliveries").update(bson.M{"_id": d.ID}, bson.M{"$set": bson.M{"state": "completed"}})
		return err
	})
}

func (s *Store) RuntimeRules(ctx context.Context) (string, error) {
	db, done, err := s.conn()
	if err != nil {
		return "", err
	}
	defer done()
	var row struct{ Rules string }
	err = db.Collection(cMeta).FindOne(ctx, bson.M{"_id": "board-control"}).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", nil
	}
	return row.Rules, s.markErr(err)
}
