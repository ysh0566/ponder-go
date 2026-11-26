package store

import (
	"context"
	"strings"

	"github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/interval"
	"github.com/ysh0566/ponder-go/rpc"
)

func (reader LogScanRPC) scanAccountRanges(ctx context.Context, r logScanRange, filters []filter.Object, children logFactoryChildren, get ManagedLiveHeaderReader) error {
	var accounts []filter.Object
	var spans []interval.Interval
	for _, f := range filters {
		if f["type"] != "transaction" && f["type"] != "transfer" {
			continue
		}
		start, end, err := logSourceBounds(f)
		if err != nil {
			return err
		}
		from, to := max(start, r.from), min(end, r.to)
		if from <= to {
			spans = append(spans, interval.Interval{int64(from), int64(to)})
			accounts = append(accounts, filter.Object{"type": "account", "filter": f})
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
			transfers := false
			for _, cb := range accounts {
				f := cb["filter"].(filter.Object)
				if f["type"] == "transfer" && filter.IsBlockInFilter(f, float64(height)) {
					transfers = true
					break
				}
			}
			data, err := reader.Actions.GetAccountScanBlock(ctx, head.Hash, transfers, &reader.Options)
			if err != nil {
				return err
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
				if err := verifyEmptyAccountPage(r.chain, accounts, rows, children); err != nil {
					return err
				}
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
