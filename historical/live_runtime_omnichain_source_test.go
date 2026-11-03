package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type omnichainGolden struct {
	SourceCommit string `json:"source_commit"`
	Chains       []struct {
		ID     uint64           `json:"id"`
		Latest string           `json:"latest_block"`
		Start  string           `json:"start_block"`
		End    string           `json:"end_block"`
		Blocks []map[string]any `json:"blocks"`
	} `json:"chains"`
	Expected struct {
		Order       []string                                            `json:"handler_order"`
		Events      []struct{ Key, Checkpoint, Timestamp, Hash string } `json:"handler_events"`
		Rows        []map[string]any                                    `json:"business_rows"`
		Checkpoints []struct {
			ID        uint64 `json:"chain_id"`
			Safe      string `json:"safe_checkpoint"`
			Latest    string `json:"latest_checkpoint"`
			Finalized string `json:"finalized_checkpoint"`
		} `json:"checkpoints"`
	} `json:"native_expected"`
}

func loadOmnichainGolden(t *testing.T) omnichainGolden {
	return loadOmnichainGoldenPath(t, "testdata/runtime_omnichain_source_golden.json")
}
func loadOmnichainGoldenPath(t *testing.T, path string) omnichainGolden {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var g omnichainGolden
	if err = json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.SourceCommit != "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334" || len(g.Expected.Order) != 4 || len(g.Chains) != 2 {
		t.Fatal("unexpected source oracle fixture")
	}
	return g
}
func omnichainGoldenRPC(t *testing.T, g omnichainGolden, latestOverride string) *finiteRPC {
	t.Helper()
	byChain := map[uint64]map[string]map[string]any{}
	latest := map[uint64]string{}
	hashes := map[string]map[string]any{}
	for _, c := range g.Chains {
		byChain[c.ID] = map[string]map[string]any{}
		latest[c.ID] = c.Latest
		if latestOverride != "" {
			latest[c.ID] = latestOverride
		}
		for _, b := range c.Blocks {
			byChain[c.ID][fmt.Sprint(b["number"])] = b
			hashes[fmt.Sprint(b["hash"])] = b
		}
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ID     json.RawMessage
			Method string
			Params []any
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		id, err := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/"), 10, 64)
		if err != nil {
			http.Error(w, "chain", 400)
			return
		}
		var result any
		switch q.Method {
		case "eth_getBlockByNumber":
			n := fmt.Sprint(q.Params[0])
			if n == "latest" {
				n = latest[id]
			}
			result = byChain[id][n]
		case "eth_getBlockByHash":
			result = hashes[fmt.Sprint(q.Params[0])]
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32601, "message": "unexpected source fixture RPC " + q.Method}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
	}))
	t.Cleanup(s.Close)
	return &finiteRPC{server: s}
}
func omnichainGoldenBuild(t *testing.T, p *finiteRPC, h indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	b := omnichainRuntimeBuild(t, p, 2, h)
	for _, c := range b.Executable().Chains {
		c["reorgWindow"] = uint64(0)
	}
	return b
}
func omnichainGoldenStorage(f *finiteFixture) {
	f.options.Owned.Storage.Schema = schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: "text", NotNull: true}}}}}
}
func omnichainGoldenHandler(g omnichainGolden, calls *[]string, crash func(int)) indexing.Handler {
	return func(ctx context.Context, a *indexing.HandlerArgs) error {
		block := map[string]any{}
		switch v := a.Event.Get("block").(type) {
		case *indexing.PropertyView:
			for _, key := range []string{"number", "hash", "timestamp"} {
				value, err := v.Get(key)
				if err != nil {
					return err
				}
				block[key] = value
			}
		case map[string]any:
			block = v
		default:
			return fmt.Errorf("missing block projection")
		}
		n, err := uintQuantity(block["number"])
		if err != nil {
			return err
		}
		key := fmt.Sprintf("%d:%d", finiteChain(a), n)
		i := len(*calls)
		if i >= len(g.Expected.Events) {
			return fmt.Errorf("extra handler %s", key)
		}
		want := g.Expected.Events[i]
		if key != want.Key || a.Event.Get("id") != want.Checkpoint || fmt.Sprint(block["hash"]) != want.Hash || fmt.Sprint(block["timestamp"]) != want.Timestamp {
			return fmt.Errorf("source event mismatch index%d key%s cp%v block%v want%+v", i, key, a.Event.Get("id"), block, want)
		}
		row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": "shared"})
		if err != nil {
			return err
		}
		value := key
		if row == nil {
			_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "value": value}, store.Conflict{})
		} else {
			value = fmt.Sprint(row["value"]) + "|" + key
			_, err = a.Context.DB.Update(ctx, "account", store.Row{"id": "shared"}, store.Row{"value": value})
		}
		if err != nil {
			return err
		}
		*calls = append(*calls, key)
		if crash != nil {
			crash(len(*calls))
		}
		return nil
	}
}
func assertOmnichainGoldenResult(t *testing.T, g omnichainGolden, f finiteFixture, c store.ManagedLiveControl) {
	t.Helper()
	if c.Version != "managed-live-omnichain-finalized-v11" {
		t.Fatalf("format %s", c.Version)
	}
	for _, want := range g.Expected.Checkpoints {
		if c.Cursors[want.ID].Checkpoint != want.Latest || c.SafeHeads[want.ID].Checkpoint != want.Safe || c.Finality[want.ID].Checkpoint != want.Finalized {
			t.Fatalf("source checkpoint mismatch chain%d control%+v", want.ID, c)
		}
	}
	managedInspect(t, f, func(db store.Backend) {
		rows := managedRows(t, db, "SELECT id,value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
		if !reflect.DeepEqual(rows, g.Expected.Rows) {
			t.Fatalf("source business mismatch got%v want%v", rows, g.Expected.Rows)
		}
		rows = managedRows(t, db, "SELECT operation_id FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".\"_ponder_go_journal\"")
		if len(rows) != 0 {
			t.Fatalf("finalized undo retained: %v", rows)
		}
	})
}
func TestRuntimeOmnichainSourceOracle(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			g := loadOmnichainGolden(t)
			p := omnichainGoldenRPC(t, g, "")
			calls := []string{}
			b := omnichainGoldenBuild(t, p, omnichainGoldenHandler(g, &calls, nil))
			f := newManagedFiniteFixture(t, backend, b)
			omnichainGoldenStorage(&f)
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omnichain-source-oracle-v1", OpenID: "oracle-1", Requesters: runtimeFactory(p, &epochs)}
			r := runtimeOpen(t, backend, o)
			out, err := r.Run(context.Background())
			if err != nil || !out.Completed || !out.Ready || !reflect.DeepEqual(calls, g.Expected.Order) {
				t.Fatalf("source run %+v calls%v err%v", out, calls, err)
			}
			c, err := r.Control(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertOmnichainGoldenResult(t, g, f, c)
			o.OpenID = "oracle-2"
			r = runtimeOpen(t, backend, o)
			out, err = r.Run(context.Background())
			if err != nil || !out.Completed || !out.Ready || !out.Recovered || !reflect.DeepEqual(calls, g.Expected.Order) {
				t.Fatalf("source cold reopen %+v calls%v err%v", out, calls, err)
			}
			c, err = r.Control(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertOmnichainGoldenResult(t, g, f, c)
		})
	}
}
func TestRuntimeOmnichainPreflightBoundary(t *testing.T) {
	for _, name := range []string{"global-finality", "local-finality", "live", "start0", "interval", "factory", "cache-false", "ordering-mismatch"} {
		t.Run(name, func(t *testing.T) {
			g := loadOmnichainGolden(t)
			latest := ""
			if name == "global-finality" || name == "local-finality" {
				latest = "0x2"
			}
			p := omnichainGoldenRPC(t, g, latest)
			calls := []string{}
			b := omnichainGoldenBuild(t, p, omnichainGoldenHandler(g, &calls, nil))
			epochs := []uint64{}
			o := RuntimeOptions{Historical: Options{Build: b}, BuildID: "omni-guard", OpenID: "guard-1", Requesters: runtimeFactory(p, &epochs)}
			expected := "bounded finalized"
			switch name {
			case "global-finality":
				expected = "global finalized watermark"
			case "local-finality":
				for _, c := range b.Executable().Chains {
					c["reorgWindow"] = uint64(1)
				}
				expected = "chain finality"
			case "live":
				o.Live = &RuntimeLiveOptions{}
			case "start0":
				b.Executable().EventCallbacks[0][0]["filter"].(map[string]any)["fromBlock"] = float64(0)
				expected = "startBlock>=1"
			case "interval":
				b.Executable().EventCallbacks[0][0]["filter"].(map[string]any)["interval"] = float64(2)
				expected = "interval1 block callback"
			case "factory":
				o.FactorySources = true
				expected = "factory sources require"
			case "cache-false":
				for _, c := range b.Executable().Chains {
					c["cacheRpcRequests"] = false
				}
				expected = "cacheRpcRequests=true"
			case "ordering-mismatch":
				o.Historical.Owned.Storage.Ordering = schema.Multichain
				expected = "unsupported runtime ordering"
			}
			opened := false
			r, err := openRuntime(context.Background(), o, func(context.Context, store.IndependentStorageOptions, store.ManagedLiveRuntimeOptions) (*store.ManagedLiveDomains, error) {
				opened = true
				return nil, errors.New("storage must not open")
			})
			if r != nil || err == nil || !strings.Contains(err.Error(), expected) || opened || len(calls) != 0 {
				t.Fatalf("unsafe preflight name%s runner%v opened%v calls%v err%v", name, r, opened, calls, err)
			}
		})
	}
}

type omnichainCrashSpec struct {
	RPC, Phase, Marker string
	Storage            store.IndependentStorageOptions
}

func omnichainCrashChild(t *testing.T, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s omnichainCrashSpec
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	g := loadOmnichainGolden(t)
	p := &finiteRPC{server: &httptest.Server{URL: s.RPC}}
	calls := []string{}
	exit := func() {
		raw, _ := json.Marshal(map[string]any{"phase": s.Phase, "calls": calls, "pid": os.Getpid(), "exit": 83, "postgres_engine_stays_running": true})
		if os.WriteFile(s.Marker, raw, 0600) != nil {
			os.Exit(87)
		}
		os.Exit(83)
	}
	h := omnichainGoldenHandler(g, &calls, func(n int) {
		if s.Phase == "handler" && n == 2 {
			exit()
		}
	})
	b := omnichainGoldenBuild(t, p, h)
	epochs := []uint64{}
	o := RuntimeOptions{Historical: Options{Build: b, Owned: indexing.OwnedOptions{Storage: s.Storage, Service: indexing.Options{Errors: &indexing.ErrorState{}}}}, BuildID: "omni-app-exit-v1", OpenID: "exit-1", Requesters: runtimeFactory(p, &epochs), OnActivated: func(context.Context) error {
		if s.Phase == "activated" {
			exit()
		}
		return nil
	}}
	r, err := OpenRuntimePostgres(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background())
	t.Fatalf("child did not exit: %v", err)
}
func TestRuntimeOmnichainApplicationExit(t *testing.T) {
	if path := os.Getenv("PONDER_M45_OMNICHAIN_CRASH_SPEC"); path != "" {
		omnichainCrashChild(t, path)
		return
	}
	for _, phase := range []string{"handler", "activated"} {
		t.Run("postgres/"+phase, func(t *testing.T) {
			continuousRoot(t)
			g := loadOmnichainGolden(t)
			p := omnichainGoldenRPC(t, g, "")
			calls := []string{}
			b := omnichainGoldenBuild(t, p, omnichainGoldenHandler(g, &calls, nil))
			f := newManagedFiniteFixture(t, "postgres", b)
			omnichainGoldenStorage(&f)
			dir, err := os.MkdirTemp(os.Getenv("PONDER_M45_TEST_ROOT"), "omni-app-exit-"+phase+"-")
			if err != nil {
				t.Fatal(err)
			}
			if real, e := filepath.EvalSymlinks(dir); e != nil || !strings.HasPrefix(real, "/root/repo/") {
				t.Fatalf("invalid retained root %s %v", real, e)
			}
			marker := filepath.Join(dir, "exit-marker.json")
			spec := omnichainCrashSpec{RPC: p.server.URL, Phase: phase, Marker: marker, Storage: f.options.Owned.Storage}
			raw, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "child-spec.json")
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRuntimeOmnichainApplicationExit$", "-test.v")
			cmd.Env = append(os.Environ(), "PONDER_M45_OMNICHAIN_CRASH_SPEC="+path)
			output, err := cmd.CombinedOutput()
			if e := os.WriteFile(filepath.Join(dir, "child.log"), output, 0600); e != nil {
				t.Fatal(e)
			}
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 83 || ctx.Err() != nil {
				t.Fatalf("app exit err%v ctx%v output%s", err, ctx.Err(), output)
			}
			raw, err = os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			var proof struct {
				Phase string
				Calls []string
				Exit  int
			}
			if err = json.Unmarshal(raw, &proof); err != nil || proof.Phase != phase || proof.Exit != 83 {
				t.Fatalf("bad marker %s %v", raw, err)
			}
			if phase == "handler" {
				if !reflect.DeepEqual(proof.Calls, g.Expected.Order[:2]) {
					t.Fatalf("wrong handler crash prefix %v", proof.Calls)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT id,value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account")
					if len(rows) != 0 {
						t.Fatalf("uncommitted handler escaped process exit %v", rows)
					}
				})
			}
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omni-app-exit-v1", OpenID: "exit-parent-2", Requesters: runtimeFactory(p, &epochs)}
			r := runtimeOpen(t, "postgres", o)
			out, err := r.Run(context.Background())
			if err != nil || !out.Completed || !out.Ready || !out.Recovered {
				t.Fatalf("app exit recovery %+v %v", out, err)
			}
			if phase == "handler" && !reflect.DeepEqual(calls, g.Expected.Order) {
				t.Fatalf("recovery not source global replay %v", calls)
			}
			if phase == "activated" && len(calls) != 0 {
				t.Fatalf("finalized activation replayed %v", calls)
			}
			c, err := r.Control(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertOmnichainGoldenResult(t, g, f, c)
		})
	}
}

type omnichainFailureGolden struct {
	Cases []struct {
		Name          string `json:"name"`
		FailAt        int    `json:"fail_at"`
		SourceReceipt string `json:"source_receipt"`
		Expected      struct {
			FailedOrder []string         `json:"failed_handler_order"`
			FailedRows  []map[string]any `json:"failed_business_rows"`
			Checkpoints []struct {
				ID        uint64 `json:"chain_id"`
				Latest    string `json:"latest_checkpoint"`
				Safe      string `json:"safe_checkpoint"`
				Finalized string `json:"finalized_checkpoint"`
			} `json:"failed_checkpoints"`
			RetryOrder []string `json:"retry_handler_order"`
		} `json:"native_expected"`
	} `json:"cases"`
}

func TestRuntimeOmnichainSourceHandlerFailure(t *testing.T) {
	raw, err := os.ReadFile("testdata/runtime_omnichain_failure_source_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture omnichainFailureGolden
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"postgres", "pglite"} {
		for _, fault := range fixture.Cases {
			t.Run(backend+"/"+fault.Name, func(t *testing.T) {
				continuousRoot(t)
				g := loadOmnichainGolden(t)
				p := omnichainGoldenRPC(t, g, "")
				calls := []string{}
				failed := false
				expectedEvents := g.Expected.Events
				h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					current := g
					current.Expected.Events = expectedEvents
					if err := omnichainGoldenHandler(current, &calls, nil)(ctx, a); err != nil {
						return err
					}
					if len(calls) == fault.FailAt && !failed {
						failed = true
						return errors.New("source controlled handler failure after shared write")
					}
					return nil
				})
				b := omnichainGoldenBuild(t, p, h)
				f := newManagedFiniteFixture(t, backend, b)
				omnichainGoldenStorage(&f)
				epochs := []uint64{}
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omni-source-failure-v1", OpenID: "failure-1", Requesters: runtimeFactory(p, &epochs)}
				r := runtimeOpen(t, backend, o)
				out, err := r.Run(context.Background())
				if err == nil || !failed || out.Ready || out.Completed || !reflect.DeepEqual(calls, fault.Expected.FailedOrder) {
					t.Fatalf("source ordinary failure result%+v calls%v failed%v err%v", out, calls, failed, err)
				}
				if ready, e := r.Ready(context.Background()); e != nil || ready {
					t.Fatalf("failed source batch advertised ready %v %v", ready, e)
				}
				c, err := r.Control(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range fault.Expected.Checkpoints {
					if c.LogicalCursors[want.ID] != want.Latest || c.LogicalSafe[want.ID] != want.Safe || c.LogicalFinality[want.ID] != want.Finalized {
						t.Fatalf("source failure checkpoint mismatch chain%d got%+v want%+v", want.ID, c, want)
					}
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT id,value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
					if len(rows) != len(fault.Expected.FailedRows) || (len(rows) > 0 && !reflect.DeepEqual(rows, fault.Expected.FailedRows)) {
						t.Fatalf("source merged transaction mismatch got%v want%v", rows, fault.Expected.FailedRows)
					}
					rows = managedRows(t, db, "SELECT operation_id FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".\"_ponder_go_journal\"")
					if len(rows) != 0 {
						t.Fatalf("source finalized history retained undo: %v", rows)
					}
				})
				calls = []string{}
				expectedEvents = nil
				for _, key := range fault.Expected.RetryOrder {
					for _, ev := range g.Expected.Events {
						if ev.Key == key {
							expectedEvents = append(expectedEvents, ev)
						}
					}
				}
				o.OpenID = "failure-2"
				r = runtimeOpen(t, backend, o)
				out, err = r.Run(context.Background())
				if err != nil || !out.Ready || !out.Completed || !out.Recovered || !reflect.DeepEqual(calls, fault.Expected.RetryOrder) {
					t.Fatalf("source failure cold replay result%+v calls%v want%v err%v", out, calls, fault.Expected.RetryOrder, err)
				}
				c, err = r.Control(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				assertOmnichainGoldenResult(t, g, f, c)
			})
		}
	}
}

func TestRuntimeOmnichainDifferentStarts(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			g := loadOmnichainGoldenPath(t, "testdata/runtime_omnichain_different_starts_source_golden.json")
			p := omnichainGoldenRPC(t, g, "")
			calls := []string{}
			chainSettings := map[string]any{}
			blockSettings := map[string]any{}
			for _, c := range g.Chains {
				name := "one"
				if c.ID == 2 {
					name = "two"
				}
				start, err := strconv.ParseUint(c.Start, 0, 64)
				if err != nil {
					t.Fatal(err)
				}
				end, err := strconv.ParseUint(c.End, 0, 64)
				if err != nil {
					t.Fatal(err)
				}
				chainSettings[name] = map[string]any{"id": c.ID, "rpc": fmt.Sprintf("%s/%d", p.server.URL, c.ID), "reorgWindow": 0, "cacheRpcRequests": true}
				blockSettings[name] = map[string]any{"startBlock": start, "endBlock": end}
			}
			raw, err := json.Marshal(map[string]any{"ordering": "omnichain", "chains": chainSettings, "blocks": map[string]any{"Blocks": map[string]any{"chain": blockSettings, "interval": 1, "startBlock": 1, "endBlock": 3}}})
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseSourceConfiguration(raw)
			if err != nil {
				t.Fatal(err)
			}
			base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{RPCMaxConcurrency: 2})
			if err != nil {
				t.Fatal(err)
			}
			for _, client := range base.RPCClients() {
				client.RequestsPerSecond = 0
				client.Retries = 0
			}
			b, err := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "Blocks:block", Fn: omnichainGoldenHandler(g, &calls, nil)}})
			if err != nil {
				t.Fatal(err)
			}
			f := newManagedFiniteFixture(t, backend, b)
			omnichainGoldenStorage(&f)
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omni-different-starts-v1", OpenID: "different-1", Requesters: runtimeFactory(p, &epochs)}
			r := runtimeOpen(t, backend, o)
			out, err := r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || !reflect.DeepEqual(calls, g.Expected.Order) {
				t.Fatalf("different starts output%+v calls%v want%v err%v", out, calls, g.Expected.Order, err)
			}
			c, err := r.Control(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertOmnichainGoldenResult(t, g, f, c)
			o.OpenID = "different-2"
			r = runtimeOpen(t, backend, o)
			out, err = r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || !out.Recovered || !reflect.DeepEqual(calls, g.Expected.Order) {
				t.Fatalf("different starts reopen%+v calls%v err%v", out, calls, err)
			}
			c, err = r.Control(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertOmnichainGoldenResult(t, g, f, c)
		})
	}
}
