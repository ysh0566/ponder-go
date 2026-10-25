package store

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"ponder.local/ponder-go/schema"
)

func TestUpstreamORMErrors(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Run("update throw error when primary key is updated", func(t *testing.T) {
				sourceORMTransaction(t, dialect, sourceAccountModel(), false, func(ctx context.Context, db *IndexingStore) error {
					key := Row{"address": sourceZeroAddress}
					alice := "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
					if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{}); e != nil {
						return e
					}
					_, e := db.Update(ctx, "account", key, Row{"address": alice})
					var userError *NonRetryableUserError
					if !errors.As(e, &userError) {
						t.Fatalf("source direct PK update NonRetryableUserError: %T %v", e, e)
					}
					_, e = db.UpdateFunc(ctx, "account", key, func(Row) (Row, error) { return Row{"address": alice}, nil })
					if !errors.As(e, &userError) {
						t.Fatalf("source callback PK update NonRetryableUserError: %T %v", e, e)
					}
					row, e := db.Update(ctx, "account", key, Row{"address": sourceZeroAddress, "balance": big.NewInt(20)})
					if e != nil {
						return e
					}
					if row["address"] != sourceZeroAddress {
						t.Fatal("source unchanged primary key", row)
					}
					expectSourceBalance(t, row, 20)
					row, e = db.UpdateFunc(ctx, "account", key, func(Row) (Row, error) { return Row{"address": sourceZeroAddress, "balance": big.NewInt(30)}, nil })
					if e != nil {
						return e
					}
					if row["address"] != sourceZeroAddress {
						t.Fatal("source callback unchanged primary key", row)
					}
					expectSourceBalance(t, row, 30)
					return nil
				})
			})
			t.Run("missing rows", func(t *testing.T) {
				sourceORMTransaction(t, dialect, sourceAccountModel(), false, func(ctx context.Context, db *IndexingStore) error {
					_, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress}, Conflict{})
					if e == nil {
						t.Fatal("source missing required balance must reject")
					}
					return nil
				})
			})
			t.Run("onchain table", func(t *testing.T) {
				// A plain SQL descriptor is not registered as an onchain model in the
				// native schema. The typed fixture separately rejects its Bind call.
				offchain := schema.Table{Name: "account", Columns: []schema.Column{{Name: "address", Type: "text", Primary: true}, {Name: "balance", Type: "integer", NotNull: true}}}
				sourceORMTransaction(t, dialect, schema.Schema{}, false, func(ctx context.Context, db *IndexingStore) error {
					_, e := db.Find(ctx, offchain.Name, Row{"address": sourceZeroAddress})
					if e == nil {
						t.Fatal("source offchain access must reject")
					}
					return nil
				})
			})
			t.Run("notNull", func(t *testing.T) {
				model := sourceAccountModel()
				model.Tables[0].Columns[1].NotNull = false
				sourceORMTransaction(t, dialect, model, false, func(ctx context.Context, db *IndexingStore) error {
					key := Row{"address": sourceZeroAddress}
					row, e := db.InsertOne(ctx, "account", key, Conflict{})
					if e != nil {
						return e
					}
					expectSourceMatch(t, row, Row{"address": sourceZeroAddress, "balance": nil})
					row, e = db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					expectSourceMatch(t, row, Row{"address": sourceZeroAddress, "balance": nil})
					row, e = db.Update(ctx, "account", key, Row{})
					if e != nil {
						return e
					}
					expectSourceMatch(t, row, Row{"address": sourceZeroAddress, "balance": nil})
					// Like the source, replace only the cache/store schema, not SQL
					// tables. No flush occurs and setupDatabaseServices had no schema.
					previous := db.writer
					strictStore := &Store{Backend: previous.s.Backend, Namespace: previous.s.Namespace, SyncNamespace: previous.s.SyncNamespace, Ordering: previous.s.Ordering, Schema: sourceAccountModel()}
					strictWriter := &Writer{s: strictStore, tx: previous.tx, event: previous.event, encoded: previous.encoded, changed: map[string]bool{}}
					defer strictWriter.closed.Store(true)
					strict := strictWriter.Indexing(CacheOptions{Complete: true})
					if _, e = strict.InsertOne(ctx, "account", key, Conflict{}); e == nil {
						t.Fatal("source strict omitted balance must reject")
					}
					if _, e = strict.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": nil}, Conflict{}); e == nil {
						t.Fatal("source strict NULL balance must reject")
					}
					return nil
				})
			})
		})
	}
}
