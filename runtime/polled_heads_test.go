package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/interval"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestAtomicPolledHeads(t *testing.T) {
	reg := metrics.New()
	ctx := metrics.WithRealtime(t.Context(), reg)
	e := &Engine{chain: config.Chain{ID: 1, Name: "one"}}
	p := make(polledHeads)
	pollCtx := metrics.WithRealtime(ctx, nil)
	block := func(n uint64, hash string) Block {
		return Block{Number: Hex(n), Timestamp: Hex(n + 100), Hash: hash}
	}
	observe := func(b Block, gaps ...interval.Interval) {
		p.receive(pollCtx, e, b)
		p.retainMissing(1, gaps)
	}
	genesis, old, replacement, gap := block(0, "genesis"), block(3, "old"), block(3, "replacement"), block(2, "gap")
	observe(genesis, interval.Interval{0, 0})
	p.committed(1, genesis)
	pollCtx = ctx
	observe(genesis, interval.Interval{0, 0}) // Peer replay on the very first live poll.
	p.committed(1, genesis)
	observe(old, interval.Interval{1, 3})
	observe(replacement, interval.Interval{1, 3})
	p.committed(1, old) // A conflicting by-height response rejected this observation.
	time.Sleep(3 * time.Millisecond)
	receivedBefore := time.Now()
	observe(replacement, interval.Interval{1, 3})
	receivedAfter := time.Now()
	p.committed(1, gap)
	p.committed(1, block(2, replacement.Hash))
	before, err := reg.Collect(ctx)
	if err != nil || before != "" {
		t.Fatalf("unexpected samples before canonical tip commit: %s, %v", before, err)
	}
	time.Sleep(3 * time.Millisecond) // A new timer on a duplicate poll would move the arrival sample.
	observe(replacement, interval.Interval{3, 3})
	p.committed(1, replacement)
	observe(replacement, interval.Interval{3, 3})
	p.committed(1, replacement) // Peer replay or another poll of the same tip.
	ancestor := block(2, "ancestor")
	observe(ancestor) // A retreat to a block already present in the store.
	observe(ancestor, interval.Interval{2, 2})
	p.committed(1, ancestor)
	end := uint64(4)
	e.endBlock = &end
	above := block(6, "past-end")
	observe(above, interval.Interval{3, 4})
	p.committed(1, block(4, "end"))
	for _, name := range []string{"ponder_realtime_latency", "ponder_realtime_block_arrival_latency"} {
		body, err := reg.Collect(context.Background())
		if err != nil || !strings.Contains(body, name+`_count{chain="one"} 1`+"\n") {
			t.Fatalf("unexpected %s count: %s, %v", name, body, err)
		}
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "ponder_realtime_block_arrival_latency" {
			mined, _ := Number(replacement.Timestamp)
			arrival := family.Metric[0].GetHistogram().GetSampleSum() + float64(mined)*1000
			if arrival < float64(receivedBefore.UnixMilli()) || arrival > float64(receivedAfter.UnixMilli()) {
				t.Fatalf("arrival %f was not from the first poll of the replacement", arrival)
			}
		}
	}
}

func TestAtomicHandlerFailureHasNoHeadSample(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			endpoint := testanvil.StartChain(t, 1, "--no-mining")
			client := rpc.New(endpoint)
			backend := stagedBackend(t, ctx, dialect, "memory://")
			defer backend.Close()
			model := schema.Schema{Tables: []schema.Table{{Name: "entry", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
			db, err := store.New(ctx, backend, model, fmt.Sprintf("ponder_go_reject_head_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Chains: []config.Chain{{Name: "one", ID: 1, RPC: []string{endpoint}, PollingIntervalMS: 25}}, Blocks: []config.BlockSource{{Name: "Head", Chain: "one", StartBlock: 1, Interval: 1}}}
			reject := errors.New("reject the observed head")
			indexer, err := NewIndexer(cfg, db, map[string]Handler{"Head:block": func(ctx context.Context, c *Context) error {
				if _, err := c.DB.Indexing().InsertOne(ctx, "entry", store.Row{"id": "pending"}, store.Conflict{}); err != nil {
					return err
				}
				return reject
			}})
			if err != nil {
				t.Fatal(err)
			}
			reg := metrics.New()
			mined := false
			err = indexer.RunIndexing(metrics.WithChainSync(metrics.WithRealtime(ctx, reg), reg), func(ready bool) {
				if ready && !mined {
					mined = true
					var result any
					if err := client.Call(ctx, "evm_mine", []any{}, &result); err != nil {
						t.Fatal(err)
					}
				}
			})
			if !mined || !errors.Is(err, reject) {
				t.Fatalf("mined=%t indexing error=%v", mined, err)
			}
			body, err := reg.Collect(ctx)
			if err != nil || strings.Contains(body, "ponder_realtime_") || strings.Contains(body, `ponder_sync_block{chain="one"} 1`) {
				t.Fatalf("rejected head exposed metrics: %q, %v", body, err)
			}
			rows, err := db.Backend.Query(ctx, "SELECT id FROM "+schema.Quote(db.Namespace)+".entry")
			if err != nil || len(rows.Rows) != 0 {
				t.Fatalf("rolled-back rows=%v err=%v", rows.Rows, err)
			}
		})
	}
}
