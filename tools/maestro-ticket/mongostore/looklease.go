package mongostore

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// AcquireLook either takes the lease or marks the current holder dirty.
// Each path changes the same document, including a release with no work.
func (s *Store) AcquireLook(ctx context.Context, token string) (bool, error) {
	db, done, err := s.conn()
	if err != nil {
		return false, err
	}
	defer done()
	expired := bson.M{"$lte": bson.A{bson.M{"$ifNull": bson.A{"$until", 0}}, "$$NOW"}}
	next := bson.M{"$add": bson.A{"$$NOW", int64(600000)}}
	update := mongo.Pipeline{bson.D{{Key: "$set", Value: bson.M{
		"owner": bson.M{"$cond": bson.A{expired, token, "$owner"}},
		"until": bson.M{"$cond": bson.A{expired, next, "$until"}},
		"dirty": bson.M{"$not": bson.A{expired}},
	}}}}
	var row struct{ Owner string }
	err = db.Collection(cMarks).FindOneAndUpdate(ctx, markID("runtime", "look"), update, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&row)
	return row.Owner == token, s.markErr(err)
}

// FinishLook consumes a dirty flag and keeps the lease, or releases it.
// Once another holder takes over, the stale token can change nothing.
func (s *Store) FinishLook(ctx context.Context, token string) (bool, error) {
	db, done, err := s.conn()
	if err != nil {
		return false, err
	}
	defer done()
	filter := append(markID("runtime", "look"), bson.E{Key: "owner", Value: token})
	update := mongo.Pipeline{bson.D{{Key: "$set", Value: bson.M{
		"owner": bson.M{"$cond": bson.A{"$dirty", token, ""}},
		"until": bson.M{"$cond": bson.A{"$dirty", bson.M{"$add": bson.A{"$$NOW", int64(600000)}}, "$$NOW"}},
		"dirty": false,
	}}}}
	var row struct{ Owner string }
	err = db.Collection(cMarks).FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, errors.New("planner look lease lost")
	}
	return row.Owner == token, s.markErr(err)
}

func (s *Store) RenewLook(ctx context.Context, token string) error {
	db, done, err := s.conn()
	if err != nil {
		return err
	}
	defer done()
	filter := append(markID("runtime", "look"), bson.E{Key: "owner", Value: token})
	result, err := db.Collection(cMarks).UpdateOne(ctx, filter, mongo.Pipeline{bson.D{{Key: "$set", Value: bson.M{"until": bson.M{"$add": bson.A{"$$NOW", int64(600000)}}}}}})
	if err != nil {
		return s.markErr(err)
	}
	if result.MatchedCount != 1 {
		return errors.New("planner look lease lost")
	}
	return nil
}
