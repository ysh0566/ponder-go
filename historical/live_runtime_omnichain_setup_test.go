package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/internal/testpglite"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestRuntimeOmnichainSetup(t *testing.T) {
	g := loadOmniLogsGolden(t)
	for _, backend := range []string{"postgres", "pglite"} {
		for _, mode := range []string{"closing", "log-only"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				testOmnichainSetupRecovery(t, backend, mode, "", 0, g)
			})
		}
	}
}

// The two interruption points straddle the transaction that installs both
// setup writes and the initial positions of all participating chains.
func TestOmnichainSetupEngineInterruption(t *testing.T) {
	g := loadOmniLogsGolden(t)
	for _, engine := range []string{"postgres", "pglite/business", "pglite/sync"} {
		backend, _, _ := strings.Cut(engine, "/")
		for i, phase := range []string{"during-setup", "after-commit"} {
			t.Run(engine+"/"+phase, func(t *testing.T) {
				testOmnichainSetupRecovery(t, backend, "closing", engine, i+1, g)
			})
		}
	}
}

func testOmnichainSetupRecovery(t *testing.T, backend, mode, engine string, interruptPass int, g omniLogsGolden) {
	t.Helper()
	continuousRoot(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	var pg *testpostgres.Server
	if engine == "postgres" {
		pg = testpostgres.Start(t)
		t.Setenv("PONDER_TEST_POSTGRES", pg.URL())
	}
	p, _ := omniLogsRPC(t, g)
	closing := mode == "closing"
	events := slices.DeleteFunc(slices.Clone(g.Cases[0].Snapshots[0].Calls), func(e omniLogsEvent) bool {
		return !closing && strings.Contains(e.Key, ":block:")
	})
	var calls []string
	handlers := omniLogsHandlers(func() []omniLogsEvent { return events }, &calls, nil)
	var order []uint64
	var kill func() error
	var interrupted bool
	var failedRead error
	pass := 0
	handlers["setup"] = func(ctx context.Context, a *indexing.HandlerArgs) error {
		id := finiteChain(a)
		order = append(order, id)
		if _, ok := a.Context.Contracts["Init"]; !ok || a.Event != nil {
			return fmt.Errorf("invalid setup context on chain %d", id)
		}
		balance, err := a.Context.Client.GetBalance(ctx, g.Address)
		if err != nil {
			return err
		}
		height := uint64(4)
		if id == 2 {
			height = 0
		}
		if balance.Uint64() != height {
			return fmt.Errorf("setup RPC on chain %d read block %s, expected %d", id, balance, height)
		}
		key := store.Row{"id": "setup"}
		if id == 1 {
			_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "setup", "value": "one"}, store.Conflict{})
			return err
		}
		row, err := a.Context.DB.Find(ctx, "account", key)
		if err != nil || row["value"] != "one" {
			return fmt.Errorf("missing first setup write: row=%v err=%v", row, err)
		}
		if _, err = a.Context.DB.Update(ctx, "account", key, store.Row{"value": "one,two"}); err != nil {
			return err
		}
		if interruptPass != 1 {
			if pass == 1 {
				return errors.New("second setup failed")
			}
			return nil
		}
		// Flush the update and verify both handlers' writes through the active transaction.
		rows, err := a.Context.DB.SQL(ctx, "SELECT value FROM account WHERE id = $1", "setup")
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["value"] != "one,two" {
			return fmt.Errorf("pending setup row %v: %v", rows.Rows, err)
		}
		if pass == 1 {
			if err := kill(); err != nil {
				return err
			}
			interrupted = true
		}
		if engine == "pglite/sync" {
			// Use a new key so this read needs the sync engine, including on replay.
			balance, err = a.Context.Client.GetBalance(ctx, "0x0000000000000000000000000000000000000001")
			if err == nil && balance.Uint64() != height {
				return fmt.Errorf("unexpected setup RPC balance %v", balance)
			}
		} else {
			_, err = a.Context.DB.SQL(ctx, "SELECT 1")
		}
		if pass == 1 {
			failedRead = err
		}
		return err
	}
	chains := map[string]any{"one": map[string]any{"startBlock": 4}, "two": map[string]any{"startBlock": 0}}
	b := omniLogsBuildWithSetup(t, g, p, handlers, 1, 3, closing, chains)
	f := newManagedFiniteFixture(t, backend, b)
	omnichainGoldenStorage(&f)
	var epochs []uint64
	o := RuntimeOptions{BuildID: "omnichain-setup-" + mode, Historical: runtimeFixtureInput(f), Requesters: runtimeFactory(p, &epochs)}
	o.OnSetupCommitted = func(context.Context) error {
		if pass != 2 {
			return fmt.Errorf("unexpected setup commit on pass %d", pass)
		}
		if interruptPass == pass {
			if err := kill(); err != nil {
				return err
			}
			interrupted = true
			return nil // Let the runner observe the engine loss on its next database access.
		}
		return errors.New("stopped after setup commit")
	}
	open := OpenRuntimePGlite
	if backend == "postgres" {
		open = OpenRuntimePostgres
	}
	for pass = 1; pass <= 4; pass++ {
		o.OpenID = fmt.Sprintf("setup-%d", pass)
		calls = nil
		fault := interruptPass == pass
		if fault {
			if pg != nil {
				kill = func() error { pg.Kill(t); return nil }
			} else {
				dir := f.options.Owned.Storage.BusinessDirectory
				if engine == "pglite/sync" {
					dir = f.options.Owned.Storage.SyncDirectory
				}
				kill = testpglite.Track(t, dir)
			}
		}
		r, err := open(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close(context.Background()) })
		out, err := r.Run(ctx)
		switch pass {
		case 1, 2:
			want := "second setup failed"
			if pass == 2 {
				want = "stopped after setup commit"
			}
			if fault && (!interrupted || (pass == 1 && failedRead == nil)) {
				t.Fatalf("pass %d: engine interruption=%t, failed read=%v, run=%v", pass, interrupted, failedRead, err)
			}
			if err == nil || (!fault && !strings.Contains(err.Error(), want)) || ctx.Err() != nil || out.Ready || out.Completed || len(calls) != 0 {
				t.Fatalf("pass %d: result %+v, calls %v, error %v", pass, out, calls, err)
			}
		case 3, 4:
			if err != nil || !out.Ready || !out.Completed || !out.Recovered {
				t.Fatalf("pass %d: result %+v, error %v", pass, out, err)
			}
			if (pass == 3 && len(calls) != len(events)) || (pass == 4 && len(calls) != 0) {
				t.Fatalf("pass %d: unexpected event sequence %v", pass, calls)
			}
		}
		if ready, err := r.Ready(ctx); err != nil || ready != out.Ready {
			t.Fatalf("pass %d: readiness %t: %v", pass, ready, err)
		}
		if !fault {
			control, err := r.Control(ctx)
			if err != nil {
				t.Fatal(err)
			}
			assertOmnichainSetupControl(t, control, pass, closing, g)
		}
		if err := r.Close(ctx); err != nil && (!fault || ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded)) {
			t.Fatal(err)
		}
		if pg != nil && fault {
			pg.Restart(t)
			f.options.Owned.Storage.PostgresURL = pg.URL()
			o.Historical.Owned.Storage.PostgresURL = pg.URL()
		}
		assertStoppedOmnichainSetup(t, f, pass, closing, g, events)
	}
	if !slices.Equal(order, []uint64{1, 2, 1, 2}) {
		t.Fatalf("setup order across retry and reopen: %v", order)
	}
}

func assertStoppedOmnichainSetup(t *testing.T, f finiteFixture, pass int, closing bool, g omniLogsGolden, events []omniLogsEvent) {
	t.Helper()
	// Inspect the durable transaction before a new runtime owner can repair it.
	managedInspect(t, f, func(db store.Backend) {
		ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace)
		state := managedRows(t, db, "SELECT payload::text AS payload FROM "+ns+"._ponder_go_live_control WHERE key='control'")
		var c store.ManagedLiveControl
		if len(state) != 1 {
			t.Fatalf("missing control after pass %d: %v", pass, state)
		}
		if err := json.Unmarshal([]byte(fmt.Sprint(state[0]["payload"])), &c); err != nil {
			t.Fatal(err)
		}
		assertOmnichainSetupControl(t, c, pass, closing, g)
		rows := managedRows(t, db, "SELECT id,value FROM "+ns+".account ORDER BY id")
		switch {
		case pass == 1 && len(rows) != 0:
			t.Fatalf("failed setup leaked writes: %v", rows)
		case pass >= 2 && (len(rows) == 0 || rows[0]["id"] != "setup" || rows[0]["value"] != "one,two"):
			t.Fatalf("committed setup is missing: %v", rows)
		case pass == 2 && len(rows) != 1:
			t.Fatalf("events ran before restart: %v", rows)
		case pass >= 3:
			keys := omniLogsKeys(events)
			if len(rows) != 2 || rows[1]["id"] != "shared" || rows[1]["value"] != strings.Join(keys, "|") {
				t.Fatalf("unexpected indexed rows: %v", rows)
			}
		}
		if rows := managedRows(t, db, "SELECT operation_id FROM "+ns+"._ponder_go_journal"); len(rows) != 0 {
			t.Fatalf("retained finalized undo: %v", rows)
		}
	})
}

func assertOmnichainSetupControl(t *testing.T, c store.ManagedLiveControl, pass int, closing bool, g omniLogsGolden) {
	t.Helper()
	if pass == 1 && (c.Batches != 0 || c.Bootstraps != 0 || len(c.Cursors) != 0) {
		t.Fatalf("failed initialization advanced checkpoints: %+v", c)
	}
	if pass >= 2 && (c.Bootstraps != 1 || len(c.Cursors) != 2) {
		t.Fatalf("missing committed setup: %+v", c)
	}
	if pass == 2 {
		for _, chain := range g.Chains {
			ts, _ := uintQuantity(chain.Blocks[1]["timestamp"])
			want := checkpoint.MustEncode(checkpoint.Block(ts, chain.ID, 1, false))
			if c.Batches != 1 || c.LogicalCursors[chain.ID] != want || c.LogicalSafe[chain.ID] != want {
				t.Fatalf("incorrect initial checkpoint for chain %d: %+v", chain.ID, c)
			}
		}
	}
	if pass >= 3 && closing {
		assertOmniLogsPositions(t, c, g.Cases[0].Snapshots[0])
	}
}

func TestRuntimeOmnichainSetupFinality(t *testing.T) {
	g := loadOmniLogsGolden(t)
	p, _ := omniLogsRPC(t, g)
	fn := indexing.Handler(func(context.Context, *indexing.HandlerArgs) error {
		t.Fatal("setup or event callback ran before finality check")
		return nil
	})
	handlers := map[string]indexing.Handler{"setup": fn, "block": fn, "A": fn, "B": fn}
	for _, tc := range []struct {
		name, expected string
		height         uint64
	}{
		{"local", "setup block exceeds chain finality", 5},
		{"global", "setup block exceeds global finalized watermark", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := omniLogsBuildWithSetup(t, g, p, handlers, 1, 3, true, map[string]any{"two": map[string]any{"startBlock": tc.height}})
			var epochs []uint64
			o := RuntimeOptions{Historical: Options{Build: b}, OpenID: "guard", BuildID: "setup-guard", Requesters: runtimeFactory(p, &epochs)}
			_, err := openRuntime(context.Background(), o, func(context.Context, store.IndependentStorageOptions, store.ManagedLiveRuntimeOptions) (*store.ManagedLiveDomains, error) {
				t.Fatal("opened database with unfinalized setup")
				return nil, nil
			})
			if err == nil || !strings.Contains(err.Error(), tc.expected) {
				t.Fatalf("expected %q, got %v", tc.expected, err)
			}
		})
	}
}

func TestRuntimeOmnichainSetupLineage(t *testing.T) {
	g := loadOmniLogsGolden(t)
	for i := range g.Chains {
		if g.Chains[i].ID == 1 {
			g.Chains[i].Latest = "0x2" // Global watermark at timestamp 115.
		}
	}
	canonical, _ := omniLogsRPC(t, g)
	for i := range g.Chains {
		if g.Chains[i].ID == 2 {
			block := maps.Clone(g.Chains[i].Blocks[3])
			block["timestamp"] = "0x6e" // Preflight sees 110; the canonical epoch sees 130.
			g.Chains[i].Blocks[3] = block
		}
	}
	preflight, _ := omniLogsRPC(t, g)
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			calls := 0
			h := indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { calls++; return nil })
			b := omniLogsBuildWithSetup(t, g, preflight, map[string]indexing.Handler{"setup": h, "block": h, "A": h, "B": h}, 1, 1, true, map[string]any{"two": map[string]any{"startBlock": 3}})
			f := newManagedFiniteFixture(t, backend, b)
			var epochs []uint64
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omnichain-setup-lineage", OpenID: "lineage", Requesters: func(ctx context.Context, epoch uint64) ([]rpc.Requester, error) {
				p := canonical
				if epoch == 0 {
					p = preflight
				}
				return runtimeFactory(p, &epochs)(ctx, epoch)
			}}
			r := runtimeOpen(t, backend, o)
			ctx := context.Background()
			out, err := r.Run(ctx)
			if err == nil || !strings.Contains(err.Error(), "on authenticated lineage") || out.Ready || out.Completed || calls != 0 {
				t.Fatalf("unexpected setup lineage result %+v, callbacks %d, err %v", out, calls, err)
			}
			c, err := r.Control(ctx)
			if err != nil || c.Batches != 0 || len(c.Cursors) != 0 {
				t.Fatalf("setup lineage failure advanced control: %+v err %v", c, err)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
