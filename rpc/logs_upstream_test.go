package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestUpstreamRPCLogs(t *testing.T) {
	const hash = "0x1111111111111111111111111111111111111111111111111111111111111111"
	const address = "0x2222222222222222222222222222222222222222"
	log := map[string]any{"blockNumber": "0x1", "logIndex": "0x0", "blockHash": hash, "address": address, "topics": []any{}, "data": "0x", "transactionHash": hash, "transactionIndex": "0x0"}
	clone := func(m map[string]any, changes map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range m {
			out[k] = v
		}
		for k, v := range changes {
			out[k] = v
		}
		return out
	}
	serverFor := func(t *testing.T, answer func(map[string]any) []map[string]any) *Client {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var q struct {
				ID     any
				Method string
				Params []map[string]any
			}
			if e := json.NewDecoder(r.Body).Decode(&q); e != nil {
				t.Error(e)
				return
			}
			if q.Method != "eth_getLogs" {
				t.Error(q.Method)
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": answer(q.Params[0])})
		}))
		t.Cleanup(server.Close)
		c := New(server.URL)
		c.Retries = 0
		c.RequestsPerSecond = 0
		return c
	}
	t.Run("eth_getLogs chunks address arrays and merges responses in order", func(t *testing.T) {
		addresses := []any{}
		for i := 0; i < 51; i++ {
			addresses = append(addresses, fmt.Sprintf("0x%040x", i))
		}
		first := clone(log, map[string]any{"address": addresses[0], "blockNumber": "0x2"})
		second := clone(log, map[string]any{"address": addresses[50], "logIndex": "0x1"})
		requests := []map[string]any{}
		c := serverFor(t, func(query map[string]any) []map[string]any {
			requests = append(requests, query)
			if query["address"].([]any)[0] == addresses[50] {
				return []map[string]any{second}
			}
			return []map[string]any{first}
		})
		params := map[string]any{"address": addresses}
		result, e := c.GetLogs(context.Background(), params)
		if e != nil {
			t.Fatal(e)
		}
		want := []map[string]any{clone(second, map[string]any{"removed": false}), clone(first, map[string]any{"removed": false})}
		if !reflect.DeepEqual(result, want) {
			t.Fatal("original full result assertion", result, want)
		}
		if len(requests) != 2 {
			t.Fatal(requests)
		}
		if !reflect.DeepEqual([]any{requests[0]["address"], requests[1]["address"]}, []any{addresses[:50], addresses[50:]}) {
			t.Fatal("original request chunk assertion", requests)
		}
		if !reflect.DeepEqual(params["address"], addresses) {
			t.Fatal("original params immutability assertion")
		}
	})
	t.Run("eth_getLogs sorts logs by block number and log index", func(t *testing.T) {
		logs := []map[string]any{clone(log, map[string]any{"blockNumber": "0x2", "logIndex": "0x0"}), clone(log, map[string]any{"blockNumber": "0x1", "logIndex": "0x1"}), clone(log, map[string]any{"blockNumber": "0x1", "logIndex": "0x0"})}
		c := serverFor(t, func(map[string]any) []map[string]any { return logs })
		result, e := c.GetLogs(context.Background(), map[string]any{"fromBlock": "0x1", "toBlock": "0x2"})
		if e != nil {
			t.Fatal(e)
		}
		pairs := [][2]string{}
		for _, row := range result {
			pairs = append(pairs, [2]string{row["blockNumber"].(string), row["logIndex"].(string)})
		}
		if !reflect.DeepEqual(pairs, [][2]string{{"0x1", "0x0"}, {"0x1", "0x1"}, {"0x2", "0x0"}}) {
			t.Fatal("original full order assertion", pairs)
		}
	})
	t.Run("standardizeTransactions() sorts transactions by transaction index", func(t *testing.T) {
		tx := map[string]any{"blockHash": hash, "blockNumber": "0x1", "from": address, "hash": hash, "to": address, "transactionIndex": "0x0"}
		result, e := StandardizeTransactions([]map[string]any{clone(tx, map[string]any{"transactionIndex": "0x2"}), clone(tx, map[string]any{"transactionIndex": "0x0"}), clone(tx, map[string]any{"transactionIndex": "0x1"})}, "eth_getBlockByNumber", []any{"0x1", true})
		if e != nil {
			t.Fatal(e)
		}
		indices := []string{}
		for _, row := range result {
			indices = append(indices, row["transactionIndex"].(string))
		}
		if !reflect.DeepEqual(indices, []string{"0x0", "0x1", "0x2"}) {
			t.Fatal("original exact order assertion", indices)
		}
	})
	t.Run("eth_getLogs skips empty address arrays", func(t *testing.T) {
		calls := 0
		c := serverFor(t, func(map[string]any) []map[string]any { calls++; return nil })
		result, e := c.GetLogs(context.Background(), map[string]any{"address": []any{}})
		if e != nil || !reflect.DeepEqual(result, []map[string]any{}) {
			t.Fatal("original empty result", result, e)
		}
		if calls != 0 {
			t.Fatal("original not-called assertion", calls)
		}
	})
}
