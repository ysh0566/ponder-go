package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestUpstreamTraceActions(t *testing.T) {
	const hash = "0x1111111111111111111111111111111111111111111111111111111111111111"
	const address = "0x2222222222222222222222222222222222222222"
	frame := func(overrides map[string]any) map[string]any {
		f := map[string]any{"type": "CALL", "from": address, "to": address, "gas": "0x1", "gasUsed": "0x1", "input": "0x"}
		for k, v := range overrides {
			f[k] = v
		}
		return f
	}
	cases := []struct {
		name  string
		input []map[string]any
		paths [][]int
	}{
		{"debug trace actions rebuild traceAddress from the call tree", []map[string]any{{"txHash": hash, "result": frame(map[string]any{"calls": []any{frame(nil), frame(map[string]any{"calls": []any{frame(nil)}})}})}}, [][]int{{}, {0}, {1}, {1, 0}}},
		{"debug trace actions exclude reverted traces and their children", []map[string]any{{"txHash": hash, "result": frame(map[string]any{"calls": []any{frame(map[string]any{"error": "execution reverted", "calls": []any{frame(nil)}}), frame(map[string]any{"calls": []any{frame(map[string]any{"error": "out of gas", "revertReason": "reason"}), frame(nil)}})}})}, {"txHash": hash, "result": frame(map[string]any{"error": "execution reverted", "calls": []any{frame(nil)}})}}, [][]int{{}, {1}, {1, 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					ID     any
					Method string
					Params []json.RawMessage
				}
				if e := json.NewDecoder(r.Body).Decode(&q); e != nil {
					t.Error(e)
					return
				}
				calls++
				if q.Method != "debug_traceBlockByNumber" || string(q.Params[0]) != `"0x1"` || string(q.Params[1]) != `{"tracer":"callTracer"}` {
					t.Error("source request", q)
				}
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": c.input})
			}))
			defer server.Close()
			client := New(server.URL)
			client.Retries = 0
			client.RequestsPerSecond = 0
			traces, e := client.TraceBlockByNumber(context.Background(), "0x1")
			if e != nil {
				t.Fatal(e)
			}
			paths := [][]int{}
			for _, trace := range traces {
				paths = append(paths, trace.Trace["traceAddress"].([]int))
			}
			if !reflect.DeepEqual(paths, c.paths) {
				t.Fatal("original full path assertion", paths, c.paths)
			}
			if calls != 1 {
				t.Fatal("source mocked request invoked once", calls)
			}
		})
	}
}
