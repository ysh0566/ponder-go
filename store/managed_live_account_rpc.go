package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/interval"
	"github.com/ysh0566/ponder-go/rpc"
)

func (reader LogScanRPC) scanBlockRanges(ctx context.Context, r logScanRange, callbacks []filter.Object, children logFactoryChildren, get ManagedLiveHeaderReader) error {
	var spans []interval.Interval
	for _, cb := range callbacks {
		f := cb["filter"].(filter.Object)
		start, end, err := logSourceBounds(f)
		if err != nil {
			return err
		}
		from, to := max(start, r.from), min(end, r.to)
		if from <= to {
			spans = append(spans, interval.Interval{int64(from), int64(to)})
		}
	}
	for _, span := range interval.Union(spans) {
		for height := uint64(span[0]); height <= uint64(span[1]); height++ {
			head, err := get(r.chain, height)
			if err != nil {
				return err
			}
			_, cp, err := normalizeLiveHead(head)
			if err != nil || cp.BlockNumber != height || cp.ChainID != r.chain {
				return ErrManagedLiveBatchConflict
			}
			var transfers, calls bool
			for _, cb := range callbacks {
				f := cb["filter"].(filter.Object)
				if filter.IsBlockInFilter(f, float64(height)) {
					transfers = transfers || f["type"] == "transfer"
					calls = calls || f["type"] == "trace"
				}
			}
			data, err := reader.Actions.GetAccountScanBlock(ctx, head.Hash, transfers || calls, &reader.Options)
			if err != nil {
				return err
			}
			if calls {
				if err := validateCallScanData(data); err != nil {
					return err
				}
			}
			n, ok := logOnlyQuantity(data.Block["number"])
			ts, tok := logOnlyQuantity(data.Block["timestamp"])
			parent, _ := data.Block["parentHash"].(string)
			if !ok || n != height || !tok || ts != cp.BlockTimestamp || !strings.EqualFold(parent, head.ParentHash) {
				return ErrManagedLiveBatchConflict
			}
			if r.checkEmpty && height >= r.emptyFrom {
				rows, err := accountScanRows(data)
				if err != nil {
					return err
				}
				if err := verifyEmptyBlockPage(r.chain, callbacks, rows, children); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// A missing or inconsistent input could hide a matching selector in an empty range.
func validateCallScanData(data rpc.AccountScanBlock) error {
	txs := data.Block["transactions"].([]map[string]any)
	inputs := make(map[string]any, len(txs))
	for _, tx := range txs {
		inputs[tx["hash"].(string)] = tx["input"]
	}
	for _, entry := range data.Traces {
		frame := entry.Trace
		if frame["type"] != "CALL" {
			continue
		}
		input, _ := frame["input"].(string)
		if _, err := hexutil.Decode(input); err != nil {
			return fmt.Errorf("invalid CALL input for %s: %w", entry.TransactionHash, err)
		}
		if path := frame["traceAddress"].([]int); len(path) == 0 {
			txInput, _ := inputs[entry.TransactionHash].(string)
			if !strings.EqualFold(input, txInput) {
				return fmt.Errorf("CALL input disagrees with transaction %s", entry.TransactionHash)
			}
		}
		if raw, ok := frame["output"]; ok && raw != nil {
			output, _ := raw.(string)
			if _, err := hexutil.Decode(output); err != nil {
				return fmt.Errorf("invalid CALL output for %s: %w", entry.TransactionHash, err)
			}
		}
	}
	return nil
}

func accountScanRows(data rpc.AccountScanBlock) (SourceEventData, error) {
	var rows SourceEventData
	block, err := events.SyncBlockToInternal(data.Block)
	if err != nil {
		return rows, err
	}
	rows.Blocks = []filter.Object{block}
	txs := data.Block["transactions"].([]map[string]any)
	byHash := make(map[string]filter.Object, len(txs))
	for _, tx := range txs {
		item, err := events.SyncTransactionToInternal(tx)
		if err != nil {
			return rows, err
		}
		rows.Transactions = append(rows.Transactions, item)
		byHash[tx["hash"].(string)] = tx
	}
	for _, receipt := range data.Receipts {
		item, err := events.SyncTransactionReceiptToInternal(receipt)
		if err != nil {
			return rows, err
		}
		rows.TransactionReceipts = append(rows.TransactionReceipts, item)
	}
	for _, trace := range data.Traces {
		item, err := events.SyncTraceToInternal(filter.Object{"trace": trace.Trace}, data.Block, byHash[trace.TransactionHash])
		if err != nil {
			return rows, err
		}
		rows.Traces = append(rows.Traces, item)
	}
	return rows, nil
}
