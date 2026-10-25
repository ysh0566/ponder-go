package store

import (
	"context"
	"math/big"
	"testing"
)

func TestUpstreamORMClosed(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			sourceORMTransaction(t, dialect, sourceAccountModel(), false, func(ctx context.Context, db *IndexingStore) error {
				// Source isProcessingEvents=false maps to closing the native scope.
				db.Close()
				_, e := db.InsertOne(ctx, "account", Row{"address": "0x0000000000000000000000000000000000000001", "balance": big.NewInt(90)}, Conflict{Action: ConflictUpdate, Set: Row{"balance": big.NewInt(16)}})
				if e == nil {
					t.Fatal("source toBeTruthy rejection after handler returned")
				}
				return nil
			})
		})
	}
}
