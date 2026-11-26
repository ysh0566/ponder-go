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
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// The forked chain commits first. Its peer then commits both finalized
// and speculative rows after the orphaned segment in actual execution order.
func TestRuntimeIsolatedLiveReorg(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend+"/completed", func(t *testing.T) { testIsolatedLiveReorg(t, backend, false) })
		t.Run(backend+"/interrupted", func(t *testing.T) { testIsolatedLiveReorg(t, backend, true) })
	}
}

func testIsolatedLiveReorg(t *testing.T, backend string, interrupt bool) {
	continuousRoot(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	node := newContinuousRPC(t, 1)
	calls := map[uint64][]uint64{}
	end := `,"endBlock":6`
	if interrupt {
		end = ""
	}
	raw := fmt.Sprintf(`{"ordering":"experimental_isolated","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":600},"two":{"id":2,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":false}},"contracts":{"Init":{"abi":[],"chain":{"one":{"startBlock":0},"two":{"startBlock":0}}}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"interval":1%s}}}`, node.p.server.URL+"/1", node.p.server.URL+"/2", end)
	onBlock := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
		id := finiteChain(a)
		n, err := continuousNumber(a)
		if err != nil {
			return err
		}
		balance, err := a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40))
		if err != nil {
			return err
		}
		calls[id] = append(calls[id], n)
		_, err = a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(id), "height": int(n), "value": balance.Int64()}, store.Conflict{})
		return err
	})
	var setups []uint64
	onSetup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
		id := finiteChain(a)
		setups = append(setups, id)
		_, err := a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(id), "height": 0, "value": 7}, store.Conflict{})
		return err
	})
	b := runtimeSourceBuild(t, raw, config.SourceIndexingFunction{Name: "Blocks:block", Fn: onBlock}, config.SourceIndexingFunction{Name: "Init:setup", Fn: onSetup})
	f := newManagedFiniteFixture(t, backend, b)
	f.options.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{Name: "seen", PrimaryKey: []string{"chain_id", "height"}, Columns: []schema.Column{
		{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "height", Type: "integer", NotNull: true}, {Name: "value", Type: "integer", NotNull: true},
	}}}}
	setTip := func(id, n uint64) { node.mu.Lock(); node.latest[id] = n; node.mu.Unlock() }
	height := func(h store.ManagedLiveHead) uint64 {
		cp, err := checkpoint.Decode(h.Checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		return cp.BlockNumber
	}
	stop := errors.New("stop after canonical replacement")
	var r *RuntimeRunner
	var epochs []uint64
	var forked, rolledBack bool
	o := RuntimeOptions{BuildID: "isolated-live", OpenID: "initial", Historical: runtimeFixtureInput(f), Requesters: runtimeFactory(node.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
	var forkCommits []RuntimeCommit
	o.OnCommitted = func(ctx context.Context, g RuntimeCommit) error {
		c, err := r.Control(ctx)
		if err != nil {
			return err
		}
		if g.Head != c.Cursors[g.ChainID] || g.Checkpoint != c.LogicalCursors[g.ChainID] || g.Safe != (g.Checkpoint <= c.LogicalSafe[g.ChainID]) {
			return fmt.Errorf("group %+v differs from isolated checkpoint or safe position", g)
		}
		if g.ChainID == 1 && height(g.Head) == 3 {
			forkCommits = append(forkCommits, g)
		}
		return nil
	}
	o.Live.OnProgress = func(ctx context.Context, p RuntimeLiveProgress) error {
		c, err := r.Control(ctx)
		if err != nil {
			return err
		}
		switch p.Type {
		case "ready":
			if rolledBack {
				return stop
			}
			setTip(1, 3)
		case "block", "finalize":
			if p.ChainID == 1 && height(c.Cursors[1]) == 3 && !forked {
				setTip(2, 4)
			}
			if !forked && height(c.Cursors[2]) == 4 && c.SafeHeads[2].Checkpoint != "" && height(c.SafeHeads[2]) >= 3 {
				forked = true
				node.fork(1, 3)
			}
		case "reorg":
			if p.ChainID != 1 || height(c.Cursors[1]) != 2 || height(c.Cursors[2]) != 4 || height(c.SafeHeads[2]) < 3 {
				return fmt.Errorf("wrong rollback cursors %+v safe %+v", c.Cursors, c.SafeHeads)
			}
			rolledBack = true
		}
		return nil
	}
	if interrupt {
		o.Live.OnDurableReorg = func(_ context.Context, stage store.ManagedLiveDomainStage) error {
			if stage.Phase == "prepare" {
				return stop
			}
			return nil
		}
	}
	r = runtimeOpen(t, backend, o)
	if _, err := r.Run(ctx); !errors.Is(err, stop) {
		t.Fatalf("fork and recovery: calls=%v: %v", calls, err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	wantCalls := []uint64{1, 2, 3}
	forkHeight := 0
	if !interrupt {
		wantCalls = append(wantCalls, 3)
		forkHeight = 3
	}
	if !forked || rolledBack == interrupt || !slices.Equal(calls[1], wantCalls) || !slices.Equal(calls[2], []uint64{1, 2, 3, 4}) {
		t.Fatalf("callbacks before cold restart: %v, rolled back %t", calls, rolledBack)
	}
	if interrupt {
		if len(forkCommits) != 1 {
			t.Fatalf("orphan notifications: %+v", forkCommits)
		}
	} else if len(forkCommits) != 2 || forkCommits[0].Head.Hash == forkCommits[1].Head.Hash || forkCommits[0].OperationID == forkCommits[1].OperationID || forkCommits[0].Safe || forkCommits[1].Safe {
		t.Fatalf("original and replacement notifications: %+v", forkCommits)
	}
	checkIsolatedLiveRows(t, f, 3, 4, forkHeight)
	// The node replaces another, earlier block while the runner is offline.
	node.fork(1, 2)
	o.OpenID = "reopen"
	o.Live.OnProgress = nil
	o.Live.OnDurableReorg = nil
	if interrupt {
		o.Live.OnProgress = func(ctx context.Context, e RuntimeLiveProgress) error {
			if e.Type != "ready" && e.Type != "block" {
				return nil
			}
			control, err := r.Control(ctx)
			if err != nil {
				return err
			}
			if control.Serving && height(control.Cursors[1]) == 6 && height(control.Cursors[2]) == 6 {
				return stop
			}
			return nil
		}
	}
	node.setLatest(6)
	clear(calls)
	r = runtimeOpen(t, backend, o)
	res, err := r.Run(ctx)
	if !res.Recovered || (interrupt && !errors.Is(err, stop)) || (!interrupt && (err != nil || !res.Completed || !res.Ready)) {
		t.Fatalf("cold restart %+v: %v", res, err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls[2], []uint64{4, 5, 6}) {
		t.Fatalf("peer replay after independent finality: %v", calls)
	}
	if !slices.Equal(setups, []uint64{1, 2}) {
		t.Fatalf("setup callbacks across openings: %v", setups)
	}
	checkIsolatedLiveRows(t, f, 6, 6, 2)
}

func checkIsolatedLiveRows(t *testing.T, f finiteFixture, first, second, forkHeight int) {
	t.Helper()
	managedInspect(t, f, func(db store.Backend) {
		ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace)
		initial := managedRows(t, db, "SELECT chain_id::text AS id, value::text AS value FROM "+ns+".seen WHERE height=0 ORDER BY chain_id")
		if len(initial) != 2 || fmt.Sprint(initial[0]["id"], "/", initial[0]["value"], "/", initial[1]["id"], "/", initial[1]["value"]) != "1/7/2/7" {
			t.Fatalf("durable setup rows: %v", initial)
		}
		rows := managedRows(t, db, "SELECT chain_id::text AS chain, height::text AS height, value::text AS value FROM "+ns+".seen WHERE height>0 ORDER BY chain_id,height")
		if len(rows) != first+second {
			t.Fatalf("business rows: %v", rows)
		}
		for i, row := range rows {
			id, n := 1, i+1
			if i >= first {
				id, n = 2, i-first+1
			}
			want := 42
			if id == 1 && forkHeight > 0 && n >= forkHeight {
				want = 99
			}
			if fmt.Sprint(row["chain"], "/", row["height"], "/", row["value"]) != fmt.Sprintf("%d/%d/%d", id, n, want) {
				t.Fatalf("row %d: %v", i, row)
			}
		}
	})
}
