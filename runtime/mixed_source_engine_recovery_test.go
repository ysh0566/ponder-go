package runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/internal/testpglite"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestMixedSourceEngineRecovery(t *testing.T) {
	for _, order := range []schema.Ordering{schema.Isolated, schema.Multichain, schema.Omnichain} {
		for _, backend := range []string{"postgres", "pglite"} {
			t.Run(string(order)+"/"+backend+"/replacement-call", func(t *testing.T) {
				testMixedSourceProviderReorg(t, order, backend, "Child.run()")
			})
			if order != schema.Isolated {
				t.Run(string(order)+"/"+backend+"/dependent-peer-log", func(t *testing.T) {
					testMixedSourceProviderReorg(t, order, backend, "Peer:Ping")
				})
			}
		}
	}
}

type mixedSourceEngineFault struct {
	open            func() store.Backend
	kill, restart   func()
	armed, fired    atomic.Bool
	written, resume chan struct{}
	probe           chan error
}

func newMixedSourceEngineFault(t *testing.T, backend, dir string) *mixedSourceEngineFault {
	t.Helper()
	f := &mixedSourceEngineFault{written: make(chan struct{}), resume: make(chan struct{}), probe: make(chan error, 1)}
	var open func() (store.Backend, error)
	if backend == "postgres" {
		server := testpostgres.Start(t)
		open = func() (store.Backend, error) { return store.OpenPostgres(t.Context(), server.URL()) }
		f.kill = func() { server.Kill(t) }
		f.restart = func() { server.Restart(t) }
	} else {
		open = func() (store.Backend, error) { return store.OpenPGlite(t.Context(), dir) }
		kill := testpglite.Track(t, dir)
		f.kill = func() {
			if err := kill(); err != nil {
				t.Fatal(err)
			}
		}
		f.restart = func() {}
	}
	f.open = func() store.Backend {
		t.Helper()
		db, err := open()
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	return f
}

// The insert has reached the database transaction. Hold the callback open
// while the test kills the engine, then query through that same transaction.
func (f *mixedSourceEngineFault) afterWrite(ctx context.Context, c *Context) error {
	if !f.fired.CompareAndSwap(false, true) {
		return nil
	}
	close(f.written)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.resume:
	}
	_, err := c.DB.Find(ctx, "deliveries", store.Row{"chain_id": c.Chain.ID, "event": c.Event.Name})
	f.probe <- err
	return err
}

func (f *mixedSourceEngineFault) interrupt(t *testing.T, app *Application) {
	t.Helper()
	resume := sync.OnceFunc(func() { close(f.resume) })
	defer resume()
	select {
	case <-f.written:
	case <-app.done:
		t.Fatalf("indexer stopped before the replacement callback: %v", app.Wait())
	case <-time.After(time.Minute):
		t.Fatal("replacement callback did not reach its write")
	}
	f.kill()
	resume()
	select {
	case err := <-f.probe:
		if err == nil {
			t.Fatal("the interrupted transaction still answered a query")
		}
	case <-time.After(time.Minute):
		t.Fatal("database interruption did not reach the active callback")
	}
	shutdown, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	_ = app.Shutdown(shutdown)
	if shutdown.Err() != nil {
		t.Fatal("interrupted indexer did not stop", shutdown.Err())
	}
}

func checkInterruptedMixedSource(t *testing.T, db *store.Store, net mixedSourceNetwork, original map[string]string, interrupted string) {
	t.Helper()
	ctx := t.Context()
	rows, err := db.Read(ctx, "deliveries")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"Wallet:transaction:to": net.replacement.child, "Child:Ping": net.replacement.amount}
	if interrupted == "Peer:Ping" {
		expected["Child.run()"] = net.replacement.child
		expected["Wallet:transfer:to"] = net.replacement.amount
	}
	if db.Ordering == schema.Isolated {
		expected["Peer:Ping"] = "23"
	}
	for _, row := range rows.Rows {
		name := fmt.Sprint(row["event"])
		value, exists := expected[name]
		id, hash := "101", net.replacement.blockHash
		if name == "Peer:Ping" {
			id, hash = "202", net.peerHash
		}
		if !exists || row["payload"] != value || fmt.Sprint(row["chain_id"]) != id || row["block_hash"] != hash {
			t.Fatalf("unexpected durable delivery before indexing resumed: %v", row)
		}
		delete(expected, name)
	}
	if len(expected) != 0 {
		t.Fatal("committed deliveries missing after engine restart", expected)
	}
	progress, err := db.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	block := (reorgNodeRPC{t}).head(net.replacement.node, 2)
	timestamp, err := Number(block["timestamp"].(string))
	if err != nil {
		t.Fatal(err)
	}
	// Both transactions emit one log. Only the second targets the new child.
	want := checkpoint.Checkpoint{BlockTimestamp: timestamp, ChainID: 101, BlockNumber: 2, TransactionIndex: 1, EventType: checkpoint.Logs, EventIndex: 1}
	if interrupted == "Peer:Ping" {
		want = checkpoint.Block(timestamp, 101, 2, true)
	}
	if progress["101"] != checkpoint.MustEncode(want) {
		t.Fatalf("replacement progress after engine restart: got %q, want %v", progress["101"], want)
	}
	header, err := db.SyncedBlock(ctx, 101, 2)
	if err != nil || header == nil || header.Hash != net.replacement.blockHash {
		t.Fatalf("replacement header missing after engine restart: %v, %v", header, err)
	}
	net.checkDiscoveredChild(t, db, net.replacement.child)
	if db.Ordering == schema.Multichain {
		replay, err := db.PendingMultichainReplay(ctx)
		if err != nil || replay == "" {
			t.Fatalf("peer replay not pending after engine restart: %q, %v", replay, err)
		}
	}
	peer := progress["202"]
	if db.Ordering == schema.Isolated {
		if peer != original["202"] {
			t.Fatalf("isolated fork changed peer progress: %q, want %q", peer, original["202"])
		}
	} else if peer >= original["202"] {
		t.Fatalf("global fork did not rewind peer progress: %q, originally %q", peer, original["202"])
	}
}
