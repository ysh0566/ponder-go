package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type liveDomainFaultBackend struct {
	Backend
	phase, mode string
	fired       bool
}
type liveDomainFaultTx struct {
	Transaction
	b      *liveDomainFaultBackend
	target bool
}

func (b *liveDomainFaultBackend) Begin(ctx context.Context) (Transaction, error) {
	if b.fired && b.mode == "readback_failure" {
		return nil, errors.New("injected domain readback failure")
	}
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &liveDomainFaultTx{Transaction: tx, b: b}, nil
}
func (b *liveDomainFaultBackend) Query(ctx context.Context, q string, args ...any) (Result, error) {
	if b.fired && b.mode == "readback_failure" {
		return Result{}, errors.New("injected domain readback failure")
	}
	return b.Backend.Query(ctx, q, args...)
}
func (tx *liveDomainFaultTx) Exec(ctx context.Context, q string, args ...any) error {
	if !tx.b.fired && ((tx.b.phase == "bootstrap" && strings.HasPrefix(q, "INSERT INTO") && strings.Contains(q, "_ponder_go_domains_head")) || (strings.Contains(q, "_ponder_go_domains_log") && strings.HasPrefix(q, "INSERT INTO") && len(args) > 1 && args[1] == tx.b.phase) || (tx.b.phase == "business" && strings.Contains(q, "_ponder_go_live_operations") && strings.Contains(q, "'reorg'"))) {
		tx.target = true
		if tx.b.mode == "exec_abort" {
			tx.b.fired = true
			return errors.New("injected domain statement abort")
		}
	}
	return tx.Transaction.Exec(ctx, q, args...)
}
func (tx *liveDomainFaultTx) Commit(ctx context.Context) error {
	if !tx.target || tx.b.fired {
		return tx.Transaction.Commit(ctx)
	}
	tx.b.fired = true
	if tx.b.mode == "before_commit" {
		_ = tx.Transaction.Rollback(ctx)
		return errors.New("injected domain commit before send")
	}
	if e := tx.Transaction.Commit(ctx); e != nil {
		return e
	}
	return errors.New("injected domain commit acknowledgement loss")
}
func liveDomainsFault(d *ManagedLiveDomains, phase, mode string) (*liveDomainFaultBackend, func()) {
	if phase == "business" {
		s := d.live().owner.s
		old := s.Backend
		b := &liveDomainFaultBackend{Backend: old, phase: phase, mode: mode}
		s.Backend = b
		return b, func() { s.Backend = old }
	}
	old := d.storage.syncBackend
	cacheOld := d.storage.syncCache.s.Backend
	b := &liveDomainFaultBackend{Backend: old, phase: phase, mode: mode}
	d.storage.syncBackend = b
	d.storage.syncCache.s.Backend = b
	return b, func() { d.storage.syncBackend = old; d.storage.syncCache.s.Backend = cacheOld }
}

func TestManagedLiveDomainsFaults(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, spec := range []struct {
				phase, mode string
				reopen      bool
			}{{"prepare", "exec_abort", false}, {"prepare", "after_commit", false}, {"business", "exec_abort", false}, {"business", "exec_abort", true}, {"business", "after_commit", false}, {"business", "readback_failure", true}, {"business", "before_commit", true}, {"ack", "before_commit", false}, {"ack", "after_commit", false}, {"prepare", "readback_failure", false}, {"ack", "readback_failure", true}} {
				t.Run(fmt.Sprintf("%s_%s_reopen_%v", spec.phase, spec.mode, spec.reopen), func(t *testing.T) {
					ctx := context.Background()
					_, opts, open := liveDomainsFixture(t, dialect)
					d := open(opts)
					batch, r := liveDomainsSeed(t, d)
					liveDomainsCacheSeed(t, d)
					before, e := checkpointStates(ctx, d.storage.businessBackend, d.storage.BusinessStore())
					if e != nil {
						t.Fatal(e)
					}
					fault, restore := liveDomainsFault(d, spec.phase, spec.mode)
					result, err := d.CoordinateReorg(ctx, r)
					restore()
					if !fault.fired {
						t.Fatalf("fault missed: %v", err)
					}
					success := (spec.mode == "after_commit")
					if success {
						if err != nil || result.Outcome != "reorg" {
							t.Fatalf("lost ACK not reconciled: %+v %v", result, err)
						}
					} else if err == nil {
						t.Fatalf("fault unexpectedly succeeded: %+v", result)
					}
					l, e := d.readLedger(ctx, d.storage.syncBackend)
					if e != nil {
						t.Fatal(e)
					}
					if spec.phase == "prepare" && spec.mode == "exec_abort" {
						if l.Pending != "" || len(l.Records) != 1 {
							t.Fatalf("prepare rollback=%+v", l)
						}
						liveDomainsCacheCount(t, d, 10, 3)
						liveBatchPGBalance(t, d.storage.businessBackend, d.storage.BusinessStore(), "20")
					} else {
						liveDomainsCacheCount(t, d, 10, 1)
					}
					if !success && !(spec.phase == "prepare" && spec.mode == "exec_abort") {
						wantFence := ErrManagedLiveDomainsPending
						if spec.phase == "business" && (spec.mode == "readback_failure" || spec.mode == "before_commit") {
							wantFence = ErrManagedLiveUncertain
						}
						if err = d.WithSync(ctx, func(*SourceSyncStore) error { t.Fatal("pending admitted sync callback"); return nil }); !errors.Is(err, wantFence) {
							t.Fatalf("sync pending fence=%v", err)
						}
						next := ManagedLiveBatch{ID: "forbidden-pending", Generation: 1, ExpectedLastOperation: batch.ID, ExpectedTail: 3, Blocks: batch.Blocks[:1]}
						if _, err = d.RetainBatch(ctx, next); !errors.Is(err, wantFence) {
							t.Fatalf("business pending fence=%v", err)
						}
					}
					if spec.reopen {
						if err = d.Close(ctx); err != nil {
							t.Fatal(err)
						}
						opts.OpenID = "domains-reopen"
						d = open(opts)
					} else if err = d.Resume(ctx); err != nil {
						t.Fatal(err)
					}
					result, err = d.CoordinateReorg(ctx, r)
					if err != nil {
						t.Fatal(err)
					}
					if spec.reopen && spec.phase != "ack" && result.Outcome != "covered-by-recovery" {
						t.Fatalf("recovery fabricated exact reorg=%+v", result)
					}
					if spec.phase == "prepare" && spec.mode == "exec_abort" && result.Outcome != "reorg" {
						t.Fatal(result)
					}
					liveBatchPGBalance(t, d.storage.businessBackend, d.storage.BusinessStore(), "missing")
					after, e := checkpointStates(ctx, d.storage.businessBackend, d.storage.BusinessStore())
					if e != nil || managedHash(after) != managedHash(before) {
						t.Fatalf("public projection changed: %v %v %v", before, after, e)
					}
					liveDomainsCacheSeed(t, d)
					retry, err := d.CoordinateReorg(ctx, r)
					if err != nil || managedHash(retry) != managedHash(result) {
						t.Fatalf("retry=%+v %v", retry, err)
					}
					liveDomainsCacheCount(t, d, 10, 3)
					if ready, e := d.Ready(ctx); e != nil || ready {
						t.Fatalf("ready=%v %v", ready, e)
					}
				})
			}
		})
	}
}

func TestManagedLiveDomainsGuards(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			o, opts, open := liveDomainsFixture(t, dialect)
			d := open(opts)
			batch, r := liveDomainsSeed(t, d)
			liveDomainsCacheSeed(t, d)
			var escaped *SourceSyncStore
			if e := d.WithSync(ctx, func(s *SourceSyncStore) error {
				escaped = s
				closeCtx, cancel := context.WithTimeout(ctx, 100000000)
				defer cancel()
				if e := d.Close(closeCtx); !errors.Is(e, ErrManagedLiveBusy) {
					return fmt.Errorf("callback close gate=%v", e)
				}
				if e := d.WithSync(ctx, func(*SourceSyncStore) error { return nil }); !errors.Is(e, ErrManagedLiveBusy) {
					return fmt.Errorf("reentrant callback=%v", e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e := escaped.PruneRPCRequestResults(ctx, 10, []uint64{5}); e == nil {
				t.Fatal("escaped generation callback usable")
			}
			for _, bad := range []ManagedLiveDomainReorg{{Reorg: r.Reorg}, {Reorg: r.Reorg, Orphaned: r.Orphaned[1:]}, {Reorg: r.Reorg, Orphaned: []ManagedLiveHead{r.Orphaned[0], r.Orphaned[0]}}} {
				if _, e := d.CoordinateReorg(ctx, bad); e == nil {
					t.Fatal("incomplete or invalid orphan branch admitted")
				}
			}
			liveDomainsCacheCount(t, d, 10, 3)
			liveBatchPGBalance(t, d.storage.businessBackend, d.storage.BusinessStore(), "20")
			// No affected segment is a real receipt, and empty prune is a real prepare/ack.
			c, e := d.Control(ctx)
			if e != nil {
				t.Fatal(e)
			}
			noop := ManagedLiveDomainReorg{Reorg: ManagedLiveReorg{ID: "empty-domains", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Ancestor: batch.Blocks[2].Head}}
			got, e := d.CoordinateReorg(ctx, noop)
			if e != nil || got.Reorg == nil || len(got.Reorg.Removed) != 0 {
				t.Fatalf("empty=%+v %v", got, e)
			}
			liveDomainsCacheCount(t, d, 10, 3)
			if e = d.Close(ctx); e != nil {
				t.Fatal(e)
			}
			opts.OpenID = "wrong-v3"
			var old *IndependentStorage
			if dialect == "postgres" {
				old, e = OpenManagedLiveRecoveryIndependentPostgres(ctx, o, opts)
			} else {
				old, e = OpenManagedLiveRecoveryIndependentPGlite(ctx, o, opts)
			}
			if e == nil {
				managedTestClose(t, old)
				t.Fatal("v3 adopted v4")
			}
			opts.OpenID = "reopen-guard"
			d = open(opts)
			if c, e = d.Control(ctx); e != nil || c.Generation != 2 || c.Version != managedLiveDomainsVersion {
				t.Fatalf("v4 reopen=%+v %v", c, e)
			}
		})
	}
}
