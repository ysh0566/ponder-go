package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func compiledAnvilChains(t *testing.T) []string {
	t.Helper()
	endpoints := make([]string, 3)
	for i, id := range []uint64{101, 303, 202} {
		url := testanvil.StartChain(t, id, "--no-mining", "--timestamp", "1700000000")
		endpoints[i] = url
		client := rpc.New(url)
		for _, offset := range []int{10, 30, 50} {
			var result any
			if err := client.Call(t.Context(), "evm_setNextBlockTimestamp", []any{1700000000 + offset + 5*i}, &result); err != nil {
				t.Fatal(err)
			}
			if err := client.Call(t.Context(), "evm_mine", []any{}, &result); err != nil {
				t.Fatal(err)
			}
		}
	}
	return endpoints
}

func TestCompiledMultichainIndexer(t *testing.T) {
	endpoints := compiledAnvilChains(t)
	for _, order := range []schema.Ordering{schema.Multichain, schema.Omnichain, schema.Isolated} {
		for _, dialect := range []string{"postgres", "pglite"} {
			t.Run(string(order)+"/"+dialect, func(t *testing.T) {
				testCompiledIndexer(t, endpoints, order, dialect, config.SourceBuildOptions{})
			})
		}
	}
}

func TestCompiledCustomTransport(t *testing.T) {
	endpoints := compiledAnvilChains(t)
	client := rpc.New(endpoints[2])
	client.Retries = 0
	client.RequestsPerSecond = 0
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			var mu sync.Mutex
			counts := map[string]int{}
			transport := func(ctx context.Context, method string, params any) (json.RawMessage, error) {
				mu.Lock()
				counts[method]++
				mu.Unlock()
				var result json.RawMessage
				err := client.Call(ctx, method, params, &result)
				return result, err
			}
			urls := slices.Clone(endpoints)
			urls[2] = ""
			options := config.SourceBuildOptions{RPCTransports: map[string]rpc.TransportFunc{"second": transport}}
			testCompiledIndexer(t, urls, schema.Omnichain, dialect, options)
			if counts["eth_chainId"] == 0 || counts["eth_getBlockByNumber"] == 0 {
				t.Fatal("missing acquisition or callback requests", counts)
			}
		})
	}
}

func testCompiledIndexer(t *testing.T, urls []string, order schema.Ordering, dialect string, options config.SourceBuildOptions) {
	ctx := t.Context()
	raw := fmt.Sprintf(`{"ordering":%q,"chains":{"first":{"id":101,"rpc":%q,"cacheRpcRequests":false,"reorgWindow":0},"unused":{"id":303,"rpc":%q},"second":{"id":202,"rpc":%q,"reorgWindow":0}},"blocks":{"Head":{"chain":{"first":{},"second":{}},"startBlock":1,"endBlock":2,"interval":1},"Unregistered":{"chain":"first","startBlock":0,"interval":1}},"contracts":{"Init":{"abi":[],"chain":"second","startBlock":1}}}`, order, urls[0], urls[1], urls[2])
	input, err := config.ParseSourceConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.BuildSourceConfig(input, options)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []string
	record := Handler(func(ctx context.Context, c *Context) error {
		var actual string
		if err := c.Client.Call(ctx, "eth_chainId", []any{}, &actual); err != nil {
			return err
		}
		if (c.Client.Cache != nil) != (c.Chain.ID == 202) {
			return fmt.Errorf("callback cache policy on chain %d", c.Chain.ID)
		}
		if actual != Hex(c.Chain.ID) {
			return fmt.Errorf("chain %d used RPC %s", c.Chain.ID, actual)
		}
		key := c.Event.Name + "/" + c.Event.Block.Number
		if err := c.DB.Write(ctx, "delivery", store.Row{"chain_id": c.Chain.ID, "key": key, "hash": c.Event.Block.Hash}, store.Insert); err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, c.Chain.Name+"/"+key)
		return nil
	})
	functions := []config.SourceIndexingFunction{{Name: "Head:block", Fn: record}, {Name: "Init:setup", Fn: record}}
	build, err := config.BuildSourceIndexing(ctx, input, base, functions)
	if err != nil {
		t.Fatal(err)
	}
	configs, err := build.NativeConfigs()
	if err != nil || len(configs) != 2 {
		t.Fatal(configs, err)
	}
	for i, source := range configs {
		clientIndex := []int{0, 2}[i]
		if source.Ordering != order || source.Chains[0].Name != []string{"first", "second"}[i] || source.SourcePlan.Client != base.RPCClients()[clientIndex] || source.SourcePlan.CacheRPCRequests != (i == 1) || len(source.SourcePlan.Setup) != i || len(source.Blocks) != 1 {
			t.Fatal("wrong compiled chain", source)
		}
	}
	model := schema.Schema{Tables: []schema.Table{{Name: "delivery", PrimaryKey: []string{"chain_id", "key"}, Columns: []schema.Column{
		{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "key", Type: "text", NotNull: true}, {Name: "hash", Type: "text", NotNull: true},
	}}}}
	path := filepath.Join(t.TempDir(), "data")
	ns := fmt.Sprintf("ponder_go_compiled_%x", time.Now().UnixNano())
	want := []string{"first/Head:block/0x1", "second/Init:setup/0x1", "second/Head:block/0x1", "first/Head:block/0x2", "second/Head:block/0x2"}
	for attempt := 0; attempt < 2; attempt++ {
		backend := stagedBackend(t, ctx, dialect, path)
		t.Cleanup(func() { _ = backend.Close() })
		db, err := store.New(ctx, backend, model, ns, order)
		if err != nil {
			t.Fatal(err)
		}
		var indexer ApplicationIndexer
		if attempt == 0 {
			indexer, err = NewIndexerFromSource(build, db, Options{})
		} else {
			indexer, err = NewSourceIndexer(ctx, input, options, functions, db, Options{})
		}
		if err != nil {
			t.Fatal(err)
		}
		run, cancel := context.WithTimeout(ctx, time.Minute)
		var ready bool
		registry := metrics.New()
		err = indexer.RunIndexing(metrics.WithChainSync(run, registry), func(v bool) { ready = v })
		cancel()
		if err != nil || !ready {
			t.Fatal("bounded completion", ready, err)
		}
		body, err := registry.Collect(ctx)
		if err != nil || strings.Contains(body, "ponder_sync_is_realtime{") {
			t.Fatalf("backfill-only run: %s, %v", body, err)
		}
		for _, name := range []string{"first", "second"} {
			for _, sample := range []string{fmt.Sprintf("ponder_sync_block{chain=%q} 2", name), fmt.Sprintf("ponder_sync_is_complete{chain=%q} 1", name)} {
				if !strings.Contains(body, sample+"\n") {
					t.Fatalf("open %d missing %s: %s", attempt, sample, body)
				}
			}
		}
		for _, client := range base.RPCClients() {
			if client.Cache != nil {
				t.Fatal("worker attached a deployment cache to the shared transport")
			}
		}
		check := slices.Clone(calls)
		if order != schema.Omnichain {
			slices.Sort(check)
			slices.Sort(want)
		}
		if !reflect.DeepEqual(check, want) {
			t.Fatal("callback deliveries", check, "want", want)
		}
		rows, err := db.Read(ctx, "delivery")
		if err != nil || len(rows.Rows) != len(want) {
			t.Fatal("persisted deliveries", rows, err)
		}
		for _, id := range []uint64{101, 202} {
			block, err := db.SyncedBlock(ctx, id, 2)
			if err != nil || block == nil {
				t.Fatal("missing indexed block", id, block, err)
			}
			var head map[string]any
			idx := 0
			if id == 202 {
				idx = 2
			}
			if err := base.RPCClients()[idx].Call(ctx, "eth_getBlockByNumber", []any{"0x2", false}, &head); err != nil || head["hash"] != block.Hash {
				t.Fatal("canonical hash", id, block, err)
			}
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompiledSignedBlockIntervals(t *testing.T) {
	ctx := t.Context()
	url := testanvil.StartChain(t, 101, "--no-mining")
	client := rpc.New(url)
	var mined any
	if err := client.Call(ctx, "anvil_mine", []any{"0x9"}, &mined); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`{"chains":{"one":{"id":101,"rpc":%q,"reorgWindow":0}},"blocks":{
		"Negative":{"chain":"one","startBlock":2,"endBlock":9,"interval":-3},
		"Positive":{"chain":"one","startBlock":2,"endBlock":9,"interval":3}
	}}`, url)
	cfg, err := config.ParseSourceConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	onBlock := Handler(func(ctx context.Context, c *Context) error {
		id := c.Event.Name + "/" + c.Event.Block.Number
		return c.DB.Write(ctx, "sample", store.Row{"id": id}, store.Insert)
	})
	build, err := config.BuildSourceIndexing(ctx, cfg, base, []config.SourceIndexingFunction{
		{Name: "Negative:block", Fn: onBlock}, {Name: "Positive:block", Fn: onBlock},
	})
	if err != nil {
		t.Fatal(err)
	}
	native, err := build.NativeConfig("one")
	if err != nil {
		t.Fatal(err)
	}
	for i, interval := range []float64{-3, 3} {
		f := native.SourcePlan.Callbacks[i].Filter.(config.SourceBlockFilter)
		if f.Interval != interval || f.Offset != 2 || native.Blocks[i].Interval != 3 {
			t.Fatalf("source filter %+v, block schedule %+v", f, native.Blocks[i])
		}
	}
	end := uint64(9)
	direct := config.Config{Chains: native.Chains, Blocks: []config.BlockSource{
		{Name: "Negative", Chain: "one", StartBlock: 2, EndBlock: &end, Interval: config.BlockPeriod(-3)},
		{Name: "Positive", Chain: "one", StartBlock: 2, EndBlock: &end, Interval: 3},
	}}
	handlers := map[string]Handler{"Negative:block": onBlock, "Positive:block": onBlock}
	for _, tc := range []struct {
		name string
		open func(*store.Store) (ApplicationIndexer, error)
	}{
		{"compiled", func(db *store.Store) (ApplicationIndexer, error) { return NewIndexerFromSource(build, db, Options{}) }},
		{"direct", func(db *store.Store) (ApplicationIndexer, error) { return NewIndexer(direct, db, handlers) }},
	} {
		for _, backend := range []string{"postgres", "pglite"} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "data")
				namespace := fmt.Sprintf("ponder_go_signed_%x", time.Now().UnixNano())
				model := schema.Schema{Tables: []schema.Table{{Name: "sample", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
				for attempt := 0; attempt < 2; attempt++ {
					b := stagedBackend(t, ctx, backend, dir)
					t.Cleanup(func() { _ = b.Close() })
					db, err := store.New(ctx, b, model, namespace, schema.Multichain)
					if err != nil {
						t.Fatal(err)
					}
					idx, err := tc.open(db)
					if err != nil {
						t.Fatal(err)
					}
					run, cancel := context.WithTimeout(ctx, time.Minute)
					var ready bool
					err = idx.RunIndexing(run, func(v bool) { ready = v })
					cancel()
					if err != nil || !ready {
						t.Fatalf("bounded indexing ready=%v: %v", ready, err)
					}
					rows, err := db.Read(ctx, "sample")
					if err != nil {
						t.Fatal(err)
					}
					var ids []string
					for _, row := range rows.Rows {
						ids = append(ids, fmt.Sprint(row["id"]))
					}
					slices.Sort(ids)
					want := []string{"Negative:block/0x2", "Negative:block/0x5", "Negative:block/0x8", "Positive:block/0x2", "Positive:block/0x5", "Positive:block/0x8"}
					if !slices.Equal(ids, want) {
						t.Fatalf("attempt %d: rows %v", attempt, ids)
					}
					if err = b.Close(); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestCompiledIndexerAdmission(t *testing.T) {
	ctx := t.Context()
	url := testanvil.StartChain(t, 101)
	model := &store.Store{Ordering: schema.Multichain}
	h := Handler(func(context.Context, *Context) error { return nil })
	build := func(order string, fn any) *config.SourceIndexingBuild {
		t.Helper()
		raw := fmt.Sprintf(`{"ordering":%q,"chains":{"one":{"id":101,"rpc":%q}},"blocks":{"Head":{"chain":"one","interval":1,"startBlock":1,"endBlock":2}}}`, order, url)
		cfg, err := config.ParseSourceConfiguration([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := config.BuildSourceIndexing(ctx, cfg, base, []config.SourceIndexingFunction{{Name: "Head:block", Fn: fn}})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	one := build("multichain", h)
	t.Run("single_participant", func(t *testing.T) {
		indexer, err := NewIndexerFromSource(one, model, Options{})
		if err != nil {
			t.Fatal(err)
		}
		engine, ok := indexer.(*Engine)
		if !ok || engine.endBlock == nil || *engine.endBlock != 2 || engine.chain.Name != "one" {
			t.Fatal("unexpected single-chain worker", indexer)
		}
	})
	for _, tc := range []struct {
		name   string
		build  *config.SourceIndexingBuild
		db     *store.Store
		reason string
	}{
		{"nil_store", one, nil, "requires a store"},
		{"nil_build", nil, model, "participating chain"},
		{"empty_build", &config.SourceIndexingBuild{}, model, "participating chain"},
		{"store_order", one, &store.Store{Ordering: schema.Omnichain}, "ordering differ"},
		{"invalid_order", build("unknown", h), model, "invalid ordering"},
		{"nil_handler", build("multichain", Handler(nil)), model, "non-nil native runtime.Handler"},
		{"wrong_handler", build("multichain", func() {}), model, "native runtime.Handler"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			indexer, err := NewIndexerFromSource(tc.build, tc.db, Options{})
			if indexer != nil || err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("indexer=%v err=%v, want %s", indexer, err, tc.reason)
			}
		})
	}
}
