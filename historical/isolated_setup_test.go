package historical

import (
	"context"
	"errors"
	"fmt"
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

func TestRuntimeIsolatedSetup(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			ctx := t.Context()
			p := newFiniteRPC(t, 5)
			var setups []uint64
			events := map[uint64]int{}
			fail := true
			setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				id := finiteChain(a)
				setups = append(setups, id)
				if a.Event != nil || a.Context.Contracts["Setup"] == nil {
					return errors.New("expected setup context")
				}
				balance, err := a.Context.Client.GetBalance(ctx, "0x1111111111111111111111111111111111111111")
				if err != nil || balance.Uint64() != 42 {
					return fmt.Errorf("setup balance on %d: %v, %v", id, balance, err)
				}
				key := store.Row{"chainId": int(3 - id), "id": "initial"}
				_, err = a.Context.DB.Find(ctx, "account", key)
				var denied *store.InvalidStoreAccessError
				if !errors.As(err, &denied) {
					return fmt.Errorf("unscoped setup access on chain %d: %v", id, err)
				}
				if _, err := a.Context.DB.InsertOne(ctx, "account", store.Row{"chainId": a.Context.Chain["id"], "id": "initial"}, store.Conflict{}); err != nil {
					return err
				}
				if fail && id == 1 {
					return errors.New("first chain setup failed")
				}
				return nil
			})
			handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				id := finiteChain(a)
				row, err := a.Context.DB.Find(ctx, "account", store.Row{"chainId": int(id), "id": "initial"})
				if err != nil || (row != nil) != (id != 3) {
					return fmt.Errorf("setup row on chain %d: %v, %v", id, row, err)
				}
				events[id]++
				return nil
			})
			chains := map[string]any{}
			all := map[string]any{}
			for id, name := range []string{"one", "two", "three"} {
				chains[name] = map[string]any{"id": id + 1, "rpc": fmt.Sprintf("%s/%d", p.server.URL, id+1), "reorgWindow": 0, "cacheRpcRequests": id != 1}
				all[name] = map[string]any{}
			}
			withSetup := map[string]any{"one": map[string]any{"startBlock": 1}, "two": map[string]any{"startBlock": 0}}
			b := compileOmniLogsConfig(t, map[string]any{
				"ordering": string(schema.Isolated), "chains": chains,
				"blocks":    map[string]any{"Blocks": map[string]any{"chain": all, "startBlock": 1, "endBlock": 2, "interval": 1}},
				"contracts": map[string]any{"Setup": map[string]any{"abi": []any{}, "chain": withSetup, "endBlock": 2}},
			}, []config.SourceIndexingFunction{{Name: "Setup:setup", Fn: setup}, {Name: "Blocks:block", Fn: handler}})
			f := newManagedFiniteFixture(t, backend, b)
			f.options.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{Name: "account", PrimaryKey: []string{"chain_id", "id"}, Columns: []schema.Column{{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "id", Type: "text", NotNull: true}}}}}
			o := RuntimeOptions{BuildID: "isolated-setup", Historical: runtimeFixtureInput(f), Requesters: func(context.Context, uint64) ([]rpc.Requester, error) {
				var req []rpc.Requester
				for _, chain := range b.Executable().Chains {
					req = append(req, rpc.New(fmt.Sprintf("%s/%d", p.server.URL, runtimeChainID(chain))))
				}
				return req, nil
			}}
			var observed int
			o.OnSetupCommitted = func(context.Context) error { observed++; return errors.New("stop after setup") }
			for pass := 1; pass <= 4; pass++ {
				o.OpenID = fmt.Sprintf("isolated-open-%d", pass)
				r := runtimeOpen(t, backend, o)
				v, err := r.Run(ctx)
				switch pass {
				case 1, 2:
					msg := "first chain setup failed"
					if pass == 2 {
						msg = "stop after setup"
					}
					if err == nil || !strings.Contains(err.Error(), msg) || v.Ready || v.Completed || len(events) > 0 {
						t.Fatalf("pass %d: %+v, events %v, error %v", pass, v, events, err)
					}
				default:
					if err != nil || !v.Ready || !v.Completed || !v.Recovered || (pass == 4 && v.HistoricalBlocks != 0) {
						t.Fatalf("pass %d: %+v, error %v", pass, v, err)
					}
				}
				if err := r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace)
					rows := managedRows(t, db, "SELECT chain_id::text AS id FROM "+ns+".account ORDER BY chain_id")
					state := managedRows(t, db, "SELECT chain_id::text AS id, p.checkpoint AS latest, f.checkpoint AS finalized, f.safe_checkpoint AS safe FROM "+ns+"._ponder_go_progress p FULL JOIN "+ns+"._ponder_go_finalized f USING(chain_id) ORDER BY chain_id")
					want := []string{"1", "2"}
					if pass == 1 {
						want = []string{"2"}
					}
					if len(rows) != len(want) || len(state) != len(want)+1 {
						t.Fatalf("pass %d: rows=%v state=%v", pass, rows, state)
					}
					for i, id := range want {
						if rows[i]["id"] != id {
							t.Fatalf("unexpected setup rows %v", rows)
						}
					}
					for i, id := range append(want, "3") {
						cp, err := checkpoint.Decode(fmt.Sprint(state[i]["latest"]))
						height := uint64(1)
						if pass >= 3 {
							height = 2
						}
						wantCP := checkpoint.Block(cp.BlockTimestamp, cp.ChainID, height, pass >= 3)
						if state[i]["id"] != id || err != nil || fmt.Sprint(cp.ChainID) != id || cp != wantCP || state[i]["safe"] != state[i]["latest"] || state[i]["finalized"] != state[i]["latest"] {
							t.Fatalf("pass %d: rows=%v state=%v", pass, rows, state)
						}
					}
				})
				fail = false
			}
			seenHeights := map[uint64]bool{}
			wantHeights := map[uint64]string{1: "0x1", 2: "0x0"}
			for _, call := range p.snapshot() {
				if call.Method != "eth_getBalance" {
					continue
				}
				if len(call.Params) != 2 || wantHeights[call.Chain] == "" || call.Params[1] != wantHeights[call.Chain] {
					t.Fatalf("setup balance read on chain %d used %v", call.Chain, call.Params)
				}
				seenHeights[call.Chain] = true
			}
			if !seenHeights[1] || !seenHeights[2] {
				t.Fatalf("missing setup RPC reads: %v", seenHeights)
			}
			if !slices.Equal(setups, []uint64{1, 2, 1}) || observed != 1 || events[1] != 2 || events[2] != 2 || events[3] != 2 {
				t.Fatalf("setup=%v events=%v observer=%d", setups, events, observed)
			}
		})
	}
}
