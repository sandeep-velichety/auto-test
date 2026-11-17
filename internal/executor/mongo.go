package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/model"
)

// toBSON converts a YAML/JSON value to BSON through extended JSON, so
// filters can use {"$oid": "..."} and {"$date": "..."}.
func toBSON(v any) (bson.D, error) {
	d := bson.D{}
	if v == nil {
		return d, nil
	}
	raw := []byte(mustJSON(v))
	if s, ok := v.(string); ok {
		raw = []byte(s)
	}
	if err := bson.UnmarshalExtJSON(raw, false, &d); err != nil {
		return nil, err
	}
	return d, nil
}

// Mongo runs a read-only check: find, count or aggregate. The result
// document is {"count": n, "documents": [...]}.
func Mongo(ctx context.Context, s *model.MongoStep, creds *auth.Credentials) (*model.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout(s.TimeoutMs, 15000))
	defer cancel()
	opts := options.Client().ApplyURI(s.URI)
	if creds.TLS != nil {
		opts.SetTLSConfig(creds.TLS.Clone())
	}
	if creds.Username != "" {
		opts.SetAuth(options.Credential{Username: creds.Username, Password: creds.Password})
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	defer client.Disconnect(context.Background())
	coll := client.Database(s.Database).Collection(s.Collection)

	filter, err := toBSON(s.Filter)
	if err != nil {
		return nil, fmt.Errorf("mongo.filter: %w", err)
	}
	start := time.Now()
	var docs []bson.D
	count := int64(-1)
	switch strings.ToLower(s.Operation) {
	case "", "find":
		fo := options.Find()
		if s.Limit > 0 {
			fo.SetLimit(int64(s.Limit))
		} else {
			fo.SetLimit(100)
		}
		if s.Sort != nil {
			sort, err := toBSON(s.Sort)
			if err != nil {
				return nil, fmt.Errorf("mongo.sort: %w", err)
			}
			fo.SetSort(sort)
		}
		cur, err := coll.Find(ctx, filter, fo)
		if err != nil {
			return nil, fmt.Errorf("mongo find: %w", err)
		}
		if err := cur.All(ctx, &docs); err != nil {
			return nil, fmt.Errorf("mongo find: %w", err)
		}
	case "count":
		count, err = coll.CountDocuments(ctx, filter)
		if err != nil {
			return nil, fmt.Errorf("mongo count: %w", err)
		}
	case "aggregate":
		pipeline := make([]bson.D, 0, len(s.Pipeline))
		for i, stage := range s.Pipeline {
			d, err := toBSON(stage)
			if err != nil {
				return nil, fmt.Errorf("mongo.pipeline[%d]: %w", i, err)
			}
			pipeline = append(pipeline, d)
		}
		cur, err := coll.Aggregate(ctx, pipeline)
		if err != nil {
			return nil, fmt.Errorf("mongo aggregate: %w", err)
		}
		if err := cur.All(ctx, &docs); err != nil {
			return nil, fmt.Errorf("mongo aggregate: %w", err)
		}
	default:
		return nil, fmt.Errorf("mongo.operation must be find, count or aggregate, not %q", s.Operation)
	}
	out := make([]json.RawMessage, 0, len(docs))
	for _, d := range docs {
		b, err := bson.MarshalExtJSON(d, false, false)
		if err != nil {
			return nil, fmt.Errorf("encode document: %w", err)
		}
		out = append(out, b)
	}
	if count < 0 {
		count = int64(len(out))
	}
	doc := mustJSON(map[string]any{"count": count, "documents": out})
	return &model.Response{Status: int(count), StatusText: fmt.Sprintf("%d document(s)", count), Body: doc, JSON: doc, DurationMs: time.Since(start).Milliseconds()}, nil
}
