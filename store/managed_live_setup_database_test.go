package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type setupFlushFault struct {
	Backend
	fired bool
}
type setupFlushFaultTx struct {
	Transaction
	b *setupFlushFault
}

func (b *setupFlushFault) Begin(ctx context.Context) (Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &setupFlushFaultTx{tx, b}, nil
}
func (t *setupFlushFaultTx) Exec(ctx context.Context, q string, args ...any) error {
	if !t.b.fired && strings.HasPrefix(q, "INSERT INTO ") && strings.Contains(q, `."account"`) {
		t.b.fired = true
		return t.Transaction.Exec(ctx, "SELECT 1/0")
	}
	return t.Transaction.Exec(ctx, q, args...)
}

func setupNativeCallback(calls *int) func(context.Context, *Writer, func(uint64) error) error {
	return func(ctx context.Context, w *Writer, attribute func(uint64) error) error {
		for _, id := range []uint64{1, 10} {
			if err := attribute(id); err != nil {
				return err
			}
			*calls++
			if id == 1 {
				if _, err := w.Indexing().InsertOne(ctx, "account", Row{"id": "shared", "balance": "1"}, Conflict{}); err != nil {
					return err
				}
			} else {
				row, err := w.Find(ctx, "account", Row{"id": "shared"})
				if err != nil || fmt.Sprint(row["balance"]) != "1" {
					return fmt.Errorf("setup prior chain not visible: %v %v", row, err)
				}
				if _, err = w.Indexing().Update(ctx, "account", Row{"id": "shared"}, Row{"balance": "12"}); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func TestManagedLiveSetupBootstrap(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, mode := range []string{"before_commit", "after_commit", "readback_failure", "flush_abort", "missing_callback"} {
			t.Run(dialect+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				_, opts, open := continuousStoreFixture(t, dialect)
				opts.Setup = true
				d := open(opts)
				base := liveSegmentsUnitBatch(d.live())
				base.Blocks = nil
				io, scope, err := d.NewRuntimeScope(ctx)
				if err != nil {
					t.Fatal(err)
				}
				a, err := d.RetainBatch(ctx, base)
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				fn := setupNativeCallback(&calls)
				s := d.live().owner.s
				underlying := s.Backend
				var native *liveBatchPGFaultBackend
				var flush *setupFlushFault
				if mode == "missing_callback" {
					if _, err = d.ApplyRuntimeBatch(io, base, a, nil); err == nil {
						t.Fatal("v8 bootstrap skipped actual setup callback")
					}
					c, e := d.Control(ctx)
					if e != nil || c.Batches != 0 {
						t.Fatalf("missing-callback mutated control %+v %v", c, e)
					}
				} else if mode == "flush_abort" {
					flush = &setupFlushFault{Backend: underlying}
					s.Backend = flush
				} else {
					native = &liveBatchPGFaultBackend{Backend: underlying, mode: mode}
					s.Backend = native
				}
				result, err := d.ApplyRuntimeBootstrap(io, base, a, fn)
				s.Backend = underlying
				if flush != nil && !flush.fired {
					t.Fatal("native setup flush fault missed")
				}
				if native != nil && !native.fired {
					t.Fatal("native setup commit fault missed")
				}
				committed := mode == "after_commit" || mode == "readback_failure" || mode == "missing_callback"
				if mode == "after_commit" || mode == "missing_callback" {
					if err != nil || !result.Applied {
						t.Fatalf("confirmed bootstrap=%+v %v", result, err)
					}
					if _, e := d.ApplyRuntimeBootstrap(io, base, a, fn); e != nil {
						t.Fatal(e)
					}
					if calls != 2 {
						t.Fatalf("exact retry reran setup %d", calls)
					}
				} else if err == nil {
					t.Fatal("native setup fault accepted")
				}
				liveBatchPGBalance(t, underlying, s, map[bool]string{true: "12", false: "missing"}[committed])
				rows, e := underlying.Query(ctx, "SELECT chain_id FROM "+s.internal("_ponder_go_progress"))
				if e != nil || len(rows.Rows) != map[bool]int{true: 2, false: 0}[committed] {
					t.Fatalf("setup checkpoint atomicity=%v %v", rows, e)
				}
				journal, e := underlying.Query(ctx, "SELECT operation_id FROM "+s.internal("_ponder_go_journal"))
				if e != nil || len(journal.Rows) != 0 {
					t.Fatalf("setup safe prefix not pruned: %v %v", journal, e)
				}
				if mode == "before_commit" || mode == "readback_failure" {
					_, forbidden, e := d.NewRuntimeScope(ctx)
					if e == nil {
						_ = forbidden.Seal(ctx)
						t.Fatal("unknown setup commit admitted new epoch")
					}
					if !errors.Is(e, ErrManagedLiveUncertain) {
						t.Fatalf("wrong setup unknown fence %v", e)
					}
				}
				if err = scope.Seal(ctx); err != nil {
					t.Fatal(err)
				}
				if err = d.Close(ctx); err != nil {
					t.Fatal(err)
				}
				opts.OpenID = "setup-native-reopen"
				d = open(opts)
				c, err := d.Control(ctx)
				if err != nil || c.Version != managedLiveSetupVersion || c.Serving {
					t.Fatalf("setup reopen=%+v %v", c, err)
				}
				if !committed {
					retry := liveSegmentsUnitBatch(d.live())
					retry.Blocks = nil
					retry.ID = "setup-native-retry"
					retry.Generation = c.Generation
					retry.ExpectedLastOperation = c.LastOperation
					io, scope, err = d.NewRuntimeScope(ctx)
					if err != nil {
						t.Fatal(err)
					}
					a, err = d.RetainBatch(ctx, retry)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = d.ApplyRuntimeBootstrap(io, retry, a, fn); err != nil {
						t.Fatal(err)
					}
					if err = scope.Seal(ctx); err != nil {
						t.Fatal(err)
					}
				}
				liveBatchPGBalance(t, d.storage.businessBackend, d.storage.BusinessStore(), "12")
				c, err = d.Control(ctx)
				if err != nil || c.Batches != 1 || c.Bootstraps != 1 {
					t.Fatalf("setup duplicate bootstrap=%+v %v", c, err)
				}
				want := 2
				if mode == "before_commit" {
					want = 4
				}
				if mode == "flush_abort" {
					want = 3
				}
				if calls != want {
					t.Fatalf("setup calls=%d want=%d", calls, want)
				}
			})
		}
	}
}

func TestManagedLiveSetupNamespace(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			o, opts, open := continuousStoreFixture(t, dialect)
			opts.Setup = true
			d := open(opts)
			b := liveSegmentsUnitBatch(d.live())
			b.Blocks = nil
			io, scope, err := d.NewRuntimeScope(ctx)
			if err != nil {
				t.Fatal(err)
			}
			a, err := d.RetainBatch(ctx, b)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			if _, err = d.ApplyRuntimeBootstrap(io, b, a, setupNativeCallback(&calls)); err != nil {
				t.Fatal(err)
			}
			if err = scope.Seal(ctx); err != nil {
				t.Fatal(err)
			}
			if err = d.Close(ctx); err != nil {
				t.Fatal(err)
			}
			legacy := opts
			legacy.Setup = false
			legacy.OpenID = "forbidden-old-format"
			var wrong *ManagedLiveDomains
			if dialect == "postgres" {
				wrong, err = OpenManagedLiveRuntimePostgres(ctx, o, legacy)
			} else {
				wrong, err = OpenManagedLiveRuntimePGlite(ctx, o, legacy)
			}
			if wrong != nil {
				_ = wrong.Close(ctx)
			}
			if err == nil {
				t.Fatal("old runtime adopted v8 setup namespace")
			}
			opts.OpenID = "setup-after-rejected-adoption"
			d = open(opts)
			liveBatchPGBalance(t, d.storage.businessBackend, d.storage.BusinessStore(), "12")
			if calls != 2 {
				t.Fatal("namespace reopen reran setup")
			}
		})
	}
}
