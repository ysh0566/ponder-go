package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/schema"
)

// Source interval end+1 encoding and native point intervals share physical
// storage but require distinct rewind boundaries, including cold ownership load.
func TestSourceIntervalRewind(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, cold := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cold_%t", dialect, cold), func(t *testing.T) {
				ctx := context.Background()
				backend := sourceBackend(t, ctx, dialect)
				db, err := New(ctx, backend, sourceActionsModel(), fmt.Sprintf("ponder_go_srcrw_%x", time.Now().UnixNano()), schema.Multichain)
				if err != nil {
					t.Fatal(err)
				}
				cache, err := db.EnableProtocolCache(ctx)
				if err != nil {
					t.Fatal(err)
				}
				source, err := cache.SourceSyncStore(0)
				if err != nil {
					t.Fatal(err)
				}
				f := filter.Object{"type": "block", "chainId": 1, "sourceId": "test", "interval": 1, "offset": 0, "include": []any{}, "hasTransactionReceipt": false}
				if err = source.InsertIntervals(ctx, 1, []SourceFilterInterval{{Filter: f, Interval: SourceInterval{1, 2}}}, nil); err != nil {
					t.Fatal(err)
				}
				if err = cache.transaction(ctx, func(tx Transaction) error {
					for _, block := range []uint64{1, 2} {
						if e := cache.complete(ctx, tx, 1, block, "runtime:control"); e != nil {
							return e
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if cold {
					db.protocol.Store(nil)
				}
				if err = db.RevertAndRewind(ctx, checkpoint.Block(1, 1, 1, true)); err != nil {
					t.Fatal(err)
				}
				values, err := source.GetIntervals(ctx, []filter.Object{f})
				if err != nil {
					t.Fatal(err)
				}
				spans, ok := values.Get(f)
				if !ok || len(spans) != 1 || !reflect.DeepEqual(spans[0].Intervals, []SourceInterval{{1, 1}}) {
					t.Fatalf("source interval retained block1 incorrectly: %+v", spans)
				}
				rows, err := backend.Query(ctx, "SELECT blocks::text AS blocks FROM "+cache.table("intervals")+" WHERE fragment_id=$1", "runtime:control")
				if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["blocks"] != "{[1,1]}" {
					t.Fatal("native point interval contract changed", rows, err)
				}
			})
		}
	}
}
