package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"strings"
	"testing"
)

const actionHash = "0x1111111111111111111111111111111111111111111111111111111111111111"
const actionAddress = "0x2222222222222222222222222222222222222222"

type actionMock struct {
	calls    []ActionRequest
	contexts []context.Context
	answer   func(ActionRequest) (any, error)
}

func (m *actionMock) Call(ctx context.Context, method string, params any, out any) error {
	request := ActionRequest{method, params.([]any)}
	m.calls = append(m.calls, request)
	m.contexts = append(m.contexts, ctx)
	value, err := m.answer(request)
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func actionLog(block string) map[string]any {
	return map[string]any{"blockNumber": block, "logIndex": "0x0", "blockHash": actionHash, "address": actionAddress, "topics": []any{}, "data": "0x", "transactionHash": actionHash, "transactionIndex": "0x0"}
}
func actionReceipt() map[string]any {
	return map[string]any{"blockHash": actionHash, "blockNumber": "0x1", "contractAddress": nil, "cumulativeGasUsed": "0x1", "effectiveGasPrice": "0x1", "from": actionAddress, "gasUsed": "0x1", "logs": []any{}, "logsBloom": "0x" + strings.Repeat("0", 512), "status": "0x1", "to": actionAddress, "transactionHash": actionHash, "transactionIndex": "0x0", "type": "0x0"}
}
func actionRanges(calls []ActionRequest) [][2]uint64 {
	out := [][2]uint64{}
	for _, call := range calls {
		query := call.Params[0].(map[string]any)
		from, _ := rpcQuantity(query["fromBlock"])
		to, _ := rpcQuantity(query["toBlock"])
		out = append(out, [2]uint64{from.Uint64(), to.Uint64()})
	}
	return out
}
func drainPages(sequence iter.Seq2[LogsPage, error]) ([]LogsPage, error) {
	pages := []LogsPage{}
	for page, err := range sequence {
		if err != nil {
			return pages, err
		}
		pages = append(pages, page)
	}
	return pages, nil
}
func pageRanges(pages []LogsPage) [][2]uint64 {
	out := [][2]uint64{}
	for _, p := range pages {
		out = append(out, [2]uint64{p.FromBlock, p.ToBlock})
	}
	return out
}

func TestUpstreamRPCPagination(t *testing.T) {
	t.Run("eth_getLogsWithPagination yields pages lazily and grows the range", func(t *testing.T) {
		mock := &actionMock{answer: func(r ActionRequest) (any, error) {
			return []map[string]any{actionLog(r.Params[0].(map[string]any)["fromBlock"].(string))}, nil
		}}
		actions := NewActions(mock)
		params := map[string]any{"address": actionAddress, "topics": []any{actionHash}, "fromBlock": "0x0", "toBlock": fmt.Sprintf("0x%x", 1100)}
		type key struct{}
		ctx := context.WithValue(context.Background(), key{}, map[string]any{"retryNullBlockRequest": false})
		generator := actions.GetLogsWithPagination(ctx, params, nil)
		if len(mock.calls) != 0 {
			t.Fatal("original lazy creation assertion", mock.calls)
		}
		next, stop := iter.Pull2(generator)
		defer stop()
		first, err, ok := next()
		if !ok || err != nil || first.FromBlock != 0 || first.ToBlock != 499 || len(first.Logs) != 1 || first.Logs[0]["blockNumber"] != "0x0" || first.Logs[0]["removed"] != false {
			t.Fatal("original next-page partial match", first, err, ok)
		}
		if !reflect.DeepEqual(actionRanges(mock.calls), [][2]uint64{{0, 499}}) {
			t.Fatal("original first request range", mock.calls)
		}
		pages := []LogsPage{}
		for {
			page, err, ok := next()
			if !ok {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			pages = append(pages, page)
		}
		if !reflect.DeepEqual(pageRanges(pages), [][2]uint64{{500, 1024}, {1025, 1100}}) {
			t.Fatal("original remaining page ranges", pages)
		}
		blocks := []any{}
		for _, p := range pages {
			blocks = append(blocks, p.Logs[0]["blockNumber"])
		}
		if !reflect.DeepEqual(blocks, []any{fmt.Sprintf("0x%x", 500), fmt.Sprintf("0x%x", 1025)}) {
			t.Fatal("original remaining log blocks", blocks)
		}
		if !reflect.DeepEqual(actionRanges(mock.calls), [][2]uint64{{0, 499}, {500, 1024}, {1025, 1100}}) {
			t.Fatal("original all request ranges", mock.calls)
		}
		for i, r := range mock.calls {
			query := r.Params[0].(map[string]any)
			if query["address"] != actionAddress || !reflect.DeepEqual(query["topics"], []any{actionHash}) {
				t.Fatal("original address/topics partial match", query)
			}
			if mock.contexts[i] != ctx {
				t.Fatal("original request context identity")
			}
		}
		if params["fromBlock"] != "0x0" {
			t.Fatal("original input immutability", params)
		}
	})
	t.Run("eth_getLogsWithPagination stops fetching when the consumer stops", func(t *testing.T) {
		mock := &actionMock{answer: func(ActionRequest) (any, error) { return []map[string]any{}, nil }}
		for _, err := range NewActions(mock).GetLogsWithPagination(context.Background(), map[string]any{"fromBlock": "0x0", "toBlock": "0xffff"}, nil) {
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if len(mock.calls) != 1 {
			t.Fatal("original single request assertion", mock.calls)
		}
	})
	t.Run("eth_getLogsWithPagination retries suggested ranges and keeps the limit per RPC", func(t *testing.T) {
		first := true
		mock := &actionMock{answer: func(r ActionRequest) (any, error) {
			if first {
				first = false
				return nil, &Error{Message: "Max range: 20"}
			}
			return []map[string]any{actionLog(r.Params[0].(map[string]any)["fromBlock"].(string))}, nil
		}}
		actions := NewActions(mock)
		ctx := context.Background()
		if _, err := drainPages(actions.GetLogsWithPagination(ctx, map[string]any{"fromBlock": "0x0", "toBlock": fmt.Sprintf("0x%x", 44)}, nil)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actionRanges(mock.calls), [][2]uint64{{0, 44}, {0, 19}, {20, 39}, {40, 44}}) {
			t.Fatal("original suggested retry ranges", mock.calls)
		}
		other := &actionMock{answer: func(ActionRequest) (any, error) { return []map[string]any{}, nil }}
		if _, err := drainPages(NewActions(other).GetLogsWithPagination(ctx, map[string]any{"fromBlock": fmt.Sprintf("0x%x", 45), "toBlock": fmt.Sprintf("0x%x", 89)}, nil)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actionRanges(other.calls), [][2]uint64{{45, 89}}) {
			t.Fatal("original RPC state isolation", other.calls)
		}
		mock.calls = nil
		mock.contexts = nil
		if _, err := drainPages(actions.GetLogsWithPagination(ctx, map[string]any{"fromBlock": fmt.Sprintf("0x%x", 45), "toBlock": fmt.Sprintf("0x%x", 89)}, nil)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actionRanges(mock.calls), [][2]uint64{{45, 64}, {65, 84}, {85, 89}}) {
			t.Fatal("original learned RPC limit", mock.calls)
		}
	})
	t.Run("eth_getLogsWithPagination retries unfinished blocks and grows inferred ranges", func(t *testing.T) {
		step := 0
		mock := &actionMock{answer: func(r ActionRequest) (any, error) {
			step++
			if step == 1 {
				return []map[string]any{}, nil
			}
			if step == 2 {
				return nil, &Error{Message: "query exceeds max results"}
			}
			return []map[string]any{actionLog(r.Params[0].(map[string]any)["fromBlock"].(string))}, nil
		}}
		pages, err := drainPages(NewActions(mock).GetLogsWithPagination(context.Background(), map[string]any{"fromBlock": "0x0", "toBlock": fmt.Sprintf("0x%x", 1200)}, nil))
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) == 0 || !reflect.DeepEqual(pages[0].Logs, []map[string]any{}) {
			t.Fatal("original first empty page", pages)
		}
		blocks := []any{}
		for _, p := range pages[1:] {
			blocks = append(blocks, p.Logs[0]["blockNumber"])
		}
		if !reflect.DeepEqual(blocks, []any{fmt.Sprintf("0x%x", 500), fmt.Sprintf("0x%x", 763), fmt.Sprintf("0x%x", 1039)}) {
			t.Fatal("original inferred log blocks", blocks)
		}
		if !reflect.DeepEqual(actionRanges(mock.calls), [][2]uint64{{0, 499}, {500, 1024}, {500, 762}, {763, 1038}, {1039, 1200}}) {
			t.Fatal("original inferred retry and growth ranges", mock.calls)
		}
	})
	t.Run("eth_getLogsWithPagination propagates errors when retries are exhausted", func(t *testing.T) {
		failure := &Error{Message: "query exceeds max results"}
		mock := &actionMock{answer: func(ActionRequest) (any, error) { return nil, failure }}
		_, err := drainPages(NewActions(mock).GetLogsWithPagination(context.Background(), map[string]any{"fromBlock": "0x0", "toBlock": "0x1"}, nil))
		if err != failure {
			t.Fatal("original error object identity", err)
		}
		if !reflect.DeepEqual(actionRanges(mock.calls), [][2]uint64{{0, 1}, {0, 0}}) {
			t.Fatal("original exhausted retry ranges", mock.calls)
		}
	})
	t.Run("eth_getLogsWithPagination uses fixed ranges and disables range retries", func(t *testing.T) {
		var failure error
		mock := &actionMock{answer: func(r ActionRequest) (any, error) {
			if failure != nil {
				return nil, failure
			}
			return []map[string]any{actionLog(r.Params[0].(map[string]any)["fromBlock"].(string))}, nil
		}}
		actions := NewActions(mock)
		size := uint64(2)
		options := &ActionOptions{EthGetLogsBlockRange: &size}
		params := map[string]any{"fromBlock": "0x0", "toBlock": "0x4"}
		if _, err := drainPages(actions.GetLogsWithPagination(context.Background(), params, options)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actionRanges(mock.calls), [][2]uint64{{0, 1}, {2, 3}, {4, 4}}) {
			t.Fatal("original fixed ranges", mock.calls)
		}
		mock.calls = nil
		failure = &Error{Message: "Max range: 1"}
		if _, err := drainPages(actions.GetLogsWithPagination(context.Background(), params, options)); err != failure {
			t.Fatal("original fixed range error identity", err)
		}
		if len(mock.calls) != 1 {
			t.Fatal("original fixed range retries disabled", mock.calls)
		}
	})
}

func TestUpstreamRPCReceipts(t *testing.T) {
	t.Run("eth_getTransactionReceipts falls back to eth_getTransactionReceipt per RPC", func(t *testing.T) {
		mock := &actionMock{answer: func(r ActionRequest) (any, error) {
			if r.Method == "eth_getBlockReceipts" {
				return nil, fmt.Errorf("method not supported")
			}
			return actionReceipt(), nil
		}}
		actions := NewActions(mock)
		ctx := context.Background()
		hashes := []string{actionHash}
		responses, err := actions.GetTransactionReceipts(ctx, actionHash, hashes, nil)
		if err != nil || len(responses) != 1 || !reflect.DeepEqual(responses[0].Request, ActionRequest{"eth_getTransactionReceipt", []any{actionHash}}) {
			t.Fatal("original fallback request partial match", responses, err)
		}
		if _, err = actions.GetTransactionReceipts(ctx, actionHash, hashes, nil); err != nil {
			t.Fatal(err)
		}
		methods := []string{}
		for _, r := range mock.calls {
			methods = append(methods, r.Method)
		}
		if !reflect.DeepEqual(methods, []string{"eth_getBlockReceipts", "eth_getTransactionReceipt", "eth_getTransactionReceipt"}) {
			t.Fatal("original remembered fallback request methods", methods)
		}
		other := &actionMock{answer: func(ActionRequest) (any, error) { return []map[string]any{actionReceipt()}, nil }}
		responses, err = NewActions(other).GetTransactionReceipts(ctx, actionHash, hashes, nil)
		if err != nil || len(responses) != 1 || !reflect.DeepEqual(responses[0].Request, ActionRequest{"eth_getBlockReceipts", []any{actionHash}}) {
			t.Fatal("original other RPC block request partial match", responses, err)
		}
		if len(other.calls) != 1 {
			t.Fatal("original other RPC single request", other.calls)
		}
	})
	t.Run("eth_getTransactionReceipts sorts receipts by transaction index", func(t *testing.T) {
		otherHash := "0x3333333333333333333333333333333333333333333333333333333333333333"
		first, second := actionReceipt(), actionReceipt()
		first["transactionHash"], first["transactionIndex"] = otherHash, "0x1"
		second["transactionHash"], second["transactionIndex"] = actionHash, "0x0"
		receipts := []map[string]any{first, second}
		hashes := []string{otherHash, actionHash}
		ctx := context.Background()
		mock := &actionMock{answer: func(ActionRequest) (any, error) { return receipts, nil }}
		responses, err := NewActions(mock).GetTransactionReceipts(ctx, actionHash, hashes, nil)
		if err != nil {
			t.Fatal(err)
		}
		indexes := []any{}
		for _, r := range responses[0].Receipts {
			indexes = append(indexes, r["transactionIndex"])
		}
		if !reflect.DeepEqual(indexes, []any{"0x0", "0x1"}) {
			t.Fatal("original sorted block receipts", indexes)
		}
		fallback := &actionMock{answer: func(r ActionRequest) (any, error) {
			if r.Method == "eth_getBlockReceipts" {
				return nil, fmt.Errorf("method not supported")
			}
			for _, receipt := range receipts {
				if receipt["transactionHash"] == r.Params[0] {
					return receipt, nil
				}
			}
			return nil, nil
		}}
		responses, err = NewActions(fallback).GetTransactionReceipts(ctx, actionHash, hashes, nil)
		if err != nil {
			t.Fatal(err)
		}
		indexes = nil
		for _, response := range responses {
			for _, r := range response.Receipts {
				indexes = append(indexes, r["transactionIndex"])
			}
		}
		if !reflect.DeepEqual(indexes, []any{"0x0", "0x1"}) {
			t.Fatal("original sorted flattened fallback receipts", indexes)
		}
	})
}
func TestUpstreamRPCLogValidation(t *testing.T) {
	logsRequest := ActionRequest{"eth_getLogs", []any{map[string]any{"blockHash": actionHash}}}
	blockRequest := ActionRequest{"eth_getBlockByHash", []any{actionHash, true}}
	block := func() map[string]any {
		return map[string]any{"hash": actionHash, "number": "0x1", "transactions": []map[string]any{}, "logsBloom": "0x" + strings.Repeat("0", 511) + "1"}
	}
	t.Run("validateLogsAndBlock allows non-empty logsBloom with no logs", func(t *testing.T) {
		if err := ValidateLogsAndBlock([]map[string]any{}, block(), logsRequest, blockRequest); err != nil {
			t.Fatal("original not-to-throw assertion", err)
		}
	})
	t.Run("validateLogsAndBlock rejects mismatched block hashes", func(t *testing.T) {
		log := map[string]any{"address": "0x" + strings.Repeat("1", 40), "blockHash": "0x" + strings.Repeat("2", 64), "blockNumber": "0x1", "logIndex": "0x0", "data": "0x", "topics": []any{}, "transactionHash": actionHash, "transactionIndex": "0x0", "removed": false}
		err := ValidateLogsAndBlock([]map[string]any{log}, block(), logsRequest, blockRequest)
		if err == nil || !strings.Contains(err.Error(), "has a 'log.blockHash'") {
			t.Fatal("original to-throw substring assertion", err)
		}
	})
}
