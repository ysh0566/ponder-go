package store

import (
	"context"
	"fmt"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
)

func TestManagedLiveDomainsRetainedFork(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, opts, open := liveDomainsFixture(t, dialect)
			d := open(opts)
			batch, _ := liveDomainsSeed(t, d)
			c, e := d.Control(ctx)
			if e != nil {
				t.Fatal(e)
			}
			a := ManagedLiveDomainReorg{Reorg: ManagedLiveReorg{ID: "reorg-a", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Ancestor: batch.Baselines[1]}, Orphaned: []ManagedLiveHead{batch.Blocks[1].Head}}
			if _, e = d.CoordinateReorg(ctx, a); e != nil {
				t.Fatal(e)
			}
			c, e = d.Control(ctx)
			if e != nil {
				t.Fatal(e)
			}
			fresh := batch.Blocks[2]
			fresh.Head.Hash = fmt.Sprintf("0x%064x", 999999)
			fresh.InputDigest = managedHash("replacement-b6")
			replay := ManagedLiveBatch{ID: "replacement-b6", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Blocks: []ManagedLiveBlock{fresh}}
			acquired, e := d.RetainBatch(ctx, replay)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = d.ApplyBatch(ctx, replay, acquired, nil); e != nil {
				t.Fatal(e)
			}
			durable, _, e := d.business(ctx)
			if e != nil {
				t.Fatal(e)
			}
			competing := 0
			for _, h := range durable.Proofs {
				cp, _ := checkpoint.Decode(h.Checkpoint)
				if cp.ChainID == 10 && cp.BlockNumber == 6 {
					competing++
				}
			}
			if competing != 2 {
				t.Fatalf("counterexample requires both retained forks, got %d", competing)
			}
			c, e = d.Control(ctx)
			if e != nil {
				t.Fatal(e)
			}
			r := ManagedLiveDomainReorg{Reorg: ManagedLiveReorg{ID: "reorg-current-b", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Ancestor: batch.Blocks[0].Head}, Orphaned: []ManagedLiveHead{fresh.Head}}
			result, e := d.CoordinateReorg(ctx, r)
			if e != nil || result.Reorg == nil || len(result.Reorg.Removed) != 1 || result.Reorg.Removed[0].Block.Head != fresh.Head {
				t.Fatalf("current branch rejected by historical competing proof: %+v %v", result, e)
			}
			liveBatchPGBalance(t, d.storage.businessBackend, d.storage.BusinessStore(), "10")
		})
	}
}

func liveDomainsRawSnapshot(t *testing.T, d *ManagedLiveDomains) string {
	t.Helper()
	rows := [][]map[string]any{}
	for _, name := range []string{"blocks", "logs", "transactions", "transaction_receipts", "traces", "factory_addresses", "intervals"} {
		r, e := d.storage.syncBackend.Query(context.Background(), "SELECT row_to_json(raw_row.*)::text AS row FROM "+d.storage.syncCache.table(name)+" raw_row ORDER BY 1")
		if e != nil {
			t.Fatal(e)
		}
		rows = append(rows, r.Rows)
	}
	return managedHash(rows)
}
func TestManagedLiveDomainsReplayData(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, opts, open := liveDomainsFixture(t, dialect)
			d := open(opts)
			_, reorg := liveDomainsSeed(t, d)
			liveDomainsCacheSeed(t, d)
			inputs := map[string]map[string]any{}
			for _, v := range protocolVectors(t) {
				if v.Name == v.Table+"/full" {
					inputs[v.Table] = v.Input
				}
			}
			if e := d.WithSync(ctx, func(s *SourceSyncStore) error {
				for _, chain := range []uint64{1, 10} {
					for _, table := range []string{"blocks", "logs", "transactions", "transaction_receipts"} {
						input := filter.Object{}
						for k, v := range inputs[table] {
							input[k] = v
						}
						if table == "blocks" {
							input["number"] = "0x5"
						} else {
							input["blockNumber"] = "0x5"
						}
						var e error
						switch table {
						case "blocks":
							e = s.InsertBlocks(ctx, chain, []filter.Object{input})
						case "logs":
							e = s.InsertLogs(ctx, chain, []filter.Object{input})
						case "transactions":
							e = s.InsertTransactions(ctx, chain, []filter.Object{input})
						case "transaction_receipts":
							e = s.InsertTransactionReceipts(ctx, chain, []filter.Object{input})
						}
						if e != nil {
							return e
						}
					}
					if e := s.InsertIntervals(ctx, chain, []SourceFilterInterval{{Filter: filter.Object{"type": "block", "chainId": float64(chain), "interval": float64(1), "offset": float64(0)}, Interval: SourceInterval{0, 6}}}, nil); e != nil {
						return e
					}
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			before := liveDomainsRawSnapshot(t, d)
			if _, e := d.CoordinateReorg(ctx, reorg); e != nil {
				t.Fatal(e)
			}
			if after := liveDomainsRawSnapshot(t, d); after != before {
				t.Fatal("RPC prune destroyed raw replay/refetch data")
			}
		})
	}
}
