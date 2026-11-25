package historical

import (
	"context"
	"fmt"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/store"
)

func TestBoundedFactorySources(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, kind := range []string{"log", "transaction", "transfer", "contract_call"} {
			t.Run(backend+"/"+kind, func(t *testing.T) {
				continuousRoot(t)
				var chain *continuousRPC
				var build *config.SourceIndexingBuild
				var fork func(uint64)
				var expected func(map[uint64]string) map[string]string
				switch kind {
				case "log":
					var replace func(uint64, uint64)
					chain, replace = factoryRPC(t, 4, 2, true)
					fork = func(id uint64) { replace(id, 2) }
					build = factoryBuild(t, chain.p, 1, 4, 600, true, true, boundedFactoryLog)
					expected = boundedFactoryLogs
				case "transaction":
					g := factoryTransactionGoldens(t)
					chain, fork = factoryTransactionRPC(t, 4, g)
					build = factoryTransactionBuild(t, chain.p, 3, 600, true, factoryTransactionOracleHandler(g))
					expected = func(branches map[uint64]string) map[string]string {
						return factoryTransactionExpected(g, branches, 3)
					}
				case "transfer":
					g := factoryTransferGoldens(t)
					chain, fork = factoryTransferRPC(t, 4, g)
					build = factoryTransferBuild(t, chain.p, 1, 600, true, factoryTransferOracleHandler(g))
					expected = func(branches map[uint64]string) map[string]string {
						return factoryTransferExpected(g, branches, 1)
					}
				case "contract_call":
					g := factoryTraceGoldens(t)
					chain, fork = factoryTraceRPC(t, 4, g)
					build = factoryTraceBuild(t, chain.p, g, 3, 600, true, true, factoryTraceHandler(g, true))
					expected = func(branches map[uint64]string) map[string]string {
						return factoryTraceExpected(g, branches, 3)
					}
				}
				build.Executable().Chains[1]["cacheRpcRequests"] = false
				fixture := newManagedFiniteFixture(t, backend, build)
				var epochs []uint64
				opts := RuntimeOptions{
					FactorySources: true, SourceCheckpoints: true, Historical: runtimeFixtureInput(fixture),
					BuildID: "bounded-factory-" + kind, Requesters: runtimeFactory(chain.p, &epochs),
				}
				branches := map[uint64]string{1: "old", 2: "old"}
				for attempt, owner := range []string{"original", "replacement"} {
					if attempt == 1 {
						// The first chain replaces the branch containing its child creation.
						// The other chain keeps its original children and business rows.
						fork(1)
						branches[1] = "fresh"
					}
					opts.OpenID = owner
					r := runtimeOpen(t, backend, opts)
					out, err := r.Run(t.Context())
					if err != nil || !out.Ready || !out.Completed || out.Recovered != (attempt == 1) {
						t.Fatalf("%s: result=%+v err=%v", owner, out, err)
					}
					control, err := r.Control(t.Context())
					if err != nil || len(control.Cursors) != 2 || len(control.ReplayTargets) != 0 {
						t.Fatalf("%s: control=%+v err=%v", owner, control, err)
					}
					for id, cursor := range control.Cursors {
						cp, err := checkpoint.Decode(cursor.Checkpoint)
						if err != nil || cp.ChainID != id || cp.BlockNumber != 4 || cursor.Hash != chain.canonical[id][4]["hash"] {
							t.Fatalf("%s: chain %d cursor=%+v checkpoint=%+v err=%v", owner, id, cursor, cp, err)
						}
					}
					if err := r.Close(t.Context()); err != nil {
						t.Fatal(err)
					}
					factoryBusinessRows(t, fixture, expected(branches))
				}
			})
		}
	}
}

func boundedFactoryLog(ctx context.Context, args *indexing.HandlerArgs) error {
	address, err := factoryTransactionProperty(args, "log", "address")
	if err != nil {
		return err
	}
	height, err := continuousNumber(args)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%d:%s:%d", finiteChain(args), address, height)
	_, err = args.Context.DB.InsertOne(ctx, "account", store.Row{"id": key, "balance": fmt.Sprint(height)}, store.Conflict{})
	return err
}

func boundedFactoryLogs(branches map[uint64]string) map[string]string {
	rows := map[string]string{}
	for id, branch := range branches {
		child, start := factoryChild, 2
		if branch == "fresh" {
			child, start = factoryOther, 3
		}
		for height := start; height <= 4; height++ {
			rows[fmt.Sprintf("%d:%s:%d", id, child, height)] = fmt.Sprint(height)
		}
	}
	return rows
}

func TestBoundedFactoryFinalizedBirth(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			chain, _ := factoryRPC(t, 5, 1, true)
			build := factoryBuild(t, chain.p, 3, 5, 1, true, true, boundedFactoryLog)
			build.Executable().Chains[1]["cacheRpcRequests"] = false
			fixture := newManagedFiniteFixture(t, backend, build)
			var epochs []uint64
			opts := RuntimeOptions{Historical: runtimeFixtureInput(fixture), BuildID: "bounded-finalized-birth",
				FactorySources: true, SourceCheckpoints: true, Requesters: runtimeFactory(chain.p, &epochs)}
			want := map[string]string{}
			for _, id := range []uint64{1, 2} {
				for n := 3; n <= 5; n++ {
					want[fmt.Sprintf("%d:%s:%d", id, factoryChild, n)] = fmt.Sprint(n)
				}
			}
			// The factory's creation interval ends at three. The final replayed
			// block is beyond it, but must still match the child born at one.
			for _, owner := range []string{"initial", "reopen"} {
				opts.OpenID = owner
				r := runtimeOpen(t, backend, opts)
				out, err := r.Run(t.Context())
				if err != nil || !out.Completed || !out.Ready || out.Recovered != (owner == "reopen") {
					t.Fatalf("%s: result=%+v err=%v", owner, out, err)
				}
				if err := r.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				factoryBusinessRows(t, fixture, want)
			}
		})
	}
}
