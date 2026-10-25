package store

import (
	"context"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/schema"
	"strings"
	"testing"
	"time"
)

func TestORMIsolatedComposite(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			model := schema.Schema{Tables: []schema.Table{{Name: "account", PrimaryKey: []string{"chain_id", "address"}, Columns: []schema.Column{{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "address", Type: "hex", NotNull: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}
			s, e := New(ctx, b, model, fmt.Sprintf("ponder_go_orm_iso_%x", time.Now().UnixNano()), schema.Isolated)
			if e != nil {
				t.Fatal(e)
			}
			for _, chain := range []uint64{1, 10} {
				if e = s.Apply(ctx, event(fmt.Sprint(chain), chain, 1), func(w *Writer) error {
					_, e := w.Indexing().InsertOne(ctx, "account", Row{"chainId": int64(chain), "address": sourceZeroAddress, "balance": big.NewInt(int64(chain))}, Conflict{})
					return e
				}); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.Apply(ctx, event("scope", 1, 2), func(w *Writer) error {
				db := w.Indexing()
				other := Row{"chainId": int64(10), "address": sourceZeroAddress}
				if _, e := db.Find(ctx, "account", other); e == nil || !strings.Contains(e.Error(), "different chains") {
					return fmt.Errorf("cross-chain find allowed: %v", e)
				}
				if _, e := db.Find(ctx, "account", Row{"address": sourceZeroAddress}); e == nil || !strings.Contains(e.Error(), "must specify 'chainId'") {
					return fmt.Errorf("missing-chain key: %v", e)
				}
				if _, e := db.InsertOne(ctx, "account", Row{"chainId": int64(10), "address": sourceZeroAddress, "balance": big.NewInt(99)}, Conflict{}); e == nil {
					return fmt.Errorf("cross-chain insert allowed")
				}
				if _, e := db.Update(ctx, "account", other, Row{"balance": big.NewInt(99)}); e == nil {
					return fmt.Errorf("cross-chain update allowed")
				}
				if _, e := db.Delete(ctx, "account", other); e == nil {
					return fmt.Errorf("cross-chain delete allowed")
				}
				_, e := db.Update(ctx, "account", Row{"chainId": int64(1), "address": sourceZeroAddress}, Row{"balance": big.NewInt(2)})
				return e
			}); e != nil {
				t.Fatal(e)
			}
			if e = s.Revert(ctx, event("", 1, 1).Checkpoint); e != nil {
				t.Fatal(e)
			}
			if e = s.Apply(ctx, event("inspect", 1, 2), func(w *Writer) error {
				row, e := w.Indexing().Find(ctx, "account", Row{"chainId": int64(1), "address": sourceZeroAddress})
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 1)
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e = s.Apply(ctx, event("inspect10", 10, 2), func(w *Writer) error {
				row, e := w.Indexing().Find(ctx, "account", Row{"chainId": int64(10), "address": sourceZeroAddress})
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 10)
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
