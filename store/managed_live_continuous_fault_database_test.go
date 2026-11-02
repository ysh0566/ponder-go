package store

import (
	"context"
	"errors"
	"ponder.local/ponder-go/checkpoint"
	"testing"
)

// These are native coordinator transport faults under the v6 format, separate
// from RuntimeRunner application-observer/OS.Exit boundaries. No source credit.
func continuousStoreFixture(t *testing.T, dialect string) (IndependentStorageOptions, ManagedLiveRuntimeOptions, func(ManagedLiveRuntimeOptions) *ManagedLiveDomains) {
	t.Helper()
	o, base, _ := liveDomainsFixture(t, dialect)
	starts := map[uint64]uint64{}
	for id, h := range base.FinalizedHeads {
		cp, err := checkpoint.Decode(h.Checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		starts[id] = cp.BlockNumber + 1
	}
	opts := ManagedLiveRuntimeOptions{ManagedLiveOptions: base, StartBlocks: starts, Continuous: true}
	open := func(in ManagedLiveRuntimeOptions) *ManagedLiveDomains {
		t.Helper()
		var d *ManagedLiveDomains
		var err error
		if dialect == "postgres" {
			d, err = OpenManagedLiveRuntimePostgres(context.Background(), o, in)
		} else {
			d, err = OpenManagedLiveRuntimePGlite(context.Background(), o, in)
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
func continuousStoreCacheSeed(t *testing.T, d *ManagedLiveDomains, io context.Context) {
	t.Helper()
	five, six := uint64(5), uint64(6)
	if err := d.WithRuntimeSync(io, func(ctx context.Context, s *SourceSyncStore) error {
		for _, id := range []uint64{1, 10} {
			if err := s.InsertRPCRawRequestResults(ctx, id, []SourceRPCRawResult{{Request: map[string]any{"method": "eth_chainId", "params": []any{}}, Result: "null-row"}, {Request: map[string]any{"method": "eth_getBalance", "params": []any{"0x123", "0x5"}}, BlockNumber: &five, Result: "five"}, {Request: map[string]any{"method": "eth_getBalance", "params": []any{"0x123", "0x6"}}, BlockNumber: &six, Result: "six"}}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestManagedLiveContinuousTransport(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, phase := range []string{"prepare", "business", "ack"} {
			for _, mode := range []string{"after_commit", "readback_failure"} {
				t.Run(dialect+"/"+phase+"_"+mode, func(t *testing.T) {
					ctx := context.Background()
					_, opts, open := continuousStoreFixture(t, dialect)
					d := open(opts)
					io, scope, err := d.NewRuntimeScope(ctx)
					if err != nil {
						t.Fatal(err)
					}
					batch := liveSegmentsUnitBatch(d.live())
					a, err := d.RetainBatch(ctx, batch)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = d.ApplyRuntimeBatch(io, batch, a, nil); err != nil {
						t.Fatal(err)
					}
					continuousStoreCacheSeed(t, d, io)
					if err = scope.Seal(ctx); err != nil {
						t.Fatal(err)
					}
					c, err := d.Control(ctx)
					if err != nil {
						t.Fatal(err)
					}
					request := ManagedLiveDomainReorg{Reorg: ManagedLiveReorg{ID: "continuous-native-reorg", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Ancestor: batch.Baselines[10]}, Orphaned: []ManagedLiveHead{batch.Blocks[0].Head, batch.Blocks[2].Head}}
					before, err := checkpointStates(ctx, d.storage.businessBackend, d.storage.BusinessStore())
					if err != nil {
						t.Fatal(err)
					}
					fault, restore := liveDomainsFault(d, phase, mode)
					result, err := d.CoordinateReorg(ctx, request)
					restore()
					if !fault.fired {
						t.Fatalf("native fault missed: %v", err)
					}
					liveDomainsCacheCount(t, d, 10, 1)
					liveDomainsCacheCount(t, d, 1, 3)
					if mode == "after_commit" {
						if err != nil || result.Outcome != "reorg" {
							t.Fatalf("authenticated lost ACK not reconciled: %+v %v", result, err)
						}
						io, scope, err = d.NewRuntimeScope(ctx)
						if err != nil {
							t.Fatal(err)
						}
						continuousStoreCacheSeed(t, d, io)
						if err = scope.Seal(ctx); err != nil {
							t.Fatal(err)
						}
						again, err := d.CoordinateReorg(ctx, request)
						if err != nil || managedHash(again) != managedHash(result) {
							t.Fatalf("exact lost-ACK retry changed: %+v %v", again, err)
						}
						liveDomainsCacheCount(t, d, 10, 3)
					} else {
						if err == nil {
							t.Fatal("unreadable native commit was treated as known success")
						}
						_, forbidden, e := d.NewRuntimeScope(ctx)
						if e == nil {
							_ = forbidden.Seal(ctx)
							t.Fatal("unknown commit admitted a new source scope")
						}
						want := ErrManagedLiveDomainsPending
						if phase == "business" {
							want = ErrManagedLiveUncertain
						}
						if !errors.Is(e, want) {
							t.Fatalf("wrong unknown fence: want=%v got=%v", want, e)
						}
						if err = d.Close(ctx); err != nil {
							t.Fatal(err)
						}
						opts.OpenID = "continuous-native-reopen"
						d = open(opts)
						result, err = d.CoordinateReorg(ctx, request)
						if err != nil {
							t.Fatalf("reopen did not reconcile exact pending request: %v", err)
						}
						liveDomainsCacheCount(t, d, 10, 1)
						liveDomainsCacheCount(t, d, 1, 1)
					}
					after, err := checkpointStates(ctx, d.storage.businessBackend, d.storage.BusinessStore())
					if err != nil || managedHash(after) != managedHash(before) {
						t.Fatalf("transport fault changed public projection before replay: %v %v err=%v", before, after, err)
					}
					c, err = d.Control(ctx)
					if err != nil || c.Serving || len(c.ReplayTargets) != 2 {
						t.Fatalf("v6 pending replay lost: %+v %v", c, err)
					}
					if yes, err := d.Ready(ctx); err != nil || yes {
						t.Fatalf("pending v6 replay ready: %v %v", yes, err)
					}
					ledger, err := d.readLedger(ctx, d.storage.syncBackend)
					if err != nil || ledger.Pending != "" || len(ledger.Intents) != 1 || len(ledger.Acks) != 1 {
						t.Fatalf("native reconciliation duplicated intent/ack: %+v %v", ledger, err)
					}
				})
			}
		}
	}
}

func TestManagedLiveContinuousNamespace(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			o, opts, open := continuousStoreFixture(t, dialect)
			d := open(opts)
			c, err := d.Control(ctx)
			if err != nil || c.Version != managedLiveContinuousVersion {
				t.Fatalf("new namespace version=%+v %v", c, err)
			}
			if err = d.Close(ctx); err != nil {
				t.Fatal(err)
			}
			legacy := opts
			legacy.Continuous = false
			legacy.OpenID = "v5-cannot-adopt-v6"
			var wrong *ManagedLiveDomains
			if dialect == "postgres" {
				wrong, err = OpenManagedLiveRuntimePostgres(ctx, o, legacy)
			} else {
				wrong, err = OpenManagedLiveRuntimePGlite(ctx, o, legacy)
			}
			if err == nil {
				_ = wrong.Close(ctx)
				t.Fatal("bounded-v5 adopted continuous-v6 storage")
			}
			opts.OpenID = "v6-after-refusal"
			d = open(opts)
			c, err = d.Control(ctx)
			if err != nil || c.Version != managedLiveContinuousVersion || c.Batches != 0 || c.Serving {
				t.Fatalf("failed v5 adoption mutated v6: %+v %v", c, err)
			}
		})
	}
}
