package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/ysh0566/ponder-go/rpc"
)

func TestSourceConfigTransport(t *testing.T) {
	cfg, err := ParseSourceConfiguration([]byte(`{"chains":{"custom":{"id":987654321},"http":{"id":1234,"rpc":"http://127.0.0.1:8545"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	fn := func(context.Context, string, any) (json.RawMessage, error) { return json.RawMessage(`"0x1"`), nil }
	for name, transports := range map[string]map[string]rpc.TransportFunc{
		"missing": nil, "typo": {"wrong": fn}, "nil": {"custom": nil}, "valid": {"custom": fn},
	} {
		t.Run(name, func(t *testing.T) {
			built, err := BuildSourceConfig(cfg, SourceBuildOptions{RPCTransports: transports})
			if name != "valid" {
				if err == nil {
					t.Fatal("unexpected transport admission")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			clients := built.RPCClients()
			if len(clients) != 2 || !clients[0].HasCustomTransport() || clients[1].HasCustomTransport() || len(clients[0].URLs) != 0 {
				t.Fatal("incorrect chain transports", clients)
			}
			if clients[0].ChainName != "custom" || clients[1].ChainName != "http" {
				t.Fatal("compiled transports lost their chain names")
			}
			want := []any{map[string]any{"$rpc": map[string]any{"hostnames": []string{"custom_transport"}}}, map[string]any{"$rpc": map[string]any{"hostnames": []string{"127.0.0.1"}}}}
			if !reflect.DeepEqual(built.Value()["rpcs"], want) {
				t.Fatal("RPC metadata", built.Value()["rpcs"])
			}
			if _, err := json.Marshal(built.Value()["rpcs"]); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSourceTransportOverridesURL(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct{ ID uint64 }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"http"}`, request.ID)
	}))
	defer server.Close()
	raw := fmt.Sprintf(`{"chains":{"one":{"id":1234,"rpc":%q}}}`, server.URL)
	cfg, err := ParseSourceConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	denied := &rpc.Error{Code: -32601, Message: "method unavailable"}
	custom, err := BuildSourceConfig(cfg, SourceBuildOptions{RPCTransports: map[string]rpc.TransportFunc{
		"one": func(context.Context, string, any) (json.RawMessage, error) { return nil, denied },
	}})
	if err != nil {
		t.Fatal(err)
	}
	var out string
	if err := custom.RPCClients()[0].Call(t.Context(), "test", nil, &out); !errors.Is(err, denied) || calls.Load() != 0 {
		t.Fatalf("custom transport error=%v URL calls=%d", err, calls.Load())
	}
	ordinary, err := BuildSourceConfig(cfg, SourceBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ordinary.RPCClients()[0].Call(t.Context(), "test", nil, &out); err != nil || out != "http" || calls.Load() != 1 {
		t.Fatalf("original URL result=%q error=%v calls=%d", out, err, calls.Load())
	}
}
