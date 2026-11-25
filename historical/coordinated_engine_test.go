package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/internal/testpglite"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestCoordinatedEngineInterruption(t *testing.T) {
	for _, engine := range []string{"postgres", "pglite/business", "pglite/sync"} {
		for _, phase := range []string{"prepare", "business", "ack"} {
			t.Run(engine+"/"+phase, func(t *testing.T) { testCoordinatedEngineInterruption(t, engine, phase) })
		}
	}
}

func testCoordinatedEngineInterruption(t *testing.T, engine, phase string) {
	continuousRoot(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	backend := strings.Split(engine, "/")[0]
	var postgres *testpostgres.Server
	if backend == "postgres" {
		postgres = testpostgres.Start(t)
		t.Setenv("PONDER_TEST_POSTGRES", postgres.URL())
	}
	chain := newContinuousRPC(t, 3)
	calls := map[uint64][]uint64{}
	build := runtimeBuild(t, chain.p, 4, 1, indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
		height, err := continuousNumber(a)
		if err != nil {
			return err
		}
		id := finiteChain(a)
		calls[id] = append(calls[id], height)
		return finiteIncrement(ctx, a)
	}))
	for _, c := range build.Executable().Chains {
		c["reorgWindow"] = uint64(600)
	}
	fixture := newManagedFiniteFixture(t, backend, build)
	opts := RuntimeOptions{Historical: runtimeFixtureInput(fixture), BuildID: "engine-recovery", OpenID: "original", Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
	var epochs []uint64
	opts.Requesters = runtimeFactory(chain.p, &epochs)
	var r *RuntimeRunner
	var err error
	var kill func() error
	if postgres != nil {
		kill = func() error { postgres.Kill(t); return nil }
	} else {
		directory := opts.Historical.Owned.Storage.BusinessDirectory
		if engine == "pglite/sync" {
			directory = opts.Historical.Owned.Storage.SyncDirectory
		}
		kill = testpglite.Track(t, directory)
	}
	forked := false
	operation := ""
	var killErr error
	opts.Live.OnProgress = func(ctx context.Context, e RuntimeLiveProgress) error {
		return forkDuringCrashTest(ctx, r, e, &forked, chain.p.server.URL)
	}
	opts.Live.OnDurableReorg = func(ctx context.Context, stage store.ManagedLiveDomainStage) error {
		if stage.Phase != phase {
			return nil
		}
		if ready, err := r.Ready(ctx); err != nil || ready {
			return fmt.Errorf("readiness during reorg %t: %v", ready, err)
		}
		operation = stage.OperationID
		killErr = kill()
		return killErr
	}
	open := OpenRuntimePGlite
	if postgres != nil {
		open = OpenRuntimePostgres
	}
	r, err = open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	result, err := r.Run(ctx)
	if killErr != nil || operation == "" || err == nil || ctx.Err() != nil || result.Ready || result.Completed {
		t.Fatalf("interrupt %s at %s: op=%q kill=%v result=%+v run=%v ctx=%v", engine, phase, operation, killErr, result, err, ctx.Err())
	}
	if ready, err := r.Ready(ctx); err != nil || ready {
		t.Fatalf("failed owner readiness %v: %v", ready, err)
	}
	// Closing an interrupted owner may return the terminated process/connection error.
	if closeErr := r.Close(ctx); errors.Is(closeErr, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("close: %v, ctx %v", closeErr, ctx.Err())
	}
	if postgres != nil {
		postgres.Restart(t)
		fixture.options.Owned.Storage.PostgresURL = postgres.URL()
	}
	opts.Historical = runtimeFixtureInput(fixture)
	opts.Live = &RuntimeLiveOptions{PollingInterval: time.Millisecond}
	var stable map[uint64]store.ManagedLiveHead
	for attempt := 0; attempt < 2; attempt++ {
		opts.OpenID = fmt.Sprintf("resume-%d", attempt)
		resumed := runtimeOpen(t, backend, opts)
		control, err := resumed.Control(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 0 && (control.Serving || len(control.ReplayTargets) == 0 || control.Recoveries != 1) {
			t.Fatalf("missing recovery on reopen: %+v", control)
		}
		// Each new owner rewinds the unfinalized suffix to its authenticated
		// safe cursor. Every height beyond that cursor must run exactly once.
		calls = map[uint64][]uint64{}
		expected := map[uint64][]uint64{}
		for _, id := range []uint64{1, 2} {
			var from uint64
			if head, ok := control.Cursors[id]; ok {
				cp, err := checkpoint.Decode(head.Checkpoint)
				if err != nil {
					t.Fatal(err)
				}
				from = cp.BlockNumber
			}
			for n := from + 1; n <= 4; n++ {
				expected[id] = append(expected[id], n)
			}
		}
		result, err := resumed.Run(ctx)
		if err != nil || !result.Completed || !result.Ready || !result.Recovered {
			t.Fatal(result, err)
		}
		if !reflect.DeepEqual(calls, expected) {
			t.Fatalf("replayed heights %v, want %v", calls, expected)
		}
		control, err = resumed.Control(ctx)
		if err != nil || len(control.Cursors) != 2 || len(control.ReplayTargets) != 0 {
			t.Fatal(control, err)
		}
		for id, head := range control.Cursors {
			cp, err := checkpoint.Decode(head.Checkpoint)
			chain.mu.Lock()
			canonical := chain.canonical[id][4]["hash"]
			chain.mu.Unlock()
			if err != nil || cp.ChainID != id || cp.BlockNumber != 4 || head.Hash != canonical {
				t.Fatal("canonical cursor", id, head, canonical, err)
			}
		}
		evidence, err := resumed.domains.DomainEvidence(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ledger struct {
			Pending string
			Intents map[string]json.RawMessage
			Acks    map[string]json.RawMessage
		}
		if err = json.Unmarshal(evidence, &ledger); err != nil || ledger.Pending != "" || len(ledger.Intents) != 1 || len(ledger.Acks) != 1 || ledger.Intents[operation] == nil || ledger.Acks[operation] == nil {
			t.Fatalf("operation %q not settled exactly once: %v, pending=%q intents=%d acks=%d", operation, err, ledger.Pending, len(ledger.Intents), len(ledger.Acks))
		}
		if attempt == 0 {
			stable = control.Cursors
		} else if !reflect.DeepEqual(stable, control.Cursors) {
			t.Fatal("settled cursors changed after second reopen", control.Cursors, stable)
		}
		if err := resumed.Close(ctx); err != nil {
			t.Fatal(err)
		}
		managedInspect(t, fixture, func(db store.Backend) {
			rows := managedRows(t, db, "SELECT id, balance::text AS balance FROM "+schema.Quote(fixture.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
			if len(rows) != 2 || rows[0]["id"] != "chain1" || rows[1]["id"] != "chain2" || rows[0]["balance"] != "4" || rows[1]["balance"] != "4" {
				t.Fatal("committed balances", rows)
			}
		})
	}
}
