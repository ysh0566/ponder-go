package historical

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/internal/testpglite"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
)

func TestOmnichainEngineInterruption(t *testing.T) {
	for _, engine := range []string{"postgres", "pglite/business", "pglite/sync"} {
		for _, mode := range []string{"closing", "log-only"} {
			t.Run(engine+"/"+mode, func(t *testing.T) {
				testOmnichainEngineInterruption(t, engine, mode)
			})
		}
	}
}

func testOmnichainEngineInterruption(t *testing.T, engine, mode string) {
	continuousRoot(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	backend, _, _ := strings.Cut(engine, "/")
	var pg *testpostgres.Server
	if backend == "postgres" {
		pg = testpostgres.Start(t)
		t.Setenv("PONDER_TEST_POSTGRES", pg.URL())
	}
	g := loadOmniLogsGolden(t)
	tc := omniLogsNamedCase(t, g, "paged_failure")
	if mode == "log-only" {
		g = omniLogOnlyFixture(t, "small_failure").Data
		tc = g.Cases[0]
	}
	p, _ := omniLogsRPC(t, g)
	expected := tc.Snapshots[0].Calls
	var calls []string
	handlers := omniLogsHandlers(func() []omniLogsEvent { return expected }, &calls, nil)
	handle := handlers["A"]
	var kill func() error
	var engineErr error
	killed := false
	handlers["A"] = func(ctx context.Context, a *indexing.HandlerArgs) error {
		if err := handle(ctx, a); err != nil {
			return err
		}
		if calls[len(calls)-1] != tc.FailKey {
			return nil
		}
		interrupt := !killed
		if interrupt {
			// A table SELECT flushes the ORM to the open business transaction.
			row, err := a.Context.DB.SQL(ctx, "SELECT value FROM account WHERE id = $1", "shared")
			if err != nil || len(row.Rows) != 1 || row.Rows[0]["value"] != strings.Join(calls, "|") {
				return fmt.Errorf("pending SQL write: rows %v, err %v", row.Rows, err)
			}
			if err := kill(); err != nil {
				return fmt.Errorf("interrupt engine: %w", err)
			}
			killed = true
		}
		var err error
		if engine == "pglite/sync" {
			// This RPC read uses the sync engine's cache in the failed and recovered owners.
			balance, readErr := a.Context.Client.GetBalance(ctx, g.Address)
			if readErr == nil && balance.Uint64() != 3 {
				return fmt.Errorf("cached balance request used the wrong log height: %v", balance)
			}
			err = readErr
		} else {
			_, err = a.Context.DB.SQL(ctx, "SELECT 1")
		}
		if interrupt {
			engineErr = err
		}
		return err
	}
	build := omniLogsBuildRange(t, g, p, handlers, 1, 3, mode == "closing")
	f := newManagedFiniteFixture(t, backend, build)
	omnichainGoldenStorage(&f)
	storage := &f.options.Owned.Storage
	if pg != nil {
		kill = func() error { pg.Kill(t); return nil }
	} else {
		directory := storage.BusinessDirectory
		if engine == "pglite/sync" {
			directory = storage.SyncDirectory
		}
		kill = testpglite.Track(t, directory)
	}

	var epochs []uint64
	o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omnichain-engine-" + mode, OpenID: "interrupted", Requesters: runtimeFactory(p, &epochs)}
	o.Historical.QuerySize = tc.QuerySize
	open := OpenRuntimePGlite
	if pg != nil {
		open = OpenRuntimePostgres
	}
	r, err := open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	result, err := r.Run(ctx)
	if !killed || engineErr == nil || err == nil || ctx.Err() != nil || result.Ready || result.Completed || !slices.Equal(calls, omniLogsKeys(expected)) {
		t.Fatalf("interrupted run: killed=%t engine=%v run=%v ctx=%v result=%+v calls=%v", killed, engineErr, err, ctx.Err(), result, calls)
	}
	if ready, err := r.Ready(ctx); ready || err != nil {
		t.Fatalf("interrupted owner ready=%t: %v", ready, err)
	}
	if closeErr := r.Close(ctx); errors.Is(closeErr, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("interrupted owner close: %v, ctx %v", closeErr, ctx.Err())
	}
	if pg != nil {
		pg.Restart(t)
		storage.PostgresURL = pg.URL()
		o.Historical.Owned.Storage.PostgresURL = pg.URL()
	}
	// Inspect committed rows and undo before a new runtime can repair anything.
	assertOmniLogsRows(t, f, tc.Snapshots[0])
	for attempt, want := range tc.Snapshots[1:] {
		o.OpenID = fmt.Sprintf("resume-%d", attempt)
		calls, expected = nil, want.Calls
		resumed := runtimeOpen(t, backend, o)
		before, err := resumed.Control(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertOmniLogsPositions(t, before, tc.Snapshots[attempt])
		result, err = resumed.Run(ctx)
		if err != nil || !result.Ready || !result.Recovered || !result.Completed || !slices.Equal(calls, omniLogsKeys(expected)) {
			t.Fatalf("resume %d: result=%+v calls=%v want=%v err=%v", attempt, result, calls, omniLogsKeys(expected), err)
		}
		control, err := resumed.Control(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, chain := range g.Chains {
			head := control.Cursors[chain.ID]
			cp, err := checkpoint.Decode(head.Checkpoint)
			if err != nil || cp.ChainID != chain.ID || cp.BlockNumber != 3 || head.Hash != chain.Blocks[3]["hash"] {
				t.Fatalf("chain %d canonical cursor: %+v, %v", chain.ID, head, err)
			}
		}
		if err := resumed.Close(ctx); err != nil {
			t.Fatal(err)
		}
		assertOmniLogsSnapshot(t, f, control, want)
	}
}
