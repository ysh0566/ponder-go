package historical

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
	"strings"
	"testing"
)

func TestRuntimeSourceSetupOnlyBoundary(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("mixed_%t", mixed), func(t *testing.T) {
			p := newFiniteRPC(t, 3)
			chains := `"one":{},"two":{}`
			blocks := ""
			called := 0
			funcs := []config.SourceIndexingFunction{{Name: "Setup:setup", Fn: indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { called++; return nil })}}
			if mixed {
				chains = `"one":{}`
				blocks = `,"blocks":{"Blocks":{"chain":{"two":{}},"startBlock":1,"endBlock":3,"interval":1}}`
				funcs = append(funcs, config.SourceIndexingFunction{Name: "Blocks:block", Fn: indexing.Handler(finiteIncrement)})
			}
			raw := fmt.Sprintf(`{"ordering":"multichain","chains":{"one":{"id":1,"rpc":%q,"reorgWindow":0},"two":{"id":2,"rpc":%q,"reorgWindow":0}},"contracts":{"Setup":{"abi":[],"chain":{%s},"startBlock":1,"endBlock":3}}%s}`, p.server.URL+"/1", p.server.URL+"/2", chains, blocks)
			cfg, e := config.ParseSourceConfiguration([]byte(raw))
			if e != nil {
				t.Fatal(e)
			}
			base, e := config.BuildSourceConfig(cfg, config.SourceBuildOptions{})
			if e != nil {
				t.Fatal(e)
			}
			for _, r := range base.RPCClients() {
				r.RequestsPerSecond = 0
				r.Retries = 0
			}
			b, e := config.BuildSourceIndexing(context.Background(), cfg, base, funcs)
			if e != nil {
				t.Fatal(e)
			}
			x := b.Executable()
			if len(x.Chains) != 2 || len(x.SetupCallbacks[0]) != 1 || len(x.EventCallbacks[0]) != 0 {
				t.Fatalf("setup-only chain not preserved by actual build %+v", x)
			}
			data, e := os.ReadFile("testdata/source-initial-17c6d5ba.json")
			if e != nil {
				t.Fatal(e)
			}
			var rows []struct {
				Name  string `json:"name"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if e = json.Unmarshal(data, &rows); e != nil {
				t.Fatal(e)
			}
			message := ""
			for _, row := range rows {
				if row.Name == "setup_only_empty_events" && row.Error != nil {
					message = row.Error.Message
					break
				}
			}
			if message == "" {
				t.Fatal("missing real pinned empty-filter error")
			}
			opened := 0
			epochs := []uint64{}
			_, e = openRuntime(context.Background(), RuntimeOptions{SourceCheckpoints: true, Historical: Options{Build: b}, BuildID: "source-empty-filter-boundary", OpenID: "source-empty-filter", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{}}, func(context.Context, store.IndependentStorageOptions, store.ManagedLiveRuntimeOptions) (*store.ManagedLiveDomains, error) {
				opened++
				return nil, fmt.Errorf("must not reach DDL")
			})
			if e == nil || !strings.Contains(e.Error(), message) || opened != 0 || called != 0 {
				t.Fatalf("source error mismatch err=%v opened=%d callbacks=%d", e, opened, called)
			}
		})
	}
}
