// Package chief holds goal conversations and decision claims in collections
// the chief of staff writes. Workers read only the published projection.
package chief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type Config struct {
	FaultThread string `json:"fault_thread,omitempty"`
	URI         string `json:"uri"`
	Board       string `json:"board_id"`
	T3Base      string `json:"t3_base,omitempty"`
}

func ReadConfig(path string) (Config, error) {
	var cfg Config
	info, err := os.Lstat(path)
	if err != nil {
		return cfg, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return cfg, errors.New("chief config must be a private regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if json.Unmarshal(data, &cfg) != nil || cfg.Board == "" {
		return cfg, errors.New("invalid chief config")
	}
	return cfg, nil
}

type Service struct {
	client *mongo.Client
	db     *mongo.Database
}

func Open(ctx context.Context, cfg Config) (*Service, error) {
	name, err := mongostore.ExplicitDatabase(cfg.URI)
	if err != nil {
		return nil, errors.New("name the dedicated board database in the private chief URI")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(cfg.URI).SetServerSelectionTimeout(5 * time.Second).SetMaxPoolSize(4))
	if err != nil {
		return nil, errors.New("cannot open private MongoDB connection")
	}
	db := client.Database(name)
	if err := db.Collection("meta").FindOne(ctx, bson.M{"_id": "runtime_board_id", "board_id": cfg.Board}).Err(); err != nil {
		client.Disconnect(ctx)
		return nil, errors.New("cannot verify chief access to the pinned board")
	}
	return &Service{client, db}, nil
}

// Prepare installs the indexes used by private pickup and recovery once.
func (s *Service) Prepare(ctx context.Context) error {
	for _, item := range []struct {
		collection string
		keys       bson.D
	}{
		{"goal_decisions", bson.D{{Key: "goal", Value: 1}, {Key: "state", Value: 1}}},
		{"goal_decisions", bson.D{{Key: "delivery.state", Value: 1}}},
		{"goal_briefs", bson.D{{Key: "goal", Value: 1}, {Key: "current", Value: 1}}},
		{"owner_rules", bson.D{{Key: "goal", Value: 1}, {Key: "current", Value: 1}}},
	} {
		if _, err := s.db.Collection(item.collection).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: item.keys}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
var conflict = errors.New("state changed; refresh this goal and reconcile the pending action")

type Request struct {
	verified         bool
	origin, question string
	RuleScope        string     `json:"rule_scope"`
	Goal             string     `json:"goal"`
	ID               string     `json:"id"`
	Profile          string     `json:"profile"`
	Model            string     `json:"model"`
	Harness          string     `json:"harness"`
	Effort           string     `json:"effort"`
	Text             string     `json:"text"`
	Source           string     `json:"source"`
	Revision         int        `json:"revision"`
	Generation       int        `json:"generation"`
	Transfer         bool       `json:"transfer"`
	Rule             string     `json:"rule"`
	RuleRevision     int        `json:"rule_revision"`
	Kind             string     `json:"kind"`
	Expires          *time.Time `json:"expires,omitempty"`
	Exceptions       string     `json:"exceptions"`
	Rationale        string     `json:"rationale"`
	Decision         string     `json:"decision"`
}

type decision struct {
	ID                                                                            string `bson:"_id"`
	Goal                                                                          string
	Revision                                                                      int
	Generation                                                                    int
	Holder, State, Question, Thread, Answer, Source, Ruling, Authority, Rationale string
	RuleRevision                                                                  int    `bson:"rule_revision"`
	RuleScope                                                                     string `bson:"rule_scope"`
}

func one(result *mongo.UpdateResult, err error) error {
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return conflict
		}
		return err
	}
	if result.MatchedCount+result.UpsertedCount != 1 {
		return conflict
	}
	return nil
}

func (s *Service) Apply(ctx context.Context, thread, verb string, r Request) (any, error) {
	if !threadID.MatchString(thread) || !identifier.MatchString(r.Goal) || r.Revision < 0 || r.Generation < 0 {
		return nil, errors.New("invalid thread, goal or revision")
	}
	sess, err := s.client.StartSession()
	if err != nil {
		return nil, err
	}
	defer sess.EndSession(ctx)
	return sess.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		sessions := s.db.Collection("chief_sessions")
		key := bson.M{"_id": thread, "goal": r.Goal}
		if verb == "register" {
			if (r.Harness != "codex" && r.Harness != "claude") || !identifier.MatchString(r.Profile) || r.Model == "" || r.Effort == "" {
				return nil, errors.New("register needs a native harness, profile, model and effort")
			}
			key["profile"], key["model"], key["harness"], key["effort"] = r.Profile, r.Model, r.Harness, r.Effort
			if err := one(sessions.UpdateOne(ctx, key, bson.M{"$setOnInsert": bson.M{"status": "open"}}, options.UpdateOne().SetUpsert(true))); err != nil {
				return nil, err
			}
			_, err := sessions.UpdateOne(ctx, bson.M{"_id": "goal/" + r.Goal}, bson.M{"$setOnInsert": bson.M{"thread": thread, "generation": 1}}, options.UpdateOne().SetUpsert(true))
			return key, err
		}
		key["status"] = "open"
		if err := sessions.FindOne(ctx, key).Err(); err != nil {
			return nil, errors.New("thread is not registered for this goal")
		}
		switch verb {
		case "assign":
			return bson.M{"thread": thread, "generation": r.Generation + 1}, one(sessions.UpdateOne(ctx, bson.M{"_id": "goal/" + r.Goal, "generation": r.Generation}, bson.M{"$set": bson.M{"thread": thread}, "$inc": bson.M{"generation": 1}}))
		case "ack":
			return s.ack(ctx, thread, r)
		case "result":
			if !identifier.MatchString(r.ID) {
				return nil, errors.New("name the original request")
			}
			id := r.Goal + "/instruction/" + r.ID
			if err := s.db.Collection("owner_rules").FindOne(ctx, bson.M{"_id": id, "kind": "ask", "author": thread}).Err(); err != nil {
				return nil, errors.New("no second opinion requested by this thread")
			}
			var d mongostore.PlannerDelivery
			if err := s.db.Collection("planner_deliveries").FindOne(ctx, bson.M{"_id": id, "goal": r.Goal, "job": "ask"}).Decode(&d); err != nil {
				return nil, err
			}
			return bson.M{"state": d.State, "answer": d.Answer, "run": d.Run}, nil
		case "read":
			return s.read(ctx, thread, r.Goal)
		case "draft", "publish":
			return s.brief(ctx, thread, verb, r)
		case "instruction":
			return s.instruction(ctx, thread, r)
		case "direction":
			return s.direction(ctx, r)
		case "propose", "claim", "question", "answer", "rule":
			return s.decide(ctx, thread, verb, r)
		default:
			return nil, errors.New("unknown chief operation")
		}
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
}

func (s *Service) list(ctx context.Context, collection string, filter bson.M) ([]bson.M, error) {
	cur, err := s.db.Collection(collection).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	results := []bson.M{}
	err = cur.All(ctx, &results)
	return results, err
}

func (s *Service) read(ctx context.Context, thread, goal string) (any, error) {
	out := bson.M{}
	for _, q := range []struct {
		name, coll string
		filter     bson.M
	}{
		{"draft", "chief_drafts", bson.M{"_id": thread + "/" + goal}},
		{"decisions", "goal_decisions", bson.M{"goal": goal, "state": bson.M{"$ne": "acknowledged"}}},
		{"direction", "owner_rules", bson.M{"goal": bson.M{"$in": bson.A{goal, "all"}}, "current": true, "kind": bson.M{"$ne": "revoked"}, "$or": bson.A{bson.M{"expires": nil}, bson.M{"$expr": bson.M{"$gt": bson.A{"$expires", "$$NOW"}}}}}},
		{"deliveries", "planner_deliveries", bson.M{"goal": goal, "job": bson.M{"$ne": "ask"}}},
		{"assignment", "chief_sessions", bson.M{"_id": "goal/" + goal}},
		{"marker", "goal_revisions", bson.M{"_id": goal}},
		{"brief", "goal_briefs", bson.M{"goal": goal, "current": true}},
	} {
		rows, err := s.list(ctx, q.coll, q.filter)
		if err != nil {
			return nil, err
		}
		out[q.name] = rows
	}
	return out, nil
}

func (s *Service) brief(ctx context.Context, thread, verb string, r Request) (any, error) {
	drafts := s.db.Collection("chief_drafts")
	id := thread + "/" + r.Goal
	if verb == "draft" {
		if r.Text == "" {
			return nil, errors.New("draft needs text")
		}
		_, err := drafts.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"goal": r.Goal, "base": r.Revision, "text": r.Text}}, options.UpdateOne().SetUpsert(true))
		return bson.M{"base": r.Revision}, err
	}
	if r.Source == "" {
		return nil, errors.New("publish needs the owner's acceptance source")
	}
	var draft struct {
		Base int
		Text string
	}
	if err := drafts.FindOne(ctx, bson.M{"_id": id}).Decode(&draft); err != nil {
		return nil, err
	}
	if draft.Base != r.Revision {
		return nil, conflict
	}
	next := r.Revision + 1
	key := fmt.Sprintf("%s/brief/%d", r.Goal, next)
	briefs := s.db.Collection("goal_briefs")
	var prior struct{ Author, Source, Text string }
	err := briefs.FindOne(ctx, bson.M{"_id": key}).Decode(&prior)
	if err == nil {
		if prior.Author != thread || prior.Source != r.Source || prior.Text != draft.Text {
			return nil, conflict
		}
		return bson.M{"delivery": key, "revision": next}, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}
	// Workers reserve merges on a separate document. Their Atlas role
	// can read accepted revisions but cannot publish one.
	if err := one(s.db.Collection("goal_merges").UpdateOne(ctx, bson.M{"_id": r.Goal, "merging": bson.M{"$in": bson.A{0, nil}}}, bson.M{"$inc": bson.M{"publications": 1}}, options.UpdateOne().SetUpsert(true))); err != nil {
		return nil, err
	}
	markers := s.db.Collection("goal_revisions")
	if err := one(markers.UpdateOne(ctx, bson.M{"_id": r.Goal, "revision": r.Revision}, bson.M{"$set": bson.M{"revision": next}}, options.UpdateOne().SetUpsert(r.Revision == 0))); err != nil {
		return nil, err
	}
	if _, err := briefs.UpdateOne(ctx, bson.M{"goal": r.Goal, "current": true}, bson.M{"$set": bson.M{"current": false}}); err != nil {
		return nil, err
	}
	doc := bson.M{"_id": key, "goal": r.Goal, "revision": next, "parent": r.Revision, "author": thread, "text": draft.Text, "source": r.Source, "current": true, "delivery": bson.M{"id": key, "state": "pending"}}
	if _, err := briefs.InsertOne(ctx, doc); err != nil {
		return nil, err
	}
	return bson.M{"delivery": key, "revision": next}, s.publishDelivery(ctx, key, r.Goal, "plan", draft.Text, next)
}

// The accepted instruction and its pending state become visible together.
// A worker records its receipt before dispatching a run by the delivery ID.
func (s *Service) publishDelivery(ctx context.Context, id, goal, job, text string, revision int) error {
	_, err := s.db.Collection("planner_deliveries").InsertOne(ctx, bson.M{"_id": id, "goal": goal, "job": job, "text": text, "revision": revision, "state": "pending"})
	return err
}

func (s *Service) direction(ctx context.Context, r Request) (any, error) {
	if !identifier.MatchString(r.ID) || r.Source == "" || r.Text == "" || (r.Kind != "preference" && r.Kind != "delegation" && r.Kind != "revoked") {
		return nil, errors.New("direction needs a rule ID, kind, wording and owner source")
	}
	coll := s.db.Collection("owner_rules")
	if r.Revision > 0 {
		if err := one(coll.UpdateOne(ctx, bson.M{"goal": r.Goal, "rule": r.ID, "revision": r.Revision, "current": true}, bson.M{"$set": bson.M{"current": false}})); err != nil {
			return nil, err
		}
	}
	n := r.Revision + 1
	_, err := coll.InsertOne(ctx, bson.M{"_id": fmt.Sprintf("%s/%s/%d", r.Goal, r.ID, n), "goal": r.Goal, "rule": r.ID, "revision": n, "kind": r.Kind, "current": true, "text": r.Text, "source": r.Source, "exceptions": r.Exceptions, "expires": r.Expires})
	if mongo.IsDuplicateKeyError(err) {
		return nil, conflict
	}
	return bson.M{"revision": n}, err
}

// Save an explicit board control or a second-opinion request before asking
// the workers to carry it out. Replays must carry identical content.
func (s *Service) instruction(ctx context.Context, thread string, r Request) (any, error) {
	if !identifier.MatchString(r.ID) || r.Source == "" || r.Text == "" {
		return nil, errors.New("instruction needs a stable ID, text and owner or brief source")
	}
	switch r.Kind {
	case "ask":
	case "start", "stop", "rules":
		if r.Goal != "all" {
			return nil, errors.New("standing controls require the all-goals session")
		}
	default:
		return nil, errors.New("unsupported instruction")
	}
	id := r.Goal + "/instruction/" + r.ID
	coll := s.db.Collection("owner_rules")
	var prior struct{ Kind, Source, Text string }
	err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&prior)
	if err == nil {
		if prior.Kind != r.Kind || prior.Source != r.Source || prior.Text != r.Text {
			return nil, conflict
		}
		return bson.M{"delivery": id}, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}
	rev := r.Revision
	if r.Kind != "ask" {
		var seq struct{ Revision int }
		err = coll.FindOneAndUpdate(ctx, bson.M{"_id": "board-control-sequence"}, bson.M{"$inc": bson.M{"revision": 1}}, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&seq)
		if err != nil {
			return nil, err
		}
		rev = seq.Revision
	}
	if _, err := coll.InsertOne(ctx, bson.M{"_id": id, "goal": r.Goal, "kind": r.Kind, "source": r.Source, "author": thread, "text": r.Text, "revision": rev}); err != nil {
		return nil, err
	}
	return bson.M{"delivery": id}, s.publishDelivery(ctx, id, r.Goal, r.Kind, r.Text, rev)
}

// CheckCos binds a cos verb to the registered chief session that issued it.
// The worker sees only the goal and a previously published delivery ID.
func (s *Service) CheckCos(ctx context.Context, thread, goal, verb, id string) error {
	if !threadID.MatchString(thread) || !identifier.MatchString(goal) {
		return errors.New("cos needs a registered goal and thread")
	}
	if err := s.db.Collection("chief_sessions").FindOne(ctx, bson.M{"_id": thread, "goal": goal, "status": "open"}).Err(); err != nil {
		return errors.New("thread is not registered for this goal")
	}
	switch verb {
	case "look", "eta", "goals", "pickup":
		return nil
	case "plan":
		if err := s.db.Collection("goal_briefs").FindOne(ctx, bson.M{"goal": goal, "delivery.id": id}).Err(); err == nil {
			return nil
		}
	case "rule":
		if err := s.db.Collection("goal_decisions").FindOne(ctx, bson.M{"goal": goal, "delivery.id": id, "holder": thread, "state": bson.M{"$in": bson.A{"ruled", "acknowledged"}}}).Err(); err == nil {
			return nil
		}
	case "ack":
		// The private ack additionally checks content and claim generation.
		return s.db.Collection("planner_deliveries").FindOne(ctx, bson.M{"_id": id, "goal": goal}).Err()
	case "ask", "stop", "start", "rules":
		if err := s.db.Collection("owner_rules").FindOne(ctx, bson.M{"_id": id, "goal": goal, "kind": verb, "author": thread}).Err(); err == nil {
			return nil
		}
	}
	return errors.New("no authorized private delivery for this command")
}
