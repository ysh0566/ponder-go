package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/internal/testpglite"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestMultichainEngineRecovery(t *testing.T) {
	for _, order := range []schema.Ordering{schema.Multichain, schema.Omnichain} {
		for _, dialect := range []string{"postgres", "pglite"} {
			t.Run(string(order)+"/"+dialect, func(t *testing.T) {
				testMultichainEngineRecovery(t, order, dialect)
			})
		}
	}
}

func testMultichainEngineRecovery(t *testing.T, order schema.Ordering, dialect string) {
	ctx := t.Context()
	a := testanvil.StartChain(t, 101, "--no-mining", "--timestamp", "1700000000")
	b := testanvil.StartChain(t, 202, "--no-mining", "--timestamp", "1700000000")
	call := func(endpoint, method string, args ...any) any {
		t.Helper()
		if args == nil {
			args = []any{}
		}
		var result any
		if err := rpc.New(endpoint).Call(ctx, method, args, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	mine := func(endpoint string, times ...int) {
		t.Helper()
		for _, offset := range times {
			call(endpoint, "evm_setNextBlockTimestamp", 1700000000+offset)
			call(endpoint, "evm_mine")
		}
	}
	snapshot := call(a, "evm_snapshot")
	mine(a, 10, 30, 60)
	mine(b, 20, 40, 60)
	end := uint64(2)
	cfg := config.Config{Ordering: order,
		Chains: []config.Chain{{ID: 101, Name: "a", RPC: []string{a}}, {ID: 202, Name: "b", RPC: []string{b}}},
		Blocks: []config.BlockSource{
			{Name: "Head", Chain: "a", StartBlock: 1, EndBlock: &end, Interval: 1},
			{Name: "Head", Chain: "b", StartBlock: 1, EndBlock: &end, Interval: 1},
		},
	}
	model := schema.Schema{Tables: []schema.Table{{Name: "heads", Columns: []schema.Column{
		{Name: "id", Type: "text", Primary: true}, {Name: "hash", Type: "text", NotNull: true},
	}}}}
	var kill func() error
	var restart func()
	path := filepath.Join(t.TempDir(), "db")
	openBackend := func() (store.Backend, error) { return store.OpenPGlite(ctx, path) }
	if dialect == "postgres" {
		server := testpostgres.Start(t)
		openBackend = func() (store.Backend, error) { return store.OpenPostgres(ctx, server.URL()) }
		kill = func() error { server.Kill(t); return nil }
		restart = func() { server.Restart(t) }
	} else {
		kill = testpglite.Track(t, path)
	}
	var calls int
	var armed, interrupted bool
	var interruptionErr error
	handler := func(ctx context.Context, event *Context) error {
		n, err := Number(event.Event.Block.Number)
		if err != nil {
			return err
		}
		key := fmt.Sprintf("%d/%d", event.Chain.ID, n)
		if err := event.DB.Write(ctx, "heads", store.Row{"id": key, "hash": event.Event.Block.Hash}, store.Insert); err != nil {
			return err
		}
		calls++
		if armed && event.Chain.ID == 101 && n == 1 {
			armed = false
			interrupted = true
			interruptionErr = kill()
			return interruptionErr
		}
		return nil
	}
	open := func() (*store.Store, ApplicationIndexer) {
		t.Helper()
		backend, err := openBackend()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = backend.Close() })
		db, err := store.New(ctx, backend, model, "ponder_go_engine_replay", order)
		if err != nil {
			t.Fatal(err)
		}
		w, err := NewIndexer(cfg, db, map[string]Handler{"Head:block": handler})
		if err != nil {
			t.Fatal(err)
		}
		return db, w
	}
	catchUp := func(w ApplicationIndexer) {
		t.Helper()
		run, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		ready := false
		err := w.RunIndexing(run, func(ok bool) {
			if ok {
				ready = true
				cancel()
			}
		})
		if !ready || !errors.Is(err, context.Canceled) {
			t.Fatalf("catch-up ready=%v err=%v", ready, err)
		}
	}
	checkCanonical := func(db *store.Store) {
		t.Helper()
		want := map[string]string{}
		for _, chain := range cfg.Chains {
			for n := uint64(1); n <= end; n++ {
				raw := call(chain.RPC[0], "eth_getBlockByNumber", Hex(n), false).(map[string]any)
				hash := raw["hash"].(string)
				want[fmt.Sprintf("%d/%d", chain.ID, n)] = hash
				stored, err := db.SyncedBlock(ctx, chain.ID, n)
				if err != nil || stored == nil || stored.Hash != hash {
					t.Fatalf("%s block %d: stored=%+v want=%s err=%v", chain.Name, n, stored, hash, err)
				}
			}
		}
		rows, err := db.Read(ctx, "heads")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, row := range rows.Rows {
			got[fmt.Sprint(row["id"])] = fmt.Sprint(row["hash"])
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("business rows=%v, canonical chain rows=%v", got, want)
		}
	}

	db, worker := open()
	catchUp(worker)
	checkCanonical(db)
	if call(a, "evm_revert", snapshot) != true {
		t.Fatal("cannot replace first chain")
	}
	mine(a, 25, 50, 65)
	armed = true
	run, cancel := context.WithTimeout(ctx, time.Minute)
	var unavailable bool
	err := worker.RunIndexing(run, func(ready bool) {
		if interrupted && !ready {
			unavailable = true
			cancel()
		}
	})
	cancel()
	if err == nil || !interrupted || interruptionErr != nil || !unavailable {
		t.Fatalf("engine interruption=%v kill=%v unavailable=%v err=%v", interrupted, interruptionErr, unavailable, err)
	}
	_ = db.Backend.Close()
	if restart != nil {
		restart()
	}
	for attempt := 0; attempt < 2; attempt++ {
		db, worker = open()
		before := calls
		if attempt == 0 {
			row := call(a, "eth_getBlockByNumber", Hex(1), false).(map[string]any)
			rows, err := db.Backend.Query(ctx, "SELECT id FROM ponder_go_engine_replay.heads WHERE id=$1 AND hash=$2", "101/1", row["hash"])
			if err != nil || len(rows.Rows) != 0 {
				t.Fatalf("interrupted callback was already committed: %v %v", rows, err)
			}
		}
		catchUp(worker)
		checkCanonical(db)
		if attempt == 0 && calls == before || attempt == 1 && calls != before {
			t.Fatalf("reopen %d: callback count %d -> %d", attempt, before, calls)
		}
		if err := db.Backend.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
