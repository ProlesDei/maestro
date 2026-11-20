package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// ExplicitDatabase refuses MongoDB's default database for a fresh board.
func ExplicitDatabase(uri string) (string, error) {
	cs, err := connstring.ParseAndValidate(uri)
	if err != nil || cs.Database == "" || cs.Database == "admin" || cs.Database == "local" || cs.Database == "config" {
		return "", fmt.Errorf("%w: name a dedicated board database in the URI", ticket.ErrInvalid)
	}
	return cs.Database, nil
}

// verifyIdentity creates only the identity in an empty target. setup can
// safely finish the schema after a crash once this identity matches.
func (s *Store) verifyIdentity(ctx context.Context, id string, create bool) error {
	db, done, err := s.conn()
	if err != nil {
		return err
	}
	defer done()
	coll := db.Collection(cMeta)
	filter := bson.D{{Key: "_id", Value: "runtime_board_id"}}
	var result struct {
		ID string `bson:"board_id"`
	}
	err = coll.FindOne(ctx, filter).Decode(&result)
	if errors.Is(err, mongo.ErrNoDocuments) && create {
		names, err := db.ListCollectionNames(ctx, bson.D{})
		if err != nil {
			return err
		}
		if len(names) == 0 {
			_, err = coll.InsertOne(ctx, bson.D{{Key: "_id", Value: "runtime_board_id"}, {Key: "board_id", Value: id}})
			if err != nil && !mongo.IsDuplicateKeyError(err) {
				return err
			}
		}
		err = coll.FindOne(ctx, filter).Decode(&result)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
	} else if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	if id == "" || result.ID != id {
		return fmt.Errorf("%w: target has no matching Maestro board identity", ticket.ErrInvalid)
	}
	return nil
}
