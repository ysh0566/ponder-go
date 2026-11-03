package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type omniLogsEvent struct {
	Key        string  `json:"key"`
	Checkpoint string  `json:"checkpoint"`
	Timestamp  string  `json:"timestamp"`
	Hash       string  `json:"hash"`
	Value      *string `json:"value"`
}
type omniLogsSnapshot struct {
	Pass        int                             `json:"pass"`
	Error       *struct{ Name, Message string } `json:"error"`
	Calls       []omniLogsEvent                 `json:"calls"`
	Rows        []map[string]any                `json:"rows"`
	Checkpoints []struct {
		ID        uint64 `json:"chain_id"`
		Safe      string `json:"safe_checkpoint"`
		Latest    string `json:"latest_checkpoint"`
		Finalized string `json:"finalized_checkpoint"`
	} `json:"checkpoints"`
	Ready bool `json:"ready"`
}
type omniLogsCase struct {
	Name      string             `json:"name"`
	QuerySize int                `json:"query_size"`
	FailKey   string             `json:"fail_key"`
	Snapshots []omniLogsSnapshot `json:"snapshots"`
}
type omniLogsGolden struct {
	SourceCommit string         `json:"source_commit"`
	ABIItem      map[string]any `json:"abi_item"`
	Address      string         `json:"address"`
	Chains       []struct {
		ID     uint64           `json:"id"`
		Latest string           `json:"latest"`
		Blocks []map[string]any `json:"blocks"`
		Logs   []map[string]any `json:"logs"`
	} `json:"chains"`
	Cases []omniLogsCase `json:"cases"`
}

func loadOmniLogsGolden(t *testing.T) omniLogsGolden {
	t.Helper()
	raw, err := os.ReadFile("testdata/runtime_omnichain_logs_source_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g omniLogsGolden
	if err = json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.SourceCommit != "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334" || len(g.Chains) != 2 || len(g.Cases) != 5 {
		t.Fatal("unexpected pinned log oracle")
	}
	return g
}

type omniLogsRPCCounts struct {
	mu         sync.Mutex
	Logs, Full map[uint64]int
}

func omniLogsRPC(t *testing.T, g omniLogsGolden) (*finiteRPC, *omniLogsRPCCounts) {
	t.Helper()
	blocks := map[uint64]map[string]map[string]any{}
	logs := map[uint64][]map[string]any{}
	latest := map[uint64]string{}
	hashes := map[string]map[string]any{}
	for _, c := range g.Chains {
		blocks[c.ID] = map[string]map[string]any{}
		latest[c.ID] = c.Latest
		logs[c.ID] = c.Logs
		for _, b := range c.Blocks {
			blocks[c.ID][fmt.Sprint(b["number"])] = b
			hashes[fmt.Sprint(b["hash"])] = b
		}
	}
	counts := &omniLogsRPCCounts{Logs: map[uint64]int{}, Full: map[uint64]int{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			http.Error(w, "invalid chain", 400)
			return
		}
		var result any
		switch q.Method {
		case "eth_getLogs":
			f, ok := q.Params[0].(map[string]any)
			if !ok {
				http.Error(w, "log filter", 400)
				return
			}
			from, fe := uintQuantity(f["fromBlock"])
			to, te := uintQuantity(f["toBlock"])
			if fe != nil || te != nil {
				http.Error(w, "log range", 400)
				return
			}
			selected := []map[string]any{}
			for _, l := range logs[id] {
				n, e := uintQuantity(l["blockNumber"])
				if e == nil && n >= from && n <= to {
					selected = append(selected, l)
				}
			}
			result = selected
			counts.mu.Lock()
			counts.Logs[id]++
			counts.mu.Unlock()
		case "eth_getBlockByNumber", "eth_getBlockByHash":
			var b map[string]any
			if q.Method == "eth_getBlockByHash" {
				b = hashes[fmt.Sprint(q.Params[0])]
			} else {
				n := fmt.Sprint(q.Params[0])
				if n == "latest" {
					n = latest[id]
				}
				b = blocks[id][n]
			}
			if b == nil {
				http.Error(w, "fixture block absent", 400)
				return
			}
			raw, _ := json.Marshal(b)
			var clone map[string]any
			_ = json.Unmarshal(raw, &clone)
			full := len(q.Params) > 1 && q.Params[1] == true
			if full {
				counts.mu.Lock()
				counts.Full[id]++
				counts.mu.Unlock()
			} else {
				txs := []any{}
				for _, tx := range clone["transactions"].([]any) {
					txs = append(txs, tx.(map[string]any)["hash"])
				}
				clone["transactions"] = txs
			}
			result = clone
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32601, "message": "unexpected log source RPC " + q.Method}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	return &finiteRPC{server: server}, counts
}
func omniLogsBuild(t *testing.T, g omniLogsGolden, p *finiteRPC, handlers map[string]indexing.Handler) *config.SourceIndexingBuild {
	t.Helper()
	chainConfig := map[string]any{"one": map[string]any{"id": 1, "rpc": p.server.URL + "/1", "reorgWindow": 0, "cacheRpcRequests": true}, "two": map[string]any{"id": 2, "rpc": p.server.URL + "/2", "reorgWindow": 0, "cacheRpcRequests": true}}
	chainSelection := map[string]any{"one": map[string]any{}, "two": map[string]any{}}
	contracts := map[string]any{}
	for _, kind := range []string{"A", "B"} {
		contracts["Token"+kind] = map[string]any{"abi": []any{g.ABIItem}, "address": g.Address, "chain": chainSelection, "startBlock": 1, "endBlock": 3}
	}
	raw, err := json.Marshal(map[string]any{"ordering": "omnichain", "chains": chainConfig, "contracts": contracts, "blocks": map[string]any{"Blocks": map[string]any{"chain": chainSelection, "startBlock": 1, "endBlock": 3, "interval": 1}}})
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
	for _, r := range base.RPCClients() {
		r.RequestsPerSecond = 0
		r.Retries = 0
	}
	b, err := config.BuildSourceIndexing(context.Background(), cfg, base, []config.SourceIndexingFunction{{Name: "Blocks:block", Fn: handlers["block"]}, {Name: "TokenA:Value", Fn: handlers["A"]}, {Name: "TokenB:Value", Fn: handlers["B"]}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func omniLogsFields(v any, keys ...string) (map[string]any, error) {
	out := map[string]any{}
	switch value := v.(type) {
	case *indexing.PropertyView:
		for _, key := range keys {
			got, err := value.Get(key)
			if err != nil {
				return nil, err
			}
			out[key] = got
		}
	case map[string]any:
		for _, key := range keys {
			out[key] = value[key]
		}
	default:
		return nil, fmt.Errorf("missing source event projection %T", v)
	}
	return out, nil
}
func assertOmniLogsSnapshot(t *testing.T, f finiteFixture, c store.ManagedLiveControl, want omniLogsSnapshot) {
	t.Helper()
	for _, cp := range want.Checkpoints {
		if c.LogicalCursors[cp.ID] != cp.Latest || c.LogicalSafe[cp.ID] != cp.Safe || c.LogicalFinality[cp.ID] != cp.Finalized {
			t.Fatalf("source log recovery positions differ chain%d got%+v want%+v", cp.ID, c, cp)
		}
	}
	managedInspect(t, f, func(db store.Backend) {
		rows := managedRows(t, db, "SELECT id,value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
		if len(rows) != len(want.Rows) || (len(rows) > 0 && !reflect.DeepEqual(rows, want.Rows)) {
			t.Fatalf("merged log SQL differs got%v want%v", rows, want.Rows)
		}
		rows = managedRows(t, db, "SELECT operation_id FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".\"_ponder_go_journal\"")
		if len(rows) != 0 {
			t.Fatalf("finalized log undo retained %v", rows)
		}
	})
}
func TestRuntimeOmnichainStaticLogsSource(t *testing.T) {
	g := loadOmniLogsGolden(t)
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			for _, tc := range g.Cases {
				t.Run(tc.Name, func(t *testing.T) {
					continuousRoot(t)
					p, counts := omniLogsRPC(t, g)
					pass := 0
					calls := []string{}
					handlers := omniLogsHandlers(func() []omniLogsEvent { return tc.Snapshots[pass-1].Calls }, &calls, func(key string) error {
						if pass == 1 && key == tc.FailKey {
							return errors.New("controlled static log handler failure after shared write")
						}
						return nil
					})
					build := omniLogsBuild(t, g, p, handlers)
					f := newManagedFiniteFixture(t, backend, build)
					omnichainGoldenStorage(&f)
					epochs := []uint64{}
					o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omnichain-static-logs-v1-" + tc.Name, Requesters: runtimeFactory(p, &epochs)}
					if tc.QuerySize != 12000 {
						o.Historical.QuerySize = tc.QuerySize
					}
					for _, want := range tc.Snapshots {
						pass = want.Pass
						calls = []string{}
						o.OpenID = fmt.Sprintf("static-log-%d", pass)
						r := runtimeOpen(t, backend, o)
						out, err := r.Run(context.Background())
						if want.Error != nil {
							if err == nil || !strings.Contains(err.Error(), "controlled static log") || out.Ready || out.Completed {
								t.Fatalf("source failure result%+v err%v", out, err)
							}
						} else if err != nil || !out.Ready || !out.Completed {
							t.Fatalf("source log result%+v err%v", out, err)
						}
						if pass > 1 && !out.Recovered {
							t.Fatalf("cold source log open not recovered %+v", out)
						}
						expected := []string{}
						for _, ev := range want.Calls {
							expected = append(expected, ev.Key)
						}
						if !reflect.DeepEqual(calls, expected) {
							t.Fatalf("source log handler sequence got%v want%v", calls, expected)
						}
						ready, e := r.Ready(context.Background())
						if e != nil || ready != want.Ready {
							t.Fatalf("source Ready mismatch got%v err%v want%v", ready, e, want.Ready)
						}
						c, e := r.Control(context.Background())
						if e != nil {
							t.Fatal(e)
						}
						if e = r.Close(context.Background()); e != nil {
							t.Fatal(e)
						}
						assertOmniLogsSnapshot(t, f, c, want)
					}
					counts.mu.Lock()
					defer counts.mu.Unlock()
					for _, id := range []uint64{1, 2} {
						if counts.Logs[id] == 0 || counts.Full[id] == 0 {
							t.Fatalf("chain%d did not use real log/full-block RPC %+v", id, counts)
						}
					}
				})
			}
		})
	}
}

func omniLogsHandlers(expected func() []omniLogsEvent, calls *[]string, onWritten func(string) error) map[string]indexing.Handler {
	handlers := map[string]indexing.Handler{}
	for _, kind := range []string{"block", "A", "B"} {
		handlers[kind] = func(ctx context.Context, a *indexing.HandlerArgs) error {
			block, err := omniLogsFields(a.Event.Get("block"), "number", "hash", "timestamp")
			if err != nil {
				return err
			}
			n, err := uintQuantity(block["number"])
			if err != nil {
				return err
			}
			logIndex := "-1"
			var value *string
			if kind != "block" {
				log, err := omniLogsFields(a.Event.Get("log"), "logIndex")
				if err != nil {
					return err
				}
				index, err := uintQuantity(log["logIndex"])
				if err != nil {
					return err
				}
				logIndex = strconv.FormatUint(index, 10)
				args, err := omniLogsFields(a.Event.Get("args"), "value")
				if err != nil {
					return err
				}
				text := fmt.Sprint(args["value"])
				value = &text
			}
			key := fmt.Sprintf("%d:%d:%s:%s", finiteChain(a), n, kind, logIndex)
			wantEvents := expected()
			index := len(*calls)
			if index >= len(wantEvents) {
				return fmt.Errorf("extra log handler %s", key)
			}
			want := wantEvents[index]
			if key != want.Key || a.Event.Get("id") != want.Checkpoint || fmt.Sprint(block["hash"]) != want.Hash || fmt.Sprint(block["timestamp"]) != want.Timestamp || !reflect.DeepEqual(value, want.Value) {
				return fmt.Errorf("source log event differs index%d key%s want%+v", index, key, want)
			}
			row, err := a.Context.DB.Find(ctx, "account", store.Row{"id": "shared"})
			if err != nil {
				return err
			}
			if row == nil {
				_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "value": key}, store.Conflict{})
			} else {
				_, err = a.Context.DB.Update(ctx, "account", store.Row{"id": "shared"}, store.Row{"value": fmt.Sprint(row["value"]) + "|" + key})
			}
			if err != nil {
				return err
			}
			*calls = append(*calls, key)
			if onWritten != nil {
				return onWritten(key)
			}
			return nil
		}
	}

	return handlers
}
