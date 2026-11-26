package historical

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestHistoricalQueryLimit(t *testing.T) {
	for _, tc := range []struct {
		budget, chains, multichain, isolated int
	}{
		{12000, 2, 4006, 3006},
		{12, 2, 10, 9},
		{1, 2, 6, 6},
		{1, 1, 7, 7},
		{5, 3, 7, 7},
	} {
		for ordering, want := range map[schema.Ordering]int{schema.Multichain: tc.multichain, schema.Isolated: tc.isolated} {
			r := RuntimeRunner{
				build:   &config.ExecutableSourceIndexingBuild{Ordering: string(ordering), Chains: make([]filter.Object, tc.chains)},
				options: RuntimeOptions{Historical: Options{QuerySize: tc.budget}},
			}
			if got := r.historicalQueryLimit(); got != want {
				t.Errorf("%s with budget %d and %d chains: got %d rows, want %d", ordering, tc.budget, tc.chains, got, want)
			}
		}
	}
}

func TestRuntimeHistoricalQuerySizeDenseBlock(t *testing.T) {
	const logsPerChain = 21
	g := loadOmniLogsGolden(t)
	for i := range g.Chains {
		chain := &g.Chains[i]
		template := chain.Logs[0]
		chain.Logs = nil
		for j := 0; j < logsPerChain; j++ {
			log := maps.Clone(template)
			log["logIndex"] = fmt.Sprintf("0x%x", j)
			log["data"] = fmt.Sprintf("0x%064x", chain.ID*100+uint64(j))
			chain.Logs = append(chain.Logs, log)
		}
	}
	for _, backend := range []string{"postgres", "pglite"} {
		for _, ordering := range []schema.Ordering{schema.Multichain, schema.Isolated} {
			t.Run(backend+"/"+string(ordering), func(t *testing.T) {
				ctx := context.Background()
				p, _ := omniLogsRPC(t, g)
				first := true
				failure := errors.New("dense block callback interrupted")
				onLog := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					log, err := omniLogsFields(a.Event.Get("log"), "logIndex")
					if err != nil {
						return err
					}
					i, err := uintQuantity(log["logIndex"])
					if err != nil {
						return err
					}
					args, err := omniLogsFields(a.Event.Get("args"), "value")
					if err != nil {
						return err
					}
					id := finiteChain(a)
					_, err = a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(id), "ordinal": int(i), "value": fmt.Sprint(args["value"])}, store.Conflict{})
					if err == nil && first && id == 1 && i == 13 {
						return failure
					}
					return err
				})
				build := compileOmniLogsConfig(t, map[string]any{
					"ordering": string(ordering),
					"chains": map[string]any{
						"one": map[string]any{"id": 1, "rpc": p.server.URL + "/1", "cacheRpcRequests": true, "reorgWindow": 0},
						"two": map[string]any{"id": 2, "rpc": p.server.URL + "/2", "cacheRpcRequests": false, "reorgWindow": 0},
					},
					"contracts": map[string]any{"Token": map[string]any{
						"abi": []any{g.ABIItem}, "address": g.Address, "startBlock": 1, "endBlock": 1,
						"chain": map[string]any{"one": map[string]any{}, "two": map[string]any{}},
					}},
				}, []config.SourceIndexingFunction{{Name: "Token:Value", Fn: onLog}})
				f := newManagedFiniteFixture(t, backend, build)
				f.options.Owned.Storage.Ordering = ordering
				f.options.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{Name: "seen", PrimaryKey: []string{"chain_id", "ordinal"}, Columns: []schema.Column{
					{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "ordinal", Type: "integer", NotNull: true}, {Name: "value", Type: "text", NotNull: true},
				}}}}
				var epochs []uint64
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "dense-log", OpenID: "initial", SourceCheckpoints: true, Requesters: runtimeFactory(p, &epochs)}
				o.Historical.QuerySize = 12
				r := runtimeOpen(t, backend, o)
				out, err := r.Run(ctx)
				if err == nil || !strings.Contains(err.Error(), failure.Error()) || out.Completed {
					t.Fatalf("interrupted block result %+v, error %v", out, err)
				}
				if err = r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				seen := schema.Quote(f.options.Owned.Storage.BusinessNamespace) + ".seen"
				managedInspect(t, f, func(db store.Backend) {
					if rows := managedRows(t, db, "SELECT ordinal FROM "+seen+" WHERE chain_id=1"); len(rows) != 0 {
						t.Fatalf("interrupted block committed %d events", len(rows))
					}
				})
				first = false
				o.OpenID, o.Historical.QuerySize = "resume", 1
				r = runtimeOpen(t, backend, o)
				out, err = r.Run(ctx)
				if err != nil || !out.Ready || !out.Completed || !out.Recovered {
					t.Fatalf("resumed result %+v, error %v", out, err)
				}
				if err = r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT chain_id,ordinal,value FROM "+seen+" ORDER BY chain_id,ordinal")
					if len(rows) != 2*logsPerChain {
						t.Fatalf("received %d rows", len(rows))
					}
					for i, row := range rows {
						id, ordinal := i/logsPerChain+1, i%logsPerChain
						if fmt.Sprint(row["chain_id"]) != fmt.Sprint(id) || fmt.Sprint(row["ordinal"]) != fmt.Sprint(ordinal) || row["value"] != fmt.Sprint(id*100+ordinal) {
							t.Fatalf("unexpected row at %d: %v", i, row)
						}
					}
				})
			})
		}
	}
}
