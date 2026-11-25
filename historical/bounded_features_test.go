package historical

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestBoundedRuntimeSetupAndCache(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, mode := range []string{"setup_cached", "setup_mixed", "uncached"} {
			t.Run(backend+"/"+mode, func(t *testing.T) { testBoundedRuntimeFeatures(t, backend, mode) })
		}
	}
}

func testBoundedRuntimeFeatures(t *testing.T, backend, mode string) {
	ctx := t.Context()
	continuousRoot(t)
	p := newFiniteRPC(t, 5)
	const address = "0x1111111111111111111111111111111111111111"
	fault := errors.New("second setup failed")
	var setup indexing.Handler
	var setupCalls []uint64
	failOnce := mode == "setup_mixed"
	if mode != "uncached" {
		setup = func(ctx context.Context, a *indexing.HandlerArgs) error {
			id := finiteChain(a)
			setupCalls = append(setupCalls, id)
			_, err := a.Context.DB.InsertOne(ctx, "account", store.Row{"id": fmt.Sprintf("setup%d", id), "balance": big.NewInt(1)}, store.Conflict{})
			if err == nil && failOnce && id == 2 {
				failOnce = false
				return fault
			}
			return err
		}
	}
	calls := map[uint64][]uint64{}
	h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
		id := finiteChain(a)
		if setup != nil {
			row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": fmt.Sprintf("setup%d", id)})
			if err != nil || row == nil {
				return fmt.Errorf("missing committed setup for %d: %v", id, err)
			}
		}
		n, err := continuousNumber(a)
		if err != nil {
			return err
		}
		value, err := a.Context.Client.GetBalance(ctx, address)
		if err != nil || value.Cmp(big.NewInt(42)) != 0 {
			return fmt.Errorf("incorrect RPC balance on %d at %d: %v (%v)", id, n, value, err)
		}
		calls[id] = append(calls[id], n)
		return finiteIncrement(ctx, a)
	})
	build := finiteBuild(t, p, 3, mode != "uncached", h, setup)
	var disabled []uint64
	for _, chain := range build.Executable().Chains {
		id := runtimeChainID(chain)
		if mode == "uncached" || (mode == "setup_mixed" && id == 1) {
			chain["cacheRpcRequests"] = false
			disabled = append(disabled, id)
		}
	}
	f := newManagedFiniteFixture(t, backend, build)
	var epochs []uint64
	opts := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "bounded-feature", OpenID: "initial", Requesters: runtimeFactory(p, &epochs)}
	r := runtimeOpen(t, backend, opts)
	for _, id := range disabled {
		uncachedSeed(t, r, id)
	}
	if failOnce {
		result, err := r.Run(ctx)
		if err == nil || !strings.Contains(err.Error(), fault.Error()) || result.Ready || len(calls) != 0 {
			t.Fatalf("setup failure %v %v, callbacks %v", result, err, calls)
		}
		if err := r.Close(ctx); err != nil {
			t.Fatal(err)
		}
		managedInspect(t, f, func(db store.Backend) {
			if rows := managedRows(t, db, "SELECT id FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account"); len(rows) != 0 {
				t.Fatal("uncommitted setup rows escaped", rows)
			}
		})
		opts.OpenID = "retry"
		r = runtimeOpen(t, backend, opts)
	}
	var firstCursors map[uint64]store.ManagedLiveHead
	for attempt := 0; attempt < 2; attempt++ {
		result, err := r.Run(ctx)
		if err != nil || !result.Ready || !result.Completed {
			t.Fatal("finite run", result, err)
		}
		want := []uint64{1, 2, 3}
		if !reflect.DeepEqual(calls[1], want) || !reflect.DeepEqual(calls[2], want) {
			t.Fatal("expected exactly the bounded blocks", calls)
		}
		var wantedSetup []uint64
		switch mode {
		case "setup_mixed":
			wantedSetup = []uint64{1, 2, 1, 2}
		case "setup_cached":
			wantedSetup = []uint64{1, 2}
		}
		if !reflect.DeepEqual(setupCalls, wantedSetup) {
			t.Fatal("setup callback sequence", setupCalls, wantedSetup)
		}
		state, err := r.Control(ctx)
		if err != nil || len(state.Cursors) != 2 {
			t.Fatal(state, err)
		}
		for id, head := range state.Cursors {
			cp, err := checkpoint.Decode(head.Checkpoint)
			if err != nil || cp.ChainID != id || cp.BlockNumber != 3 || head.Hash != finiteBlock(id, 3)["hash"] {
				t.Fatal("wrong end", id, head, err)
			}
		}
		if attempt == 0 {
			firstCursors = state.Cursors
		} else if !reflect.DeepEqual(firstCursors, state.Cursors) {
			t.Fatal("cursors changed on reopen")
		}
		if err := r.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if len(disabled) > 0 {
			uncachedRawInspect(t, f, disabled)
		}
		managedInspect(t, f, func(db store.Backend) {
			rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
			want := map[string]string{"chain1": "3", "chain2": "3"}
			if setup != nil {
				want["setup1"] = "1"
				want["setup2"] = "1"
			}
			if len(rows) != len(want) {
				t.Fatal(rows)
			}
			for _, row := range rows {
				if want[fmt.Sprint(row["id"])] != row["balance"] {
					t.Fatal("balance", row)
				}
			}
		})
		if attempt == 0 {
			opts.OpenID = "restart"
			r = runtimeOpen(t, backend, opts)
		}
	}
	if mode == "setup_mixed" {
		syncFixture := f
		syncFixture.options.Owned.Storage.BusinessDirectory = f.options.Owned.Storage.SyncDirectory
		managedInspect(t, syncFixture, func(db store.Backend) {
			ns := schema.Quote(f.options.Owned.Storage.SyncNamespace + "_rpc_1")
			rows := managedRows(t, db, "SELECT block_number::text AS height, result::jsonb #>> '{}' AS balance FROM "+ns+".rpc_request_results WHERE chain_id=$1 ORDER BY block_number", uint64(2))
			if len(rows) != 3 {
				t.Fatal("cached chain should retain one balance per indexed block", rows)
			}
			for i, row := range rows {
				if row["height"] != fmt.Sprint(i+1) || row["balance"] != "0x2a" {
					t.Fatal("cached chain balance", row)
				}
			}
		})
	}
}
