package runtime

import (
	"context"
	"fmt"
	selection "ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
	"strings"
)

func (e *Engine) discoverFactories(ctx context.Context, b Block, number uint64) error {
	seen := map[string]bool{}
	for _, source := range e.Config.Contracts {
		f, ok := e.factories[source.Name]
		if !ok || seen[f.ID] {
			continue
		}
		seen[f.ID] = true
		if f.FromBlock != nil && number < *f.FromBlock || f.ToBlock != nil && number > *f.ToBlock {
			continue
		}
		query := map[string]any{"blockHash": b.Hash, "topics": []any{f.EventSelector}}
		empty := false
		switch address := f.Address.(type) {
		case string:
			query["address"] = address
		case []string:
			if len(address) == 0 {
				empty = true
			} else {
				query["address"] = address
			}
		}
		logs := []Log{}
		if !empty {
			if err := e.Client.CallCached(ctx, "eth_getLogs", []any{query}, &logs); err != nil {
				return err
			}
		}
		for _, log := range logs {
			if log.BlockHash != "" && !strings.EqualFold(log.BlockHash, b.Hash) {
				return fmt.Errorf("factory RPC log belongs to a different block")
			}
		}
		if err := e.cacheLogs(ctx, logs); err != nil {
			return err
		}
		children := []store.FactoryChild{}
		for _, log := range logs {
			if log.Removed || !selection.IsLogFactoryMatched(nativeFactory(f), nativeLog(log, number)) {
				continue
			}
			address, err := f.ChildAddress(log.Topics, log.Data)
			if err != nil {
				return fmt.Errorf("factory %s: %w", f.ID, err)
			}
			index, err := Number(log.LogIndex)
			if err != nil {
				return err
			}
			children = append(children, store.FactoryChild{Address: strings.ToLower(address), LogIndex: index})
		}
		if err := e.Store.RecordFactoryChildren(ctx, f.ID, e.chain.ID, number, b.Hash, children); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) cacheLogs(ctx context.Context, logs []Log) error {
	if e.cache == nil || len(logs) == 0 {
		return nil
	}
	rows := make([]map[string]any, len(logs))
	for i, log := range logs {
		rows[i] = map[string]any{"blockNumber": log.BlockNumber, "logIndex": log.LogIndex, "transactionIndex": log.TransactionIndex, "blockHash": log.BlockHash, "transactionHash": log.TransactionHash, "address": log.Address, "topics": log.Topics, "data": log.Data}
	}
	return e.cache.Insert(ctx, "logs", e.chain.ID, rows)
}
