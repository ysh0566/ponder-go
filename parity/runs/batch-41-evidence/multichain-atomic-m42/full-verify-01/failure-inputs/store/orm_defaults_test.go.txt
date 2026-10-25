package store

import (
	"context"
	"errors"
	"math/big"
	"ponder.local/ponder-go/schema"
	"testing"
)

func TestUpstreamORMDefaults(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"default", "$default", "$onUpdateFn"} {
				t.Run(name, func(t *testing.T) {
					model := sourceAccountModel()
					column := &model.Tables[0].Columns[1]
					column.NotNull = false
					switch name {
					case "default":
						column.Type = "integer"
						column.Default = 0
					case "$default":
						column.DefaultFn = func() any { return big.NewInt(10) }
					case "$onUpdateFn":
						column.NotNull = true
						column.OnUpdateFn = func() any { return big.NewInt(10) }
					}
					sourceORMTransaction(t, dialect, model, false, func(ctx context.Context, db *IndexingStore) error {
						if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress}, Conflict{}); e != nil {
							return e
						}
						result, e := db.Find(ctx, "account", Row{"address": sourceZeroAddress})
						if e != nil {
							return e
						}
						want := any(big.NewInt(10))
						if name == "default" {
							want = int64(0)
						}
						expectSourceMatch(t, result, Row{"address": sourceZeroAddress, "balance": want})
						return nil
					})
				})
			}
		})
	}
}

func TestUpstreamORMFoundUpdate(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			sourceORMTransaction(t, dialect, sourceAccountModel(), true, func(ctx context.Context, db *IndexingStore) error {
				key := Row{"address": sourceZeroAddress}
				if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{}); e != nil {
					return e
				}
				row, e := db.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				row["balance"] = big.NewInt(20)
				if _, e = db.Update(ctx, "account", key, row); e != nil {
					return e
				}
				row, e = db.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				expectSourceMatch(t, row, Row{"balance": big.NewInt(20)})
				if e = db.Cache.Flush(ctx); e != nil {
					return e
				}
				db.Cache.Clear()
				db.Cache.Invalidate()
				row, e = db.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				expectSourceMatch(t, row, Row{"balance": big.NewInt(20)})
				return nil
			})
		})
	}
}

func TestUpstreamORMJSONBigInt(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "metadata", Type: "json"}}}}}
			sourceORMTransaction(t, dialect, model, true, func(ctx context.Context, db *IndexingStore) error {
				_, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "metadata": map[string]any{"balance": big.NewInt(10)}}, Conflict{})
				var expected *BigIntSerializationError
				if !errors.As(e, &expected) {
					t.Fatalf("source toBeInstanceOf(BigIntSerializationError): %T %v", e, e)
				}
				return nil
			})
		})
	}
}
