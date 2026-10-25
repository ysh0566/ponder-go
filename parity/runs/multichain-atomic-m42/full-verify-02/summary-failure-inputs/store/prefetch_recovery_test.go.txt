package store

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
)

func TestProfileAcrossRollbackAndReorg(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			s, e := New(ctx, b, sourceAccountModel(), fmt.Sprintf("ponder_go_profile_%x", time.Now().UnixNano()), schema.Multichain)
			if e != nil {
				t.Fatal(e)
			}
			key := Row{"address": sourceZeroAddress}
			profile := ProfileEvent{Callback: "Token:Transfer", Count: 1, Type: "log", ChainID: 1, Data: Row{"args": Row{"to": sourceZeroAddress}}}
			if e = s.Apply(ctx, event("first", 1, 1), func(w *Writer) error {
				db := w.Indexing()
				if e := db.Cache.SetProfileEvent(profile); e != nil {
					return e
				}
				_, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{Action: ConflictNothing})
				return e
			}); e != nil {
				t.Fatal(e)
			}
			profile.Count = 2
			e = s.Apply(ctx, event("abort", 1, 2), func(w *Writer) error {
				db := w.Indexing()
				if e := db.Cache.SetProfileEvent(profile); e != nil {
					return e
				}
				if e := db.Cache.Prefetch(ctx, []ProfileEvent{profile}); e != nil {
					return e
				}
				known, e := db.Cache.Has("account", key)
				if e != nil {
					return e
				}
				if !known {
					return fmt.Errorf("previous-event profile was lost")
				}
				row, e := db.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 10)
				if _, e = db.Update(ctx, "account", key, Row{"balance": big.NewInt(100)}); e != nil {
					return e
				}
				if e = db.Cache.Flush(ctx); e != nil {
					return e
				}
				return fmt.Errorf("injected rollback")
			})
			if e == nil {
				t.Fatal("failed event committed")
			}
			if e = s.Apply(ctx, event("after-abort", 1, 2), func(w *Writer) error {
				db := w.Indexing()
				if e := db.Cache.SetProfileEvent(profile); e != nil {
					return e
				}
				if e := db.Cache.Prefetch(ctx, []ProfileEvent{profile}); e != nil {
					return e
				}
				row, e := db.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 10)
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e = s.Revert(ctx, event("", 1, 0).Checkpoint); e != nil {
				t.Fatal(e)
			}
			profile.Count = 3
			if e = s.Apply(ctx, event("after-reorg", 1, 1), func(w *Writer) error {
				db := w.Indexing()
				if e := db.Cache.SetProfileEvent(profile); e != nil {
					return e
				}
				if e := db.Cache.Prefetch(ctx, []ProfileEvent{profile}); e != nil {
					return e
				}
				known, e := db.Cache.Has("account", key)
				if e != nil {
					return e
				}
				if !known {
					return fmt.Errorf("negative prediction not cached")
				}
				row, e := db.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				if row != nil {
					return fmt.Errorf("orphan business row leaked through profile: %v", row)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestCacheMemoryBudget(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			sourceORMTransaction(t, dialect, sourceAccountModel(), true, func(ctx context.Context, db *IndexingStore) error {
				db.Cache.maxBytes = 1
				if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{}); e != nil {
					return e
				}
				if e := db.Cache.Flush(ctx); e != nil {
					return e
				}
				if e := db.Cache.Prefetch(ctx, nil); e != nil {
					return e
				}
				if db.Cache.tables["account"].complete {
					return fmt.Errorf("cache completeness survived budget eviction")
				}
				known, e := db.Cache.Has("account", Row{"address": sourceZeroAddress})
				if e != nil {
					return e
				}
				if known {
					return fmt.Errorf("budget eviction retained row")
				}
				row, e := db.Find(ctx, "account", Row{"address": sourceZeroAddress})
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 10)
				return nil
			})
		})
	}
}
