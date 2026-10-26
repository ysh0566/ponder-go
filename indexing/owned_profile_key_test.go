package indexing

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"ponder.local/ponder-go/store"
)

// Numeric-string primary keys are legal user inputs. A learned block.number
// prediction is a BigInt internally; both hint comparison and real SQL prefetch
// must adapt it without allowing public callers to pass a BigInt to text.
func TestOwnedProfileRecoveredTextPrefetch(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			options, chains, cb := multiBuild(func(run context.Context, args *HandlerArgs) error {
				p, _ := args.Event.Property("block")
				number, err := p.Get("number")
				if err != nil {
					return err
				}
				id := fmt.Sprint(number)
				if _, err := args.Context.DB.InsertOne(run, "account", store.Row{"id": id, "balance": big.NewInt(1)}, store.Conflict{}); err != nil {
					return err
				}
				row, err := args.Context.DB.Find(run, "account", store.Row{"id": id})
				if err != nil || row == nil {
					return fmt.Errorf("training read: %v %v", row, err)
				}
				return nil
			})
			options.Build.SetupCallbacks = [][]Object{{{"name": "Token:setup", "chain": chains[0], "block": 2, "fn": Handler(func(run context.Context, args *HandlerArgs) error {
				_, err := args.Context.DB.InsertOne(run, "account", store.Row{"id": "5", "balance": big.NewInt(5)}, store.Conflict{})
				return err
			})}}}
			owner := multiFixture(t, dialect, options).start(t)
			if err := owner.InitializeMultichain(ctx, multiInitial()); err != nil {
				t.Fatal(err)
			}
			if applied, err := owner.HistoricalGroup(ctx, multiCP(1, 4), []Object{multiEvent(chains[0], cb, 3), multiEvent(chains[0], cb, 4)}, nil); err != nil || !applied {
				t.Fatal(applied, err)
			}
			if err := owner.Invalidate(ctx); err != nil {
				t.Fatal(err)
			}
			events, err := owner.Service().ProfileEvents([]Object{multiEvent(chains[0], cb, 5)})
			if err != nil {
				t.Fatal(err)
			}
			err = owner.Database().WithReadOnly(ctx, 1, func(read context.Context) error {
				underlying, release, err := owner.Database().acquire(read)
				if err != nil {
					return err
				}
				defer release()
				if cached, err := underlying.Cache.Has("account", store.Row{"id": "5"}); err != nil || cached {
					return fmt.Errorf("prediction was preseeded: %v %v", cached, err)
				}
				if err := owner.Database().Prefetch(read, events); err != nil {
					return err
				}
				if cached, err := underlying.Cache.Has("account", store.Row{"id": "5"}); err != nil || !cached {
					return fmt.Errorf("real prediction not loaded: %v %v", cached, err)
				}
				row, err := owner.Database().Find(read, "account", store.Row{"id": "5"})
				if err != nil || row == nil || row["id"] != "5" || fmt.Sprint(row["balance"]) != "5" {
					return fmt.Errorf("prefetch row: %v %v", row, err)
				}
				if _, err := owner.Database().Find(read, "account", store.Row{"id": big.NewInt(5)}); err == nil {
					return fmt.Errorf("public bigint text key accepted")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
