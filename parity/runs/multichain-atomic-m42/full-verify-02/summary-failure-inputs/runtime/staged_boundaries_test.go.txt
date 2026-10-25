package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

var errRuntimePersist = errors.New("injected staged runtime persistence failure")

type runtimeFaultBackend struct {
	store.Backend
	stage     atomic.Int32
	fired     atomic.Bool
	terminate func()
}
type runtimeFaultTx struct {
	store.Transaction
	b        *runtimeFaultBackend
	assigned bool
}

func (b *runtimeFaultBackend) Begin(ctx context.Context) (store.Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &runtimeFaultTx{Transaction: tx, b: b}, nil
}
func (t *runtimeFaultTx) trip() error {
	t.b.fired.Store(true)
	if t.b.terminate != nil {
		t.b.terminate()
	}
	return errRuntimePersist
}
func (t *runtimeFaultTx) Exec(ctx context.Context, q string, args ...any) error {
	assign := strings.HasPrefix(q, "UPDATE ") && strings.Contains(q, `."_ponder_go_journal" SET checkpoint=`)
	if assign {
		r, e := t.Transaction.Query(ctx, "SELECT checkpoint FROM "+strings.Split(q, " SET ")[0][7:]+" WHERE checkpoint=$1", checkpoint.MaxString)
		if e != nil {
			return e
		}
		// A source-style MAX must exist before the runtime assigns the block.
		if len(r.Rows) > 0 {
			t.assigned = true
			if t.b.stage.Load() == 1 {
				return t.trip()
			}
		}
	}
	if e := t.Transaction.Exec(ctx, q, args...); e != nil {
		return e
	}
	if t.assigned && t.b.stage.Load() == 2 && strings.HasPrefix(q, "UPDATE ") && strings.Contains(q, `."_ponder_go_progress" SET checkpoint=`) {
		return t.trip()
	}
	return nil
}
func (t *runtimeFaultTx) Commit(ctx context.Context) error {
	if e := t.Transaction.Commit(ctx); e != nil {
		return e
	}
	if t.assigned && t.b.stage.Load() == 3 {
		return t.trip()
	}
	return nil
}

func stagedBlockModel() schema.Schema {
	return schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}
}
func stagedBlockConfig(url string) config.Config {
	return config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{url}}}, Blocks: []config.BlockSource{{Name: "heads", Chain: "local", Interval: 1}}}
}
func stagedBlockHandlers(count *int) map[string]Handler {
	return map[string]Handler{"heads:block": func(ctx context.Context, c *Context) error {
		// A cache miss inside a handler must reuse its open transaction rather
		// than recursively taking the store/PGlite transaction locks.
		var header map[string]any
		if e := c.Client.CallCached(ctx, "eth_getBlockByHash", []any{c.Event.Block.Hash, false}, &header); e != nil {
			return e
		}
		if header["hash"] != c.Event.Block.Hash {
			return fmt.Errorf("event client block changed")
		}
		if count != nil {
			*count++
		}
		n, e := Number(c.Event.Block.Number)
		if e != nil {
			return e
		}
		return c.DB.Write(ctx, "account", store.Row{"id": fmt.Sprint(n), "balance": fmt.Sprint(n + 1)}, store.Insert)
	}}
}
func stagedBackend(t *testing.T, ctx context.Context, dialect, dir string) store.Backend {
	t.Helper()
	var b store.Backend
	var e error
	if dialect == "postgres" {
		url := os.Getenv("PONDER_TEST_POSTGRES")
		if url == "" {
			t.Skip("explicit task PostgreSQL required; no parity credit")
		}
		b, e = store.OpenPostgres(ctx, url)
	} else {
		b, e = store.OpenPGlite(ctx, dir)
	}
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func stagedEngine(t *testing.T, b store.Backend, ns, url string, handlers map[string]Handler) (*Engine, *store.Lifecycle) {
	t.Helper()
	d, e := store.NewLifecycle(b, stagedBlockModel(), ns, schema.Multichain, store.LifecycleOptions{})
	if e != nil {
		t.Fatal(e)
	}
	r, e := NewStaged(stagedBlockConfig(url), d, handlers, StagedOptions{BuildID: "staged-boundaries"})
	if e != nil {
		t.Fatal(e)
	}
	r.Client.RequestsPerSecond = 0
	return r, d
}
func stageRows(t *testing.T, s *store.Store, want int) {
	t.Helper()
	r, e := s.Read(context.Background(), "account")
	if e != nil || len(r.Rows) != want {
		t.Fatalf("business rows: %#v %v", r, e)
	}
}
func stageLatest(t *testing.T, d *store.Lifecycle, want uint64) {
	t.Helper()
	s, e := d.Actions().Checkpoints(context.Background())
	if e != nil || len(s) != 1 {
		t.Fatal(s, e)
	}
	cp, e := checkpoint.Decode(s[0].LatestCheckpoint)
	if e != nil || cp.BlockNumber != want {
		t.Fatal("latest", s, e)
	}
}
func stageMessage(t *testing.T, l store.NotificationListener, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	n, e := l.Wait(ctx)
	if e != nil || n.Payload != payload {
		t.Fatal("actual notification", n, e)
	}
}
func stageSilent(t *testing.T, l store.NotificationListener) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	n, e := l.Wait(ctx)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("unexpected notification", n, e)
	}
}

func TestStagedRuntimePersistence(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		for _, stage := range []int32{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/stage_%d", dialect, stage), func(t *testing.T) {
				ctx := context.Background()
				rpc, url := localChain(t)
				base := stagedBackend(t, ctx, dialect, "memory://")
				defer base.Close()
				fault := &runtimeFaultBackend{Backend: base}
				ns := fmt.Sprintf("ponder_go_stage_%x", time.Now().UnixNano())
				calls := 0
				r, d := stagedEngine(t, fault, ns, url, stagedBlockHandlers(&calls))
				defer d.Close()
				if e := r.Sync(ctx); e != nil {
					t.Fatal(e)
				}
				stageRows(t, r.Store, 1)
				stageLatest(t, d, 0)
				l, e := store.Listen(ctx, base, d.Actions().LiveQueryChannel())
				if e != nil {
					t.Fatal(e)
				}
				defer l.Close()
				var ignored any
				if e = rpc.Call(ctx, "evm_mine", []any{}, &ignored); e != nil {
					t.Fatal(e)
				}
				fault.stage.Store(stage)
				e = r.Sync(ctx)
				if !errors.Is(e, errRuntimePersist) || !fault.fired.Load() {
					t.Fatal("persistence injection did not fire", e)
				}
				fault.stage.Store(0)
				if stage < 3 {
					stageRows(t, r.Store, 1)
					stageLatest(t, d, 0)
					stageSilent(t, l)
				} else {
					stageRows(t, r.Store, 2)
					stageLatest(t, d, 1)
					stageMessage(t, l, `["account"]`)
				}
				if e = r.Sync(ctx); e != nil {
					t.Fatal(e)
				}
				stageRows(t, r.Store, 2)
				stageLatest(t, d, 1)
				if stage < 3 {
					stageMessage(t, l, `["account"]`)
				}
				stageSilent(t, l)
				if e = r.Sync(ctx); e != nil {
					t.Fatal(e)
				}
				stageSilent(t, l)
				j, e := d.Actions().Journal(ctx, "account")
				if e != nil {
					t.Fatal(e)
				}
				for _, row := range j {
					if row["checkpoint"] == checkpoint.MaxString {
						t.Fatal("runtime left a durable sentinel", j)
					}
				}
			})
		}
	}
}

func TestStagedHandlerFailureAndReconnect(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			rpc, url := localChain(t)
			b := stagedBackend(t, ctx, dialect, "memory://")
			defer b.Close()
			fail := false
			calls := 0
			h := stagedBlockHandlers(&calls)
			original := h["heads:block"]
			h["heads:block"] = func(ctx context.Context, c *Context) error {
				if e := original(ctx, c); e != nil {
					return e
				}
				if fail {
					return errRuntimePersist
				}
				return nil
			}
			r, d := stagedEngine(t, b, fmt.Sprintf("ponder_go_handler_%x", time.Now().UnixNano()), url, h)
			defer d.Close()
			fail = true
			if e := r.Sync(ctx); !errors.Is(e, errRuntimePersist) {
				t.Fatal(e)
			}
			stageRows(t, r.Store, 0)
			fail = false
			if e := r.Sync(ctx); e != nil {
				t.Fatal("retry historical init without re-migrating", e)
			}
			stageRows(t, r.Store, 1)
			l, e := store.Listen(ctx, b, d.Actions().LiveQueryChannel())
			if e != nil {
				t.Fatal(e)
			}
			stageSilent(t, l)
			var ignored any
			if e = rpc.Call(ctx, "evm_mine", []any{}, &ignored); e != nil {
				t.Fatal(e)
			}
			fail = true
			if e = r.Sync(ctx); !errors.Is(e, errRuntimePersist) {
				t.Fatal(e)
			}
			stageRows(t, r.Store, 1)
			stageLatest(t, d, 0)
			stageSilent(t, l)
			fail = false
			if e = r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			stageMessage(t, l, `["account"]`)
			if e = l.Close(); e != nil {
				t.Fatal(e)
			}
			// LISTEN itself is not a durable event stream. Reconnect reads current
			// data, then listens to later committed changes, never replaying NOTIFY.
			stageRows(t, r.Store, 2)
			l, e = store.Listen(ctx, b, d.Actions().LiveQueryChannel())
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			stageSilent(t, l)
			if e = rpc.Call(ctx, "evm_mine", []any{}, &ignored); e != nil {
				t.Fatal(e)
			}
			if e = r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			stageMessage(t, l, `["account"]`)
			stageRows(t, r.Store, 3)
			stageSilent(t, l)
		})
	}
}

func TestStagedHandoffAdvancingHead(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			rpc, url := localChain(t)
			b := stagedBackend(t, ctx, dialect, "memory://")
			defer b.Close()
			calls := 0
			h := stagedBlockHandlers(&calls)
			original := h["heads:block"]
			var r *Engine
			h["heads:block"] = func(ctx context.Context, c *Context) error {
				n, e := Number(c.Event.Block.Number)
				if e != nil {
					return e
				}
				if n == 0 {
					if r.Phase() != "historical" {
						return fmt.Errorf("genesis skipped historical phase")
					}
					for i := 0; i < 3; i++ {
						var ignored any
						if e = rpc.Call(ctx, "evm_mine", []any{}, &ignored); e != nil {
							return e
						}
					}
				} else if r.Phase() != "realtime" {
					return fmt.Errorf("new head ran before live handoff")
				}
				return original(ctx, c)
			}
			var d *store.Lifecycle
			r, d = stagedEngine(t, b, fmt.Sprintf("ponder_go_handoff_%x", time.Now().UnixNano()), url, h)
			defer d.Close()
			if e := r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			stageRows(t, r.Store, 1)
			if r.Phase() != "realtime" {
				t.Fatal("handoff did not complete")
			}
			l, e := store.Listen(ctx, b, d.Actions().LiveQueryChannel())
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			if e = r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			stageRows(t, r.Store, 4)
			stageLatest(t, d, 3)
			for i := 0; i < 3; i++ {
				stageMessage(t, l, `["account"]`)
			}
			stageSilent(t, l)
			if e = r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			if calls != 4 {
				t.Fatal("handoff missed/duplicated handlers", calls)
			}
			stageSilent(t, l)
		})
	}
}

func TestStagedAutomaticFinality(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			rpc, url := localChain(t)
			b := stagedBackend(t, ctx, dialect, "memory://")
			defer b.Close()
			r, d := stagedEngine(t, b, fmt.Sprintf("ponder_go_final_%x", time.Now().UnixNano()), url, stagedBlockHandlers(nil))
			defer d.Close()
			zero := 0
			r.chain.ReorgWindow = &zero
			r.Config.Chains[0].ReorgWindow = &zero
			if e := r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			var ignored any
			if e := rpc.Call(ctx, "evm_mine", []any{}, &ignored); e != nil {
				t.Fatal(e)
			}
			if e := r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			states, e := d.Actions().Checkpoints(ctx)
			if e != nil || len(states) != 1 {
				t.Fatal(states, e)
			}
			if states[0].SafeCheckpoint != states[0].LatestCheckpoint || states[0].FinalizedCheckpoint != states[0].LatestCheckpoint {
				t.Fatal("automatic finalize did not seal latest", states)
			}
			journal, e := d.Actions().Journal(ctx, "account")
			if e != nil || len(journal) != 0 {
				t.Fatal("finalized undo retained", journal, e)
			}
			if e = d.Actions().RevertRuntime(ctx, checkpoint.Checkpoint{ChainID: 1}); e == nil {
				t.Fatal("reverted finalized baseline")
			}
			stageRows(t, r.Store, 2)
		})
	}
}

func TestStagedRollbackKeepsPublicLatest(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			rpc, url := localChain(t)
			base := stagedBackend(t, ctx, dialect, "memory://")
			defer base.Close()
			fault := &runtimeFaultBackend{Backend: base}
			r, d := stagedEngine(t, fault, fmt.Sprintf("ponder_go_cursor_%x", time.Now().UnixNano()), url, stagedBlockHandlers(nil))
			defer d.Close()
			if e := r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 2; i++ {
				var ignored any
				if e := rpc.Call(ctx, "evm_mine", []any{}, &ignored); e != nil {
					t.Fatal(e)
				}
			}
			if e := r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			stageLatest(t, d, 2)
			stageRows(t, r.Store, 3)
			l, e := store.Listen(ctx, base, d.Actions().LiveQueryChannel())
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			genesis, e := r.block(ctx, "0x0")
			if e != nil {
				t.Fatal(e)
			}
			ts, e := Number(genesis.Timestamp)
			if e != nil {
				t.Fatal(e)
			}
			target := checkpoint.Block(ts, 1, 0, true)
			if e = d.Actions().RevertRuntime(ctx, target); e != nil {
				t.Fatal(e)
			}
			stageLatest(t, d, 2)
			stageRows(t, r.Store, 1)
			stageSilent(t, l)
			cursor, e := d.Actions().RuntimeCheckpoint(ctx, 1)
			if e != nil || cursor != checkpoint.MustEncode(target) {
				t.Fatal("private rollback cursor", cursor, e)
			}
			fault.stage.Store(1)
			if e = r.Sync(ctx); !errors.Is(e, errRuntimePersist) {
				t.Fatal(e)
			}
			fault.stage.Store(0)
			stageLatest(t, d, 2)
			stageRows(t, r.Store, 1)
			stageSilent(t, l)
			if e = r.Sync(ctx); e != nil {
				t.Fatal("retry after rollback/failure skipped replacement", e)
			}
			stageRows(t, r.Store, 3)
			stageLatest(t, d, 2)
			for i := 0; i < 2; i++ {
				stageMessage(t, l, `["account"]`)
			}
			stageSilent(t, l)
		})
	}
}

func TestStagedEmptySchema(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			rpc, url := localChain(t)
			b := stagedBackend(t, ctx, dialect, "memory://")
			defer b.Close()
			d, e := store.NewLifecycle(b, schema.Schema{}, fmt.Sprintf("ponder_go_empty_stage_%x", time.Now().UnixNano()), schema.Multichain, store.LifecycleOptions{})
			if e != nil {
				t.Fatal(e)
			}
			defer d.Close()
			calls := 0
			r, e := NewStaged(stagedBlockConfig(url), d, map[string]Handler{"heads:block": func(context.Context, *Context) error { calls++; return nil }}, StagedOptions{BuildID: "empty"})
			if e != nil {
				t.Fatal(e)
			}
			r.Client.RequestsPerSecond = 0
			if e = r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			l, e := store.Listen(ctx, b, d.Actions().LiveQueryChannel())
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			var ignored any
			if e = rpc.Call(ctx, "evm_mine", []any{}, &ignored); e != nil {
				t.Fatal(e)
			}
			if e = r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			stageMessage(t, l, `[]`)
			stageLatest(t, d, 1)
			genesis, e := r.block(ctx, "0x0")
			if e != nil {
				t.Fatal(e)
			}
			ts, e := Number(genesis.Timestamp)
			if e != nil {
				t.Fatal(e)
			}
			if e = d.Actions().RevertRuntime(ctx, checkpoint.Block(ts, 1, 0, true)); e != nil {
				t.Fatal(e)
			}
			stageLatest(t, d, 1)
			stageSilent(t, l)
			if e = r.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			stageMessage(t, l, `[]`)
			stageSilent(t, l)
			if calls != 3 {
				t.Fatal("empty schema runtime skipped replay", calls)
			}
		})
	}
}
