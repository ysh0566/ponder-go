package historical

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestOmnichainClosingCachePolicy(t *testing.T) {
	g := loadOmniLogsGolden(t)
	golden := omniLogsNamedCase(t, g, "default").Snapshots[0]
	failKey := omniLogsNamedCase(t, g, "paged_failure").FailKey
	for _, backend := range []string{"postgres", "pglite"} {
		for _, tc := range []struct {
			name     string
			disabled []uint64
		}{{"first-disabled", []uint64{1}}, {"second-disabled", []uint64{2}}, {"all-disabled", []uint64{1, 2}}} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				continuousRoot(t)
				ctx := context.Background()
				p, _ := omniLogsRPC(t, g)
				expected := golden.Calls
				var calls []string
				failed := false
				handlers := omniLogsHandlers(func() []omniLogsEvent { return expected }, &calls, func(key string) error {
					if !failed && key == failKey {
						failed = true
						return errors.New("controlled log rollback")
					}
					return nil
				})
				block := handlers["block"]
				handlers["block"] = func(ctx context.Context, a *indexing.HandlerArgs) error {
					n, err := continuousNumber(a)
					if err != nil {
						return err
					}
					got, err := a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40))
					if err != nil || got.Uint64() != n {
						return fmt.Errorf("chain %d block %d RPC result %v: %v", finiteChain(a), n, got, err)
					}
					return block(ctx, a)
				}
				build := omniLogsBuildWithSetup(t, g, p, handlers, 1, 3, true, nil, tc.disabled...)
				f := newManagedFiniteFixture(t, backend, build)
				omnichainGoldenStorage(&f)
				var epochs []uint64
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omni-cache-" + tc.name, Requesters: runtimeFactory(p, &epochs)}
				o.Historical.QuerySize = 6
				for pass := 0; pass < 3; pass++ {
					o.OpenID = fmt.Sprintf("open-%d", pass)
					calls = nil
					r := runtimeOpen(t, backend, o)
					if pass == 0 {
						for _, id := range tc.disabled {
							uncachedSeed(t, r, id)
						}
					}
					out, err := r.Run(ctx)
					if pass == 0 {
						if err == nil || !strings.Contains(err.Error(), "controlled log rollback") || out.Completed || out.Ready || len(calls) == 0 || calls[len(calls)-1] != failKey {
							t.Fatalf("unexpected failure boundary: result=%+v calls=%v err=%v", out, calls, err)
						}
					} else if err != nil || !out.Completed || !out.Ready || !out.Recovered || !slices.Equal(calls, omniLogsKeys(expected)) {
						t.Fatalf("pass %d: result=%+v calls=%v want=%v err=%v", pass, out, calls, omniLogsKeys(expected), err)
					}
					control, err := r.Control(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if err := r.Close(ctx); err != nil {
						t.Fatal(err)
					}
					uncachedRawInspect(t, f, tc.disabled)
					if pass == 0 {
						managedInspect(t, f, func(db store.Backend) {
							rows := managedRows(t, db, "SELECT value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'")
							var committed []string
							if len(rows) == 1 {
								committed = strings.Split(fmt.Sprint(rows[0]["value"]), "|")
							}
							if len(rows) > 1 || len(committed) >= len(calls) || !slices.Equal(committed, calls[:len(committed)]) {
								t.Fatalf("interrupted group escaped rollback: calls=%v rows=%v", calls, rows)
							}
							expected = golden.Calls[len(committed):]
						})
					} else {
						assertOmniLogsSnapshot(t, f, control, golden)
						expected = nil
						if pass == 1 && tc.name == "first-disabled" {
							changed := o
							changed.OpenID = "changed-policy"
							changed.Historical.Build = omniLogsBuild(t, g, p, handlers)
							open := OpenRuntimePostgres
							if backend == "pglite" {
								open = OpenRuntimePGlite
							}
							if owner, err := open(ctx, changed); err == nil || owner != nil {
								if owner != nil {
									_ = owner.Close(ctx)
								}
								t.Fatalf("reopened an existing mixed deployment with all chains cached: %v", err)
							}
						}
					}
				}
				syncFixture := f
				syncFixture.options.Owned.Storage.BusinessDirectory = f.options.Owned.Storage.SyncDirectory
				managedInspect(t, syncFixture, func(db store.Backend) {
					ns := schema.Quote(f.options.Owned.Storage.SyncNamespace + "_rpc_1")
					for _, chain := range g.Chains {
						logs := managedRows(t, db, "SELECT log_index FROM "+ns+".logs WHERE chain_id=$1", chain.ID)
						if slices.Contains(tc.disabled, chain.ID) {
							if len(logs) != 0 {
								t.Fatalf("disabled chain %d persisted logs", chain.ID)
							}
						} else {
							results := managedRows(t, db, "SELECT result FROM "+ns+".rpc_request_results WHERE chain_id=$1", chain.ID)
							if len(logs) == 0 || len(results) == 0 {
								t.Fatalf("enabled chain %d did not persist log/RPC cache", chain.ID)
							}
						}
					}
				})
			})
		}
	}
}

func TestOmnichainClosingUncachedRefetch(t *testing.T) {
	g := loadOmniLogsGolden(t)
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			ctx := context.Background()
			p, _ := omniLogsRPC(t, g)
			noop := indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { return nil })
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				block, ok := a.Event.Property("block")
				if !ok {
					return fmt.Errorf("missing block")
				}
				value, err := block.Get("gasUsed")
				if err != nil {
					return err
				}
				if n, err := uintQuantity(value); err != nil || n != 21000 {
					return fmt.Errorf("unexpected refetched gasUsed %v: %v", value, err)
				}
				return finiteIncrement(ctx, a)
			})
			build := omniLogsBuildWithSetup(t, g, p, map[string]indexing.Handler{"block": h, "A": noop, "B": noop}, 1, 3, true, nil, 1)
			for _, group := range build.Executable().EventCallbacks {
				for _, cb := range group {
					f := cb["filter"].(map[string]any)
					f["include"] = slices.DeleteFunc(slices.Clone(f["include"].([]any)), func(field any) bool {
						return field == "block.gasUsed"
					})
				}
			}
			f := newManagedFiniteFixture(t, backend, build)
			var epochs []uint64
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omni-uncached-refetch", OpenID: "open", Requesters: runtimeFactory(p, &epochs)}
			r := runtimeOpen(t, backend, o)
			uncachedSeed(t, r, 1)
			out, err := r.Run(ctx)
			if err != nil || !out.Completed || !out.Ready || out.Refetches < 1 {
				t.Fatalf("expected column refetch: %+v, %v", out, err)
			}
			// Exercise the memory retry against an authenticated block with overlapping logs.
			epoch, err := r.newEpoch(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer epoch.Abort(context.Background())
			chains, err := r.omnichainHistoricalSources(epoch.Context(), epoch)
			if err != nil {
				t.Fatal(err)
			}
			for _, chain := range chains {
				if chain.CacheRPCRequests {
					continue
				}
				for _, fixture := range g.Chains {
					if fixture.ID != chain.Local.ChainID {
						continue
					}
					head, err := runtimeHead(fixture.ID, fixture.Blocks[1])
					if err != nil {
						t.Fatal(err)
					}
					input := &runtimeAcquiredBlock{head: head, id: fixture.ID}
					events, err := r.refetchOmnichainBlock(epoch.Context(), chain, input)
					if err != nil || len(events) != 9 {
						t.Fatalf("uncached block refetch: events=%d err=%v", len(events), err)
					}
					input.head.Hash = "0x" + strings.Repeat("a", 64)
					if _, err := r.refetchOmnichainBlock(epoch.Context(), chain, input); err == nil || !strings.Contains(err.Error(), "differs from selected identity: hash") {
						t.Fatalf("refetch accepted a different canonical block: %v", err)
					}
				}
			}
			if err := epoch.Abort(ctx); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
			uncachedRawInspect(t, f, []uint64{1})
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || rows[0]["id"] != "chain1" || rows[1]["id"] != "chain2" || rows[0]["balance"] != "3" || rows[1]["balance"] != "3" {
					t.Fatalf("refetch replayed a committed write: %v", rows)
				}
			})
		})
	}
}
