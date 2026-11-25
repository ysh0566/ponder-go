package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type providerGate struct {
	url                string
	offline            atomic.Bool
	accepted, rejected atomic.Uint64
}

func newProviderGate(t *testing.T, target string, status int) *providerGate {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	g := new(providerGate)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.offline.Load() {
			g.rejected.Add(1)
			http.Error(w, http.StatusText(status), status)
			return
		}
		g.accepted.Add(1)
		proxy.ServeHTTP(w, r)
	}))
	g.url = srv.URL
	t.Cleanup(srv.Close)
	return g
}

func TestMultiProviderCrossChainReorg(t *testing.T) {
	for _, order := range []schema.Ordering{schema.Multichain, schema.Omnichain} {
		for _, backend := range []string{"postgres", "pglite"} {
			t.Run(string(order)+"/"+backend, func(t *testing.T) { testProviderReorg(t, order, backend) })
		}
	}
}

func testProviderReorg(t *testing.T, order schema.Ordering, backend string) {
	ctx := t.Context()
	const genesis = 1700000000
	node := func(id uint64) string { return testanvil.StartChain(t, id, "--no-mining", "--timestamp", "1700000000") }
	original, replacement, peer := node(101), node(101), node(202)
	primary := newProviderGate(t, original, http.StatusServiceUnavailable)
	standby := newProviderGate(t, replacement, http.StatusTooManyRequests)
	standby.offline.Store(true)
	call := func(endpoint, method string, args ...any) any {
		t.Helper()
		if args == nil {
			args = []any{}
		}
		var v any
		if err := rpc.New(endpoint).Call(ctx, method, args, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	head := func(endpoint string, n uint64) map[string]any {
		t.Helper()
		return call(endpoint, "eth_getBlockByNumber", Hex(n), false).(map[string]any)
	}
	mine := func(endpoint string, offset int) {
		t.Helper()
		call(endpoint, "evm_setNextBlockTimestamp", genesis+offset)
		call(endpoint, "evm_mine")
	}
	if head(original, 0)["hash"] != head(replacement, 0)["hash"] {
		t.Fatal("providers must have identical genesis")
	}
	snapshot := call(original, "evm_snapshot")
	mine(original, 10)
	mine(original, 60)
	mine(replacement, 15)
	mine(replacement, 60)
	mine(peer, 20)
	mine(peer, 70)
	oldHash := head(original, 1)["hash"].(string)
	newHash := head(replacement, 1)["hash"].(string)
	if oldHash == newHash {
		t.Fatal("expected separate provider forks")
	}
	model := schema.Schema{Tables: []schema.Table{{Name: "ledger", Columns: []schema.Column{
		{Name: "id", Type: "text", Primary: true}, {Name: "branch", Type: "text", NotNull: true},
	}}}}
	cfg := config.Config{Ordering: order, Chains: []config.Chain{
		{Name: "A", ID: 101, RPC: []string{primary.url, standby.url}, PollingIntervalMS: 20},
		{Name: "B", ID: 202, RPC: []string{peer}, PollingIntervalMS: 20},
	}, Blocks: []config.BlockSource{{Name: "Head", Chain: "A", StartBlock: 1, Interval: 10}, {Name: "Head", Chain: "B", StartBlock: 1, Interval: 10}}}
	var calls []string
	handlers := map[string]Handler{"Head:block": func(ctx context.Context, c *Context) error {
		calls = append(calls, c.Chain.Name+":"+c.Event.Block.Hash)
		value := c.Event.Block.Hash
		if c.Chain.Name == "B" {
			row, err := c.DB.Find(ctx, "ledger", store.Row{"id": "A"})
			if err != nil {
				return err
			}
			if row == nil {
				return fmt.Errorf("A has not committed before B")
			}
			value = fmt.Sprint(row["branch"])
		}
		return c.DB.Write(ctx, "ledger", store.Row{"id": c.Chain.Name, "branch": value}, store.Insert)
	}}
	ns := fmt.Sprintf("ponder_go_pool_%x", time.Now().UnixNano())
	dir := filepath.Join(t.TempDir(), "db")
	var db *store.Store
	open := func() ApplicationIndexer {
		t.Helper()
		b := stagedBackend(t, ctx, backend, dir)
		t.Cleanup(func() { _ = b.Close() })
		var err error
		db, err = store.New(ctx, b, model, ns, order)
		if err != nil {
			t.Fatal(err)
		}
		w, err := NewIndexerWithOptions(cfg, db, handlers, Options{DisableCache: backend == "pglite"})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	worker := open()
	poll := func(wantReady bool) {
		t.Helper()
		round, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		reached := false
		err := worker.RunIndexing(round, func(ready bool) {
			if ready == wantReady {
				reached = true
				cancel()
			}
		})
		if !errors.Is(err, context.Canceled) || !reached {
			t.Fatalf("expected ready=%t: %v", wantReady, err)
		}
	}
	check := func(endpoint, hash string, wantCalls []string) map[string]string {
		t.Helper()
		rows, err := db.Read(ctx, "ledger")
		if err != nil || len(rows.Rows) != 2 || !reflect.DeepEqual(calls, wantCalls) {
			t.Fatal("rows/callbacks", rows, calls, wantCalls, err)
		}
		for _, row := range rows.Rows {
			if row["branch"] != hash {
				t.Fatalf("wrong branch for %v, want %s", row, hash)
			}
		}
		progress, err := db.Progress(ctx)
		if err != nil || len(progress) != 2 {
			t.Fatal("progress", progress, err)
		}
		for chain, provider := range map[uint64]string{101: endpoint, 202: peer} {
			stored, err := db.SyncedBlock(ctx, chain, 1)
			if err != nil || stored == nil || stored.Hash != head(provider, 1)["hash"] {
				t.Fatal("stored fork", chain, stored, err)
			}
			cp, err := checkpoint.Decode(progress[strconv.FormatUint(chain, 10)])
			if err != nil || cp.ChainID != chain {
				t.Fatal("checkpoint", cp, err)
			}
			expected, err := Number(head(provider, cp.BlockNumber)["timestamp"].(string))
			if err != nil || cp.BlockTimestamp != expected {
				t.Fatal("checkpoint timestamp", cp, expected, err)
			}
		}
		return progress
	}
	peerHash := head(peer, 1)["hash"].(string)
	initial := []string{"A:" + oldHash, "B:" + peerHash}
	poll(true)
	before := check(original, oldHash, initial)
	if primary.accepted.Load() == 0 || standby.accepted.Load() != 0 {
		t.Fatal("unexpected initial providers")
	}
	primary.offline.Store(true)
	poll(false)
	if after := check(original, oldHash, initial); !reflect.DeepEqual(before, after) {
		t.Fatal("outage advanced checkpoints")
	}
	if primary.rejected.Load() == 0 || standby.rejected.Load() == 0 {
		t.Fatal("pool exhaustion not observed")
	}

	standby.offline.Store(false)
	poll(true)
	replay := append(initial, "A:"+newHash, "B:"+peerHash)
	after := check(replacement, newHash, replay)
	if standby.accepted.Load() == 0 {
		t.Fatal("standby did not supply canonical data")
	}
	if err := db.Backend.Close(); err != nil {
		t.Fatal(err)
	}
	worker = open()
	poll(true)
	if progress := check(replacement, newHash, replay); !reflect.DeepEqual(after, progress) {
		t.Fatal("cold reopen changed progress")
	}

	// The original provider returns after it has adopted the replacement fork.
	if call(original, "evm_revert", snapshot) != true {
		t.Fatal("restore original node")
	}
	mine(original, 15)
	mine(original, 60)
	if head(original, 2)["hash"] != head(replacement, 2)["hash"] {
		t.Fatal("recovered providers disagree")
	}
	served := primary.accepted.Load()
	primary.offline.Store(false)
	standby.offline.Store(true)
	poll(true)
	if progress := check(original, newHash, replay); !reflect.DeepEqual(after, progress) || primary.accepted.Load() == served {
		t.Fatal("failback did not preserve replay")
	}
}
