package chief

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Wake reads its destinations privately. Worker text never enters the T3 prompt.
func (s *Service) Wake(ctx context.Context, base string) error {
	inbox := s.db.Collection("goal_inbox")
	rows, err := s.list(ctx, "goal_inbox", bson.M{"state": "pending"})
	if err != nil {
		return err
	}
	type route struct{ Thread string }
	type item struct{ ID, Goal, Text string }
	groups := map[string][]item{}
	for _, row := range rows {
		id, _ := row["_id"].(string)
		goal, _ := row["goal"].(string)
		text, _ := row["text"].(string)
		if !identifier.MatchString(goal) || !identifier.MatchString(id) || text == "" {
			continue
		}
		var dest route
		err := s.db.Collection("chief_sessions").FindOne(ctx, bson.M{"_id": "goal/" + goal}).Decode(&dest)
		if errors.Is(err, mongo.ErrNoDocuments) {
			continue
		}
		if err != nil {
			return err
		}
		if !threadID.MatchString(dest.Thread) {
			return errors.New("invalid private wake registration")
		}
		var marker struct{ Revision int }
		err = s.db.Collection("goal_revisions").FindOne(ctx, bson.M{"_id": goal}).Decode(&marker)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		// Accept a repeated item only if its goal and original payload match.
		filter := bson.M{"_id": goal + "/" + id, "goal": goal, "proposal": text}
		_, err = s.db.Collection("goal_decisions").UpdateOne(ctx, filter, bson.M{"$setOnInsert": bson.M{"state": "open", "revision": marker.Revision, "generation": 0}}, options.UpdateOne().SetUpsert(true))
		if err != nil {
			return err
		}
		groups[dest.Thread] = append(groups[dest.Thread], item{id, goal, text})
	}
	var sendErrors error
	for dest, items := range groups {
		// Reserve the wake before sending. A killed sender may leave an
		// uncertain receipt; the next ordinary wake retries after the gate.
		now := time.Now().UTC()
		result, err := s.db.Collection("chief_sessions").UpdateOne(ctx, bson.M{"_id": dest, "status": "open", "$or": bson.A{bson.M{"wake_after": nil}, bson.M{"wake_after": bson.M{"$lte": now}}}}, bson.M{"$set": bson.M{"wake_after": now.Add(runs.GateWindow)}})
		if err != nil {
			return err
		}
		if result.MatchedCount == 0 {
			continue
		}
		if err := SendWake(ctx, base, dest); err != nil {
			sendErrors = errors.Join(sendErrors, err)
			continue
		}
		ids := make([]string, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.ID)
		}
		if _, err := inbox.UpdateMany(ctx, bson.M{"_id": bson.M{"$in": ids}, "state": "pending"}, bson.M{"$set": bson.M{"state": "delivered"}}); err != nil {
			return err
		}
	}
	return sendErrors
}

func SendWake(ctx context.Context, base, thread string) error {
	if !threadID.MatchString(thread) {
		return errors.New("no registered chief for this fault")
	}
	args := []string{"thread", "send", thread, "ring: run pickup", "--json"}
	if base != "" {
		args = append(args, "--base-dir", base)
	}
	if err := exec.CommandContext(ctx, "t3", args...).Run(); err != nil {
		return errors.New("T3 wake failed; item remains pending")
	}
	return nil
}
