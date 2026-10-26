package config_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
)

func TestExecutableSourceActualHandlers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Method != "eth_getBlockByNumber" {
			t.Error("unexpected build RPC", req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"hash": "0x" + strings.Repeat("1", 64), "parentHash": "0x" + strings.Repeat("0", 64), "number": "0xa", "timestamp": "0x64", "logsBloom": "0x" + strings.Repeat("0", 512)}})
	}))
	defer server.Close()
	cfg, err := config.ParseSourceConfiguration([]byte(fmt.Sprintf(`{"chains":{"one":{"id":1,"rpc":%q,"reorgWindow":0}},"contracts":{"Token":{"chain":"one","address":["0x1111111111111111111111111111111111111111"],"abi":[{"type":"event","name":"A","inputs":[]},{"type":"event","name":"B","inputs":[]}],"startBlock":0,"endBlock":1}},"blocks":{"Block":{"chain":"one","interval":1,"startBlock":0,"endBlock":1}}}`, server.URL)))
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.BuildSourceConfig(cfg, config.SourceBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seen := []string{}
	names := []string{"Block:block", "Token:B", "Token:A"}
	functions := []config.SourceIndexingFunction{}
	for _, name := range names {
		name := name
		functions = append(functions, config.SourceIndexingFunction{Name: name, Fn: indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { seen = append(seen, name); return nil })})
	}
	build, err := config.BuildSourceIndexing(context.Background(), cfg, base, functions)
	if err != nil {
		t.Fatal(err)
	}
	live := build.Executable()
	if live.Ordering != "multichain" || len(live.RPCs) != 1 {
		t.Fatal("source default or actual clients lost")
	}
	for i, fn := range live.IndexingFunctions {
		if fn["name"] != names[i] {
			t.Fatal("registration order lost")
		}
		handler, ok := fn["fn"].(indexing.Handler)
		if !ok {
			t.Fatal("actual Handler not retained")
		}
		if err = handler(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(seen, names) {
		t.Fatal(seen)
	}
	var contracts []map[string]any
	for _, cb := range live.EventCallbacks[0] {
		if reflect.ValueOf(cb["chain"]).Pointer() != reflect.ValueOf(live.Chains[0]).Pointer() {
			t.Fatal("callback chain not canonical")
		}
		if cb["type"] == "contract" {
			contracts = append(contracts, cb)
		}
	}
	if len(contracts) != 2 {
		t.Fatal("contract callback count", len(contracts))
	}
	a := contracts[0]["filter"].(map[string]any)["address"].([]any)
	b := contracts[1]["filter"].(map[string]any)["address"].([]any)
	a[0] = "actual-alias-probe"
	if b[0] != a[0] {
		t.Fatal("actual source shared address array was rebuilt")
	}
	if build.Executable() != live {
		t.Fatal("projection not cached")
	}
}
