package historical

import (
	"context"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"testing"
	"time"
)

// Supplemental actual-runtime/native coverage; no translated-source credit.
func uncachedSeed(t *testing.T, r *RuntimeRunner, id uint64) {
	t.Helper()
	ctx, scope, err := r.domains.NewRuntimeScope(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Seal(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	err = r.domains.WithRuntimeSync(ctx, func(io context.Context, s *store.SourceSyncStore) error {
		raw := finiteBlock(id, 1)
		raw["hash"] = fmt.Sprintf("0x%064x", 987654321+id)
		if err := s.InsertBlocks(io, id, []map[string]any{raw}); err != nil {
			return err
		}
		for _, n := range []uint64{1, 3} {
			n := n
			if err := s.InsertRPCRawRequestResults(io, id, []store.SourceRPCRawResult{{Request: map[string]any{"method": "eth_getBalance", "params": []any{"0x" + strings.Repeat("1", 40), fmt.Sprintf("0x%x", n)}}, Result: "0xdead", BlockNumber: &n}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func uncachedRawInspect(t *testing.T, f finiteFixture, disabled []uint64) {
	t.Helper()
	syncf := f
	syncf.options.Owned.Storage.BusinessDirectory = f.options.Owned.Storage.SyncDirectory
	managedInspect(t, syncf, func(db store.Backend) {
		ns := schema.Quote(f.options.Owned.Storage.SyncNamespace + "_rpc_1")
		for _, id := range disabled {
			rows := managedRows(t, db, "SELECT hash FROM "+ns+".blocks WHERE chain_id=$1 AND number=1", id)
			if len(rows) != 1 || fmt.Sprint(rows[0]["hash"]) != fmt.Sprintf("0x%064x", 987654321+id) {
				t.Fatalf("disabled raw cache overwritten/read as canonical: %v", rows)
			}
			rows = managedRows(t, db, "SELECT result FROM "+ns+".rpc_request_results WHERE chain_id=$1 ORDER BY request_hash", id)
			if len(rows) != 2 {
				t.Fatalf("disabled RPC cache persisted or pruned: %v", rows)
			}
			for _, row := range rows {
				if fmt.Sprint(row["result"]) != "0xdead" {
					t.Fatalf("disabled sentinel changed: %v", rows)
				}
			}
			rows = managedRows(t, db, "SELECT number FROM "+ns+".blocks WHERE chain_id=$1", id)
			if len(rows) != 1 {
				t.Fatalf("disabled finality persisted raw blocks: %v", rows)
			}
		}
	})
}
func TestRuntimeUncachedHistoryLiveReopen(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, mode := range []string{"mixed", "all_disabled"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				continuousRoot(t)
				s := newContinuousRPC(t, 4)
				calls := map[uint64]int{}
				h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					value, err := a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40))
					if err != nil {
						return err
					}
					if value.Cmp(big.NewInt(42)) != 0 {
						return fmt.Errorf("stale RPC cache used: %s", value)
					}
					calls[finiteChain(a)]++
					return finiteIncrement(ctx, a)
				})
				b := runtimeBuild(t, s.p, 5, 1, h)
				disabled := []uint64{1}
				for i, c := range b.Executable().Chains {
					c["reorgWindow"] = uint64(2)
					if i == 0 || mode == "all_disabled" {
						c["cacheRpcRequests"] = false
					}
				}
				if mode == "all_disabled" {
					disabled = append(disabled, 2)
				}
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				var r *RuntimeRunner
				advanced := false
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "uncached-history-live", OpenID: "uncached-first", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnProgress: func(_ context.Context, e RuntimeLiveProgress) error {
					if e.Type == "block" && !advanced {
						advanced = true
						s.setLatest(5)
					}
					return nil
				}}}
				r = runtimeOpen(t, backend, o)
				for _, id := range disabled {
					uncachedSeed(t, r, id)
				}
				out, err := r.Run(context.Background())
				if err != nil || !out.Ready || !out.Completed || out.HistoricalBlocks != 4 {
					t.Fatalf("cachefalse history/live result=%+v calls=%v err=%v", out, calls, err)
				}
				c, err := r.Control(context.Background())
				if err != nil || c.Version != "managed-live-uncached-v7" || c.Finalizations == 0 {
					t.Fatalf("uncached durable finality=%+v %v", c, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				uncachedRawInspect(t, f, disabled)
				o.OpenID = "uncached-second"
				o.Live.OnProgress = nil
				r = runtimeOpen(t, backend, o)
				if ready, err := r.Ready(context.Background()); err != nil || ready {
					t.Fatalf("reopen ready before replay: %v %v", ready, err)
				}
				out, err = r.Run(context.Background())
				if err != nil || !out.Completed || !out.Ready || !out.Recovered {
					t.Fatalf("cachefalse recovery=%+v err=%v", out, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				uncachedRawInspect(t, f, disabled)
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
					if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "5" || fmt.Sprint(rows[1]["balance"]) != "5" {
						t.Fatalf("replay lost/duplicated real business rows: %v", rows)
					}
				})
				for _, id := range []uint64{1, 2} {
					if calls[id] < 5 {
						t.Fatalf("actual handlers missing: %v", calls)
					}
				}
			})
		}
	}
}
