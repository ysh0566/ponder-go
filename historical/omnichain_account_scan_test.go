package historical

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestOmnichainAccountScan(t *testing.T) {
	chains := map[uint64]omniAccountChain{101: mineOmniAccountChain(t, 101, []uint64{10, 20, 30}), 202: mineOmniAccountChain(t, 202, []uint64{15, 25, 35})}
	for id, c := range chains {
		client := rpc.New(c.url)
		offset := uint64(0)
		if id == 202 {
			offset = 5
		}
		// The tail contains a reverted outgoing transaction from the watched wallet.
		const reverter = "0x3333333333333333333333333333333333333333"
		var out any
		if err := client.Call(t.Context(), "anvil_setCode", []any{reverter, "0x60006000fd"}, &out); err != nil {
			t.Fatal(err)
		}
		for _, ts := range []uint64{40, 50, 70} {
			var out any
			if err := client.Call(t.Context(), "evm_setNextBlockTimestamp", []any{1700000000 + ts + offset}, &out); err != nil {
				t.Fatal(err)
			}
			if ts == 40 {
				var hash string
				if err := client.Call(t.Context(), "eth_sendTransaction", []any{map[string]any{"from": c.other, "to": c.wallet, "value": "0x15", "gas": "0x5208"}}, &hash); err != nil {
					t.Fatal(err)
				}
				c.tx[4] = hash
			} else if ts == 50 {
				var hash string
				if err := client.Call(t.Context(), "eth_sendTransaction", []any{map[string]any{"from": c.wallet, "to": reverter, "value": "0x1", "gas": "0x186a0"}}, &hash); err != nil {
					t.Fatal(err)
				}
			}
			if err := client.Call(t.Context(), "evm_mine", []any{}, &out); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		backend string
		cached  bool
	}{{"postgres", true}, {"postgres", false}, {"pglite", true}, {"pglite", false}} {
		backend, cached := tc.backend, tc.cached
		t.Run(fmt.Sprintf("%s/cache-202-%t", backend, cached), func(t *testing.T) {
			continuousRoot(t)
			ctx := t.Context()
			var calls []string
			fail := true
			handler := func(kind string) indexing.Handler {
				return func(ctx context.Context, a *indexing.HandlerArgs) error {
					id := finiteChain(a)
					n, err := continuousNumber(a)
					if err != nil {
						return err
					}
					hash, err := factoryTransactionProperty(a, "transaction", "hash")
					if err != nil || hash != chains[id].tx[n] {
						return fmt.Errorf("transaction hash at %d/%d: %v, %v", id, n, hash, err)
					}
					key := fmt.Sprintf("%d/%d/%s", id, n, kind)
					row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": "shared"})
					if err != nil {
						return err
					}
					if row == nil {
						_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "value": key}, store.Conflict{})
					} else {
						_, err = a.Context.DB.Update(ctx, "account", store.Row{"id": "shared"}, store.Row{"value": fmt.Sprint(row["value"]) + "|" + key})
					}
					if err != nil {
						return err
					}
					calls = append(calls, key)
					if fail && key == "202/4/transfer:to" {
						return fmt.Errorf("interrupted sparse transfer")
					}
					return nil
				}
			}
			raw := fmt.Sprintf(`{"ordering":"omnichain","chains":{"one":{"id":101,"rpc":%q,"reorgWindow":0},"two":{"id":202,"rpc":%q,"reorgWindow":0,"cacheRpcRequests":%t}},"accounts":{"Wallet":{"chain":{"one":{},"two":{}},"address":%q,"startBlock":1,"endBlock":5,"includeTransactionReceipts":true}}}`, chains[101].url, chains[202].url, cached, chains[101].wallet)
			cfg, err := config.ParseSourceConfiguration([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 2})
			if err != nil {
				t.Fatal(err)
			}
			var handlers []config.SourceIndexingFunction
			for _, kind := range []string{"transaction:to", "transfer:to", "transaction:from", "transfer:from"} {
				handlers = append(handlers, config.SourceIndexingFunction{Name: "Wallet:" + kind, Fn: handler(kind)})
			}
			build, err := config.BuildSourceIndexing(ctx, cfg, base, handlers)
			if err != nil {
				t.Fatal(err)
			}
			f := newManagedFiniteFixture(t, backend, build)
			omnichainGoldenStorage(&f)
			opts := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "sparse-accounts", Requesters: func(context.Context, uint64) ([]rpc.Requester, error) {
				var req []rpc.Requester
				for _, c := range build.Executable().Chains {
					req = append(req, rpc.New(chains[runtimeChainID(c)].url))
				}
				return req, nil
			}}
			opts.Historical.QuerySize = 1
			var expected []string
			for _, n := range []int{1, 2, 4} {
				dir := "to"
				if n == 2 {
					dir = "from"
				}
				for _, id := range []int{101, 202} {
					for _, kind := range []string{"transaction", "transfer"} {
						expected = append(expected, fmt.Sprintf("%d/%d/%s:%s", id, n, kind, dir))
					}
				}
			}
			committed := 0
			var completedCursors map[uint64]string
			for _, owner := range []string{"failed", "recovered", "completed"} {
				opts.OpenID = owner
				r := runtimeOpen(t, backend, opts)
				calls = nil
				result, err := r.Run(ctx)
				if fail {
					if err == nil || !strings.Contains(err.Error(), "interrupted sparse transfer") || result.Ready || result.Completed || !slices.Equal(calls, expected) {
						t.Fatalf("interrupted: %+v, calls=%v, err=%v", result, calls, err)
					}
				} else {
					if err != nil || !result.Recovered || !result.Ready || !result.Completed || !slices.Equal(calls, expected[committed:]) {
						t.Fatalf("reopen: %+v, calls=%v, err=%v", result, calls, err)
					}
					ctrl, err := r.Control(ctx)
					if err != nil {
						t.Fatal(err)
					}
					last, err := checkpoint.Decode(ctrl.LogOnlyLastEvent)
					if err != nil || last.ChainID != 202 || last.BlockNumber != 4 || !last.MatchesEvent("transfer") {
						t.Fatalf("last callback: %+v, %v", last, err)
					}
					for id := range chains {
						end, err := checkpoint.Decode(ctrl.LogOnlyScanHeads[id].Checkpoint)
						logical, e := checkpoint.Decode(store.ManagedLiveCursorCheckpoint(ctrl, id))
						// An uncached group commits the block-end position at its last event block.
						validPosition := logical == end || logical.BlockNumber == 4 && logical.MatchesEvent("transfer")
						if id == 202 && !cached {
							validPosition = logical == checkpoint.Block(1700000045, id, 4, true)
						}
						if err != nil || e != nil || end.ChainID != id || end.BlockNumber != 5 || logical.ChainID != id || !validPosition {
							t.Fatalf("scan and source positions on %d: %+v %+v, err=%v %v", id, end, logical, err, e)
						}
					}
					if completedCursors != nil && !maps.Equal(ctrl.LogicalCursors, completedCursors) {
						t.Fatalf("cursors changed on completed reopen: %v -> %v", completedCursors, ctrl.LogicalCursors)
					}
					completedCursors = maps.Clone(ctrl.LogicalCursors)
				}
				if err := r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account")
					var got []string
					if len(rows) == 1 {
						got = strings.Split(fmt.Sprint(rows[0]["value"]), "|")
					} else if len(rows) > 1 {
						t.Fatal(rows)
					}
					if len(got) > len(expected) || !slices.Equal(got, expected[:len(got)]) || (fail && (len(got)%2 != 0 || len(got) > len(expected)-2)) || (!fail && len(got) != len(expected)) {
						t.Fatalf("unexpected committed account events: %v", got)
					}
					committed = len(got)
				})
				fail = false
			}
			syncStore := f
			syncStore.options.Owned.Storage.BusinessDirectory = f.options.Owned.Storage.SyncDirectory
			managedInspect(t, syncStore, func(db store.Backend) {
				ns := schema.Quote(f.options.Owned.Storage.SyncNamespace + "_rpc_1")
				for _, table := range []string{"intervals", "transactions", "traces", "transaction_receipts"} {
					rows := managedRows(t, db, "SELECT 1 FROM "+ns+"."+table+" WHERE chain_id=$1 LIMIT 1", 202)
					if (len(rows) > 0) != cached {
						t.Fatalf("cache-202=%t table %s, rows=%v", cached, table, rows)
					}
				}
			})
		})
	}
}
