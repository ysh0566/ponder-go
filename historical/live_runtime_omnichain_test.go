package historical

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"testing"
	"time"

	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

func omnichainRuntimeBuild(t *testing.T, p *finiteRPC, end uint64, h indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	raw := fmt.Sprintf(`{"ordering":"omnichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":true},"two":{"id":2,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":true}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":%d,"interval":1}}}`, p.server.URL+"/1", p.server.URL+"/2", end)
	cfg, err := config.ParseSourceConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range base.RPCClients() {
		r.RequestsPerSecond = 0
		r.Retries = 0
	}
	b, err := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "Blocks:block", Fn: h}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRuntimeOmnichainFinalizedOrderAndReopen(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			if root == "" {
				t.Fatal("retained root required")
			}
			t.Setenv("PONDER_M44_TEST_ROOT", root)
			p := newFiniteRPC(t, 5)
			calls := []uint64{}
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				id := finiteChain(a)
				row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": "shared"})
				if err != nil {
					return err
				}
				v := big.NewInt(0)
				if row != nil {
					var ok bool
					v, ok = new(big.Int).SetString(fmt.Sprint(row["balance"]), 10)
					if !ok {
						return fmt.Errorf("invalid bigint")
					}
				}
				v.Mul(v, big.NewInt(10))
				v.Add(v, new(big.Int).SetUint64(id))
				if row == nil {
					_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "balance": v}, store.Conflict{})
				} else {
					_, err = a.Context.DB.Update(ctx, "account", store.Row{"id": "shared"}, store.Row{"balance": v})
				}
				calls = append(calls, id)
				return err
			})
			b := omnichainRuntimeBuild(t, p, 3, h)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omnichain-finalized-v1", OpenID: "omni-1", Requesters: runtimeFactory(p, &epochs)}
			r := runtimeOpen(t, backend, o)
			control, err := r.domains.Control(context.Background())
			if err != nil || control.Version != "managed-live-omnichain-finalized-v11" {
				t.Fatalf("wrong persistent omnichain format: %+v %v", control, err)
			}
			p.setHook(func(ctx context.Context, c finiteRPCRequest) error {
				if c.Chain == 1 && c.Method == "eth_getBlockByNumber" && len(c.Params) > 1 && c.Params[0] == "0x1" && c.Params[1] == true {
					select {
					case <-time.After(150 * time.Millisecond):
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			})
			out, err := r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed {
				t.Fatalf("omnichain run %+v %v", out, err)
			}
			want := []uint64{1, 2, 1, 2, 1, 2}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("timestamp tie must order by chain ID, got %v want %v", calls, want)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'")
				if len(rows) != 1 || rows[0]["balance"] != "121212" {
					t.Fatalf("shared business result %v", rows)
				}
			})
			o.OpenID = "omni-2"
			r = runtimeOpen(t, backend, o)
			out, err = r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || !out.Recovered {
				t.Fatalf("omnichain reopen %+v %v", out, err)
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("finalized handlers replayed after reopen: %v", calls)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
