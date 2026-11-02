package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func liveDomainsFixture(t *testing.T, dialect string) (IndependentStorageOptions, ManagedLiveOptions, func(ManagedLiveOptions) *ManagedLiveDomains) {
	t.Helper()
	o, opts, _ := liveDBFixture(t, dialect)
	opts.EndBlocks = map[uint64]uint64{1: 20, 10: 20}
	open := func(in ManagedLiveOptions) *ManagedLiveDomains {
		t.Helper()
		var d *ManagedLiveDomains
		var err error
		if dialect == "postgres" {
			d, err = OpenManagedLiveDomainsPostgres(context.Background(), o, in)
		} else {
			d, err = OpenManagedLiveDomainsPGlite(context.Background(), o, in)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		return d
	}
	return o, opts, open
}
func liveDomainsSeed(t *testing.T, d *ManagedLiveDomains) (ManagedLiveBatch, ManagedLiveDomainReorg) {
	t.Helper()
	ctx := context.Background()
	b := liveSegmentsUnitBatch(d.live())
	a, e := d.RetainBatch(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	var saved *Writer
	if _, e = d.ApplyBatch(ctx, b, a, liveBatchPGSharedHandler(t, &calls, &saved)); e != nil {
		t.Fatal(e)
	}
	c, e := d.Control(ctx)
	if e != nil {
		t.Fatal(e)
	}
	return b, ManagedLiveDomainReorg{Reorg: ManagedLiveReorg{ID: "domains-reorg", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Ancestor: b.Baselines[10]}, Orphaned: []ManagedLiveHead{b.Blocks[0].Head, b.Blocks[2].Head}}
}
func liveDomainsCacheSeed(t *testing.T, d *ManagedLiveDomains) {
	t.Helper()
	ctx := context.Background()
	five, six := uint64(5), uint64(6)
	if err := d.WithSync(ctx, func(s *SourceSyncStore) error {
		for _, chain := range []uint64{1, 10} {
			if err := s.InsertRPCRawRequestResults(ctx, chain, []SourceRPCRawResult{{Request: map[string]any{"method": "eth_chainId", "params": []any{}}, Result: "null-row"}, {Request: map[string]any{"method": "eth_getBalance", "params": []any{"0x123", "0x5"}}, BlockNumber: &five, Result: "five"}, {Request: map[string]any{"method": "eth_getBalance", "params": []any{"0x123", "0x6"}}, BlockNumber: &six, Result: "six"}}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func liveDomainsCacheCount(t *testing.T, d *ManagedLiveDomains, chain uint64, want int) {
	t.Helper()
	r, e := d.storage.syncBackend.Query(context.Background(), "SELECT count(*)::text AS n FROM "+d.storage.syncCache.table("rpc_request_results")+" WHERE chain_id=$1", chain)
	if e != nil || len(r.Rows) != 1 || r.Rows[0]["n"] != fmt.Sprint(want) {
		t.Fatalf("chain %d rpc count want %d: %v %v", chain, want, r.Rows, e)
	}
}

func TestManagedLiveDomainsDatabase(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			t.Run("exact_prune_global_suffix_and_idempotent_ack", func(t *testing.T) {
				ctx := context.Background()
				_, opts, open := liveDomainsFixture(t, dialect)
				d := open(opts)
				_, r := liveDomainsSeed(t, d)
				liveDomainsCacheSeed(t, d)
				result, e := d.CoordinateReorg(ctx, r)
				if e != nil || result.Outcome != "reorg" || result.Reorg == nil || len(result.Reorg.Removed) != 3 {
					t.Fatalf("coordinated=%+v err=%v", result, e)
				}
				liveBatchPGBalance(t, d.storage.businessBackend, d.storage.BusinessStore(), "missing")
				liveDomainsCacheCount(t, d, 10, 1)
				liveDomainsCacheCount(t, d, 1, 3)
				liveDomainsCacheSeed(t, d)
				again, e := d.CoordinateReorg(ctx, r)
				if e != nil || managedHash(again) != managedHash(result) {
					t.Fatalf("retry=%+v %v", again, e)
				}
				liveDomainsCacheCount(t, d, 10, 3)
				changed := r
				changed.Orphaned = changed.Orphaned[:1]
				if _, e = d.CoordinateReorg(ctx, changed); !errors.Is(e, ErrManagedLiveReceiptConflict) {
					t.Fatalf("changed retry=%v", e)
				}
				if ready, e := d.Ready(ctx); e != nil || ready {
					t.Fatalf("ready=%v %v", ready, e)
				}
			})
		})
	}
}
