package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// These cases come from actual pinned source functions, not original test credit.
func TestRPCActionDifferential(t *testing.T) {
	data, err := os.ReadFile("../testdata/rpc/actions-differential.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Operation string
			Input           map[string]any
			Result          any
			Error           *struct {
				Name, Message string
				Meta          []string
			}
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var result any
			var err error
			switch c.Operation {
			case "retry":
				result, err = GetLogsRetryHelper(c.Input["query"].(map[string]any), c.Input["error"])
			case "receipts":
				rows := []map[string]any{}
				for _, row := range c.Input["receipts"].([]any) {
					rows = append(rows, row.(map[string]any))
				}
				result, err = StandardizeTransactionReceipts(rows, fixtureRequest(c.Input["request"]))
			case "validate":
				rows := []map[string]any{}
				for _, row := range c.Input["logs"].([]any) {
					rows = append(rows, row.(map[string]any))
				}
				err = ValidateLogsAndBlock(rows, c.Input["block"].(map[string]any), fixtureRequest(c.Input["logsRequest"]), fixtureRequest(c.Input["blockRequest"]))
			default:
				t.Fatal("unknown fixture operation", c.Operation)
			}
			if c.Error != nil {
				var provider *ProviderError
				if !errors.As(err, &provider) || c.Error.Name != "RpcProviderError" || provider.Message != c.Error.Message || !reflect.DeepEqual(provider.Meta, c.Error.Meta) {
					t.Fatalf("source error mismatch: expected=%+v got=%#v", c.Error, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var normalized any
			if err := json.Unmarshal(encoded, &normalized); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(normalized, c.Result) {
				t.Fatalf("source result mismatch: expected=%v got=%v", c.Result, normalized)
			}
		})
	}
}
func fixtureRequest(input any) ActionRequest {
	r := input.(map[string]any)
	return ActionRequest{r["method"].(string), r["params"].([]any)}
}

type actionTestLogger struct{ entries []map[string]any }

func (l *actionTestLogger) Debug(fields map[string]any) { l.entries = append(l.entries, fields) }
func (l *actionTestLogger) Warn(fields map[string]any)  { l.entries = append(l.entries, fields) }

func TestRPCActionBoundaries(t *testing.T) {
	t.Run("null_receipt_retains_not_found_class", func(t *testing.T) {
		mock := &actionMock{answer: func(ActionRequest) (any, error) { return nil, nil }}
		_, err := NewActions(mock).GetTransactionReceipt(context.Background(), actionHash)
		var missing *TransactionReceiptNotFoundError
		expected := fmt.Sprintf("Transaction receipt with hash %q could not be found. The Transaction may not be processed on a block yet.", actionHash)
		if !errors.As(err, &missing) || missing.Hash != actionHash || missing.Error() != expected {
			t.Fatal("source not-found error", err)
		}
	})
	t.Run("invalid_block_receipts_fallback_warns_once_with_provenance", func(t *testing.T) {
		mock := &actionMock{answer: func(r ActionRequest) (any, error) {
			if r.Method == "eth_getBlockReceipts" {
				return nil, nil
			}
			return actionReceipt(), nil
		}}
		actions := NewActions(mock)
		logger := &actionTestLogger{}
		for i := 0; i < 2; i++ {
			responses, err := actions.GetTransactionReceipts(context.Background(), actionHash, []string{actionHash, actionHash}, &ActionOptions{Logger: logger})
			if err != nil || len(responses) != 1 || responses[0].Request.Method != "eth_getTransactionReceipt" || responses[0].Receipts[0]["transactionHash"] != actionHash {
				t.Fatal(responses, err)
			}
		}
		if len(mock.calls) != 3 || len(logger.entries) != 1 || !strings.Contains(logger.entries[0]["error"].(error).Error(), "invalid empty") {
			t.Fatal(mock.calls, logger.entries)
		}
	})
	t.Run("standardization_failure_is_remembered_per_rpc", func(t *testing.T) {
		mock := &actionMock{answer: func(r ActionRequest) (any, error) {
			if r.Method == "eth_getBlockReceipts" {
				row := actionReceipt()
				delete(row, "status")
				return []map[string]any{row}, nil
			}
			return actionReceipt(), nil
		}}
		actions := NewActions(mock)
		for i := 0; i < 2; i++ {
			if _, err := actions.GetTransactionReceipts(context.Background(), actionHash, []string{actionHash}, nil); err != nil {
				t.Fatal(err)
			}
		}
		if len(mock.calls) != 3 || mock.calls[2].Method != "eth_getTransactionReceipt" {
			t.Fatal(mock.calls)
		}
	})
	t.Run("no_hashes_or_cancelled_consumer_perform_no_requests", func(t *testing.T) {
		mock := &actionMock{answer: func(ActionRequest) (any, error) { t.Fatal("unexpected request"); return nil, nil }}
		actions := NewActions(mock)
		receipts, err := actions.GetTransactionReceipts(context.Background(), actionHash, nil, nil)
		if err != nil || !reflect.DeepEqual(receipts, []ReceiptResponse{}) {
			t.Fatal(receipts, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = drainPages(actions.GetLogsWithPagination(ctx, map[string]any{"fromBlock": "0x0", "toBlock": "0x4"}, nil))
		if err != context.Canceled {
			t.Fatal("cancellation identity", err)
		}
	})
	t.Run("learned_limits_shared_across_scoped_clients_without_immutable_cache", func(t *testing.T) {
		calls := [][2]uint64{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var q struct {
				ID     any              `json:"id"`
				Params []map[string]any `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
				t.Error(err)
				return
			}
			from, _ := rpcQuantity(q.Params[0]["fromBlock"])
			to, _ := rpcQuantity(q.Params[0]["toBlock"])
			calls = append(calls, [2]uint64{from.Uint64(), to.Uint64()})
			response := map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": []any{}}
			if len(calls) == 1 {
				delete(response, "result")
				response["error"] = map[string]any{"code": -32000, "message": "Max range: 2"}
			}
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()
		client := New(server.URL)
		client.Retries = 0
		client.RequestsPerSecond = 0
		cache := &deniedImmutableCache{}
		first := client.WithCache(cache)
		if _, err := drainPages(first.GetLogsWithPagination(context.Background(), map[string]any{"fromBlock": "0x0", "toBlock": "0x4"}, nil)); err != nil {
			t.Fatal(err)
		}
		second := client.WithCache(cache)
		if _, err := drainPages(second.GetLogsWithPagination(context.Background(), map[string]any{"fromBlock": "0x5", "toBlock": "0x8"}, nil)); err != nil {
			t.Fatal(err)
		}
		expected := [][2]uint64{{0, 4}, {0, 1}, {2, 3}, {4, 4}, {5, 6}, {7, 8}}
		if !reflect.DeepEqual(calls, expected) || cache.get != 0 || cache.put != 0 || first.Actions() != second.Actions() {
			t.Fatal(calls, cache)
		}
	})
	t.Run("concurrent_consumers_share_state_safely", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var q struct {
				ID any `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
				t.Error(err)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": []any{}})
		}))
		defer server.Close()
		client := New(server.URL)
		client.Retries = 0
		client.RequestsPerSecond = 0
		var group sync.WaitGroup
		for i := 0; i < 4; i++ {
			group.Add(1)
			go func() {
				defer group.Done()
				pages, err := drainPages(client.WithCache(nil).GetLogsWithPagination(context.Background(), map[string]any{"fromBlock": "0x0", "toBlock": "0x1000"}, nil))
				if err != nil {
					t.Error(err)
					return
				}
				cursor := uint64(0)
				for _, page := range pages {
					if page.FromBlock != cursor || page.ToBlock < page.FromBlock {
						t.Error("missing/duplicate page", page)
					}
					cursor = page.ToBlock + 1
				}
				if cursor != 4097 {
					t.Error("incomplete pagination", cursor)
				}
			}()
		}
		group.Wait()
	})
}
