package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestManagedLiveUncachedTransport(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, phase := range []string{"prepare", "business", "ack"} {
			for _, mode := range []string{"after_commit", "readback_failure"} {
				t.Run(dialect+"/"+phase+"_"+mode, func(t *testing.T) {
					ctx := context.Background()
					_, opts, open := continuousStoreFixture(t, dialect)
					opts.CacheRPCRequests = map[uint64]bool{1: true, 10: false}
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
					liveDomainsCacheCount(t, d, 10, 3)
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
						liveDomainsCacheCount(t, d, 10, 3)
						liveDomainsCacheCount(t, d, 1, 1)
					}
					after, err := checkpointStates(ctx, d.storage.businessBackend, d.storage.BusinessStore())
					if err != nil || managedHash(after) != managedHash(before) {
						t.Fatalf("transport fault changed public projection before replay: %v %v err=%v", before, after, err)
					}
					c, err = d.Control(ctx)
					if err != nil || c.Version != "managed-live-uncached-v7" || c.Serving || len(c.ReplayTargets) != 2 {
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

func TestManagedLiveUncachedNamespace(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			o, opts, open := continuousStoreFixture(t, dialect)
			opts.CacheRPCRequests = map[uint64]bool{1: true, 10: false}
			d := open(opts)
			if d.live().version() != managedLiveUncachedVersion {
				t.Fatal("missing independent cache policy version")
			}
			opts.CacheRPCRequests[10] = true
			if runtimeRPCCacheEnabled(d.live().opts, 10) {
				t.Fatal("caller mutated live cache policy")
			}
			opts.CacheRPCRequests[10] = false
			for _, bad := range []map[uint64]bool{{1: false}, {1: false, 10: true, 99: false}, {1: false, 99: true}} {
				in := opts
				in.CacheRPCRequests = bad
				var x *ManagedLiveDomains
				var err error
				if dialect == "postgres" {
					x, err = OpenManagedLiveRuntimePostgres(ctx, o, in)
				} else {
					x, err = OpenManagedLiveRuntimePGlite(ctx, o, in)
				}
				if err == nil {
					_ = x.Close(ctx)
					t.Fatal("invalid cache policy opened storage")
				}
				if !strings.Contains(err.Error(), "RPC cache") {
					t.Fatalf("policy validation happened after storage admission: %v", err)
				}
			}
			if err := d.Close(ctx); err != nil {
				t.Fatal(err)
			}
			for i, policy := range []map[uint64]bool{nil, {1: false, 10: true}} {
				in := opts
				in.OpenID = fmt.Sprintf("wrong-cache-policy-%d", i)
				in.CacheRPCRequests = policy
				var x *ManagedLiveDomains
				var err error
				if dialect == "postgres" {
					x, err = OpenManagedLiveRuntimePostgres(ctx, o, in)
				} else {
					x, err = OpenManagedLiveRuntimePGlite(ctx, o, in)
				}
				if err == nil {
					_ = x.Close(ctx)
					t.Fatal("cache policy/format change adopted old namespace")
				}
			}
			opts.OpenID = "uncached-namespace-reopen"
			d = open(opts)
			c, err := d.Control(ctx)
			if err != nil || c.Version != managedLiveUncachedVersion || c.Serving || c.Batches != 0 {
				t.Fatalf("rejected policies mutated namespace: %+v %v", c, err)
			}
		})
	}
}
func TestManagedLiveUncachedIdentity(t *testing.T) {
	raw := []byte(`{"Plan":"fixed"}`)
	opts := ManagedLiveOptions{runtimeStarts: map[uint64]uint64{1: 1, 10: 1}}
	legacy, err := runtimeIdentityPayload(raw, opts.runtimeStarts)
	if err != nil {
		t.Fatal(err)
	}
	same, err := runtimeOptionsIdentityPayload(raw, opts)
	if err != nil || string(same) != string(legacy) {
		t.Fatal("legacy identity bytes changed", err)
	}
	opts.runtimeCachePolicy = map[uint64]bool{1: true, 10: false}
	a, err := runtimeOptionsIdentityPayload(raw, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.runtimeCachePolicy = map[uint64]bool{1: false, 10: true}
	b, err := runtimeOptionsIdentityPayload(raw, opts)
	if err != nil || string(a) == string(b) || string(a) == string(legacy) {
		t.Fatal("cache policy absent from durable identity", err)
	}
	for _, raw := range []string{`{}`, `not-json`, `{"chain_id":99}`, `{"chain_id":1}`} {
		if runtimeCacheDeletedAllowed(opts, []string{raw}) {
			t.Fatalf("uncached/invalid deletion proof admitted: %s", raw)
		}
	}
	if !runtimeCacheDeletedAllowed(opts, []string{`{"chain_id":10}`}) {
		t.Fatal("enabled cache deletion rejected")
	}
}

// Validate the semantic deletion boundary independently of authenticated hashes.
func TestManagedLiveUncachedDeletionBounds(t *testing.T) {
	m, _ := liveSegmentsUnitOwner(t)
	b := liveSegmentsUnitBatch(m)
	in := ManagedLiveDomainReorg{Reorg: ManagedLiveReorg{Ancestor: b.Baselines[10]}, Orphaned: []ManagedLiveHead{b.Blocks[0].Head, b.Blocks[2].Head}}
	o := ManagedLiveOptions{runtimeCachePolicy: map[uint64]bool{1: false, 10: true}}
	good := []string{`{"chain_id":10,"block_number":5}`, `{"chain_id":10,"block_number":6}`}
	if !runtimeReorgDeletedAllowed(o, in, good) || !runtimeFenceDeletedAllowed(o, map[uint64]uint64{1: 4, 10: 4}, good) {
		t.Fatal("valid enabled-chain suffix rejected")
	}
	for _, raw := range []string{`{}`, `{"chain_id":10,"block_number":null}`, `{"chain_id":10,"block_number":4}`, `{"chain_id":10,"block_number":7}`, `{"chain_id":1,"block_number":5}`, `{"chain_id":99,"block_number":5}`} {
		if runtimeReorgDeletedAllowed(o, in, []string{raw}) {
			t.Fatalf("out-of-request deletion admitted: %s", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"chain_id":10,"block_number":null}`, `{"chain_id":10,"block_number":4}`, `{"chain_id":1,"block_number":5}`, `{"chain_id":99,"block_number":5}`} {
		if runtimeFenceDeletedAllowed(o, map[uint64]uint64{1: 4, 10: 4}, []string{raw}) {
			t.Fatalf("unsafe generation deletion admitted: %s", raw)
		}
	}
	if !runtimeFenceDeletedAllowed(o, map[uint64]uint64{10: 4}, []string{`{"chain_id":10,"block_number":7}`}) {
		t.Fatal("generation suffix above cut rejected")
	}
	if !runtimeReorgDeletedAllowed(ManagedLiveOptions{}, in, []string{"legacy"}) || !runtimeFenceDeletedAllowed(ManagedLiveOptions{}, nil, []string{"legacy"}) {
		t.Fatal("legacy proof format changed")
	}
}
