package store

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"testing"
)

func TestUpstreamCacheEviction(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			sourceORMTransaction(t, dialect, sourceAccountModel(), true, func(ctx context.Context, db *IndexingStore) error {
				db.Cache.Invalidate()
				if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{}); e != nil {
					return e
				}
				if e := db.Cache.Flush(ctx); e != nil {
					return e
				}
				if e := db.Cache.Prefetch(ctx, nil); e != nil {
					return e
				}
				if e := db.Cache.Prefetch(ctx, nil); e != nil {
					return e
				}
				result, e := db.Cache.Has("account", Row{"address": sourceZeroAddress})
				if e != nil {
					return e
				}
				if result {
					t.Fatal("source prefetch eviction expect(false)")
				}
				return nil
			})
		})
	}
}

// These metadata cases use literal native events, not the unavailable source
// deploy/mint fixture, and earn no credit for that original prefetch test.
func TestNativeProfilePrefetch(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, exists := range []bool{false, true} {
				t.Run(fmt.Sprint(exists), func(t *testing.T) {
					sourceORMTransaction(t, dialect, sourceAccountModel(), true, func(ctx context.Context, db *IndexingStore) error {
						alice := "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
						bob := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
						if exists {
							if _, e := db.InsertOne(ctx, "account", Row{"address": bob, "balance": big.NewInt(7)}, Conflict{}); e != nil {
								return e
							}
							if e := db.Cache.Flush(ctx); e != nil {
								return e
							}
							db.Cache.Clear()
							db.Cache.Invalidate()
						}
						profile := ProfileEvent{Callback: "Erc20:Transfer", Count: 1, Type: "log", ChainID: 1, Data: Row{"args": Row{"to": alice}, "log": Row{}, "block": Row{"timestamp": big.NewInt(1)}, "transaction": Row{}}}
						if e := db.Cache.SetProfileEvent(profile); e != nil {
							return e
						}
						if _, e := db.InsertOne(ctx, "account", Row{"address": alice, "balance": new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)}, Conflict{Action: ConflictNothing}); e != nil {
							return e
						}
						profile.Data["args"].(Row)["to"] = bob
						if e := db.Cache.Flush(ctx); e != nil {
							return e
						}
						db.Cache.Invalidate()
						if e := db.Cache.Prefetch(ctx, []ProfileEvent{profile}); e != nil {
							return e
						}
						known, e := db.Cache.Has("account", Row{"address": bob})
						if e != nil {
							return e
						}
						if !known {
							return fmt.Errorf("prediction not loaded")
						}
						reads := db.Cache.tables["account"].diskReads
						row, e := db.Find(ctx, "account", Row{"address": bob})
						if e != nil {
							return e
						}
						if exists {
							expectSourceBalance(t, row, 7)
						} else if row != nil {
							return fmt.Errorf("missing prefetched key became row %v", row)
						}
						if reads != db.Cache.tables["account"].diskReads {
							return fmt.Errorf("prefetched lookup issued a disk read")
						}
						return nil
					})
				})
			}
		})
	}
}

func TestCacheConcurrentReadModifyWrite(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			sourceORMTransaction(t, dialect, sourceAccountModel(), true, func(ctx context.Context, db *IndexingStore) error {
				key := Row{"address": sourceZeroAddress}
				if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(0)}, Conflict{}); e != nil {
					return e
				}
				var wg sync.WaitGroup
				errors := make(chan error, 32)
				for i := 0; i < 32; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						row, e := db.UpdateFunc(ctx, "account", key, func(row Row) (Row, error) {
							return Row{"balance": new(big.Int).Add(row["balance"].(*big.Int), big.NewInt(1))}, nil
						})
						if e == nil {
							row["balance"].(*big.Int).SetInt64(-100)
						}
						errors <- e
					}()
				}
				wg.Wait()
				close(errors)
				for e := range errors {
					if e != nil {
						return e
					}
				}
				row, e := db.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 32)
				if e = db.Cache.Flush(ctx); e != nil {
					return e
				}
				db.Cache.Clear()
				db.Cache.Invalidate()
				row, e = db.Find(ctx, "account", key)
				if e != nil {
					return e
				}
				expectSourceBalance(t, row, 32)
				return nil
			})
		})
	}
}
