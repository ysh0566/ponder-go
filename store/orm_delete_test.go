package store

import (
	"context"
	"math/big"
	"testing"
)

func TestUpstreamORMDelete(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			sourceORMTransaction(t, dialect, sourceAccountModel(), true, func(ctx context.Context, db *IndexingStore) error {
				key := Row{"address": sourceZeroAddress}
				deleted, e := db.Delete(ctx, "account", key)
				if e != nil {
					return e
				}
				if deleted {
					t.Fatal("source toBe(false)")
				}
				if _, e = db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(12)}, Conflict{}); e != nil {
					return e
				}
				if _, e = db.Update(ctx, "account", key, Row{"balance": big.NewInt(12)}); e != nil {
					return e
				}
				deleted, e = db.Delete(ctx, "account", key)
				if e != nil {
					return e
				}
				if !deleted {
					t.Fatal("source toBe(true)")
				}
				row, e := db.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				if row != nil {
					t.Fatal("source toBe(null)", row)
				}
				return nil
			})
		})
	}
}
