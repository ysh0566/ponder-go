package config_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/rpc"
)

func TestNativeSourceHeadReads(t *testing.T) {
	const factory = `{"event":{"type":"event","name":"Made","inputs":[{"type":"address","name":"child","indexed":true}]},"parameter":"child","endBlock":"latest"}`
	for _, tc := range []struct {
		name, sources, callback string
		wantReads               int
		wantError               string
	}{
		{"canceled before static build", `"blocks":{"H":{"chain":"two"}}`, "H:block", 0, "context canceled"},
		{"static", `"blocks":{"H":{"chain":{"one":{},"two":{}},"startBlock":1}}`, "H:block", 0, ""},
		{"source bounds share head", `"blocks":{"H":{"chain":{"one":{"startBlock":"latest","endBlock":"latest"},"two":{"startBlock":3}}},"G":{"chain":"one","endBlock":"latest"}}`, "H:block", 1, ""},
		{"factory bound", `"accounts":{"H":{"chain":"one","endBlock":10,"address":` + factory + `}}`, "H:transaction:to", 1, ""},
		{"invalid static range", `"blocks":{"H":{"chain":"two","startBlock":5,"endBlock":1}}`, "H:block", 0, "after end block"},
		{"invalid resolved factory range", `"accounts":{"H":{"chain":"one","endBlock":3,"address":` + factory + `}}`, "H:transaction:to", 1, "before end block of factory"},
		{"unavailable dynamic head", `"blocks":{"H":{"chain":"two","endBlock":"latest"}}`, "H:block", 0, `Unable to fetch "latest" block for chain 'two'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := config.ParseSourceConfiguration([]byte(`{"ordering":"experimental_isolated","chains":{"one":{"id":101},"two":{"id":202}},` + tc.sources + `}`))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			opts := config.SourceBuildOptions{RPCTransports: map[string]rpc.TransportFunc{
				"one": func(_ context.Context, method string, params any) (json.RawMessage, error) {
					reads++
					if method != "eth_getBlockByNumber" || !reflect.DeepEqual(params, []any{"latest", false}) {
						return nil, fmt.Errorf("unexpected head request %s %v", method, params)
					}
					return json.RawMessage(`{"number":"0xa","timestamp":"0xf","hash":"0x01","parentHash":"0x02","logsBloom":"0x00"}`), nil
				},
				"two": func(_ context.Context, _ string, _ any) (json.RawMessage, error) {
					if tc.name != "unavailable dynamic head" {
						t.Error("queried static chain")
					}
					cancel()
					return nil, context.Canceled
				},
			}}
			if tc.name == "canceled before static build" {
				cancel()
			}
			configs, err := config.BuildNativeSourceConfigs(ctx, input, opts, []config.SourceIndexingFunction{{Name: tc.callback, Fn: func() {}}})
			if reads != tc.wantReads {
				t.Fatalf("read latest %d times, want %d", reads, tc.wantReads)
			}
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("wanted %q, got %v", tc.wantError, err)
				}
				if ctx.Err() != nil && !errors.Is(err, context.Canceled) {
					t.Fatalf("head read lost cancellation: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "static":
				if len(configs) != 2 || configs[1].Blocks[0].StartBlock != 1 {
					t.Fatal(configs)
				}
			case "source bounds share head":
				block := configs[0].Blocks[0]
				if block.StartBlock != 10 || block.EndBlock == nil || *block.EndBlock != 10 || configs[1].Blocks[0].StartBlock != 3 {
					t.Fatal(configs)
				}
			case "factory bound":
				f := configs[0].Accounts[0].Factory
				if f == nil || f.ToBlock == nil || *f.ToBlock != 10 {
					t.Fatal(f)
				}
			}
		})
	}
}
