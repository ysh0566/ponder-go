package store

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/schema"
)

// These source methods preserve per-batch commit and explicit conflict targets.
// EncodeProtocolRow is reused on real valid RPC objects; wider JS/Go coercion
// and invalid/empty optional-field equivalence still need source differentials.
func (s *SourceSyncStore) insertSourceRows(ctx context.Context, table string, chain uint64, inputs []filter.Object) error {
	if err := s.scopeActive(ctx); err != nil {
		return err
	}
	if len(inputs) == 0 {
		return nil
	}
	fields, ok := protocolFields[table]
	if !ok {
		return fmt.Errorf("unknown source sync relation %q", table)
	}
	if _, e := EncodeProtocolRow(table, chain, inputs[0]); e != nil {
		return e
	}
	columns := []string{"chain_id"}
	for _, spec := range fields {
		columns = append(columns, snakeField(strings.TrimRight(spec, "#?!@[]")))
	}
	budget := s.maxQueryParameters / len(columns)
	if budget == 0 {
		return fmt.Errorf("source sync parameter budget smaller than encoded row")
	}
	conflict := map[string]string{"logs": "chain_id,block_number,log_index", "blocks": "chain_id,number", "transactions": "chain_id,block_number,transaction_index", "transaction_receipts": "chain_id,block_number,transaction_index", "traces": "chain_id,block_number,transaction_index,trace_address"}[table]
	quoted := make([]string, len(columns))
	for i, col := range columns {
		quoted[i] = schema.Quote(col)
	}
	for offset := 0; offset < len(inputs); offset += budget {
		end := min(offset+budget, len(inputs))
		args := []any{}
		values := []string{}
		for _, input := range inputs[offset:end] {
			row, e := EncodeProtocolRow(table, chain, input)
			if e != nil {
				return e
			}
			slots := []string{}
			for _, column := range columns {
				args = append(args, row[column])
				slots = append(slots, fmt.Sprintf("$%d", len(args)))
			}
			values = append(values, "("+strings.Join(slots, ",")+")")
		}
		if e := s.transaction(ctx, func(tx Transaction) error {
			return tx.Exec(ctx, "INSERT INTO "+s.cache.table(table)+"("+strings.Join(quoted, ",")+") VALUES"+strings.Join(values, ",")+" ON CONFLICT("+conflict+") DO NOTHING", args...)
		}); e != nil {
			return e
		}
	}
	return nil
}
func (s *SourceSyncStore) InsertLogs(ctx context.Context, chain uint64, logs []filter.Object) error {
	return s.insertSourceRows(ctx, "logs", chain, logs)
}
func (s *SourceSyncStore) InsertBlocks(ctx context.Context, chain uint64, blocks []filter.Object) error {
	return s.insertSourceRows(ctx, "blocks", chain, blocks)
}
func (s *SourceSyncStore) InsertTransactions(ctx context.Context, chain uint64, transactions []filter.Object) error {
	return s.insertSourceRows(ctx, "transactions", chain, transactions)
}
func (s *SourceSyncStore) InsertTransactionReceipts(ctx context.Context, chain uint64, receipts []filter.Object) error {
	return s.insertSourceRows(ctx, "transaction_receipts", chain, receipts)
}

type SourceTraceInput struct{ Trace, Block, Transaction filter.Object }

func (s *SourceSyncStore) InsertTraces(ctx context.Context, chain uint64, traces []SourceTraceInput) error {
	if err := s.scopeActive(ctx); err != nil {
		return err
	}
	rows := make([]filter.Object, len(traces))
	for i, item := range traces {
		inner, ok := item.Trace["trace"].(filter.Object)
		if !ok {
			return fmt.Errorf("missing source trace.trace")
		}
		r := filter.Object{}
		for k, v := range inner {
			r[k] = v
		}
		r["blockNumber"] = item.Block["number"]
		r["transactionIndex"] = item.Transaction["transactionIndex"]
		rows[i] = r
	}
	return s.insertSourceRows(ctx, "traces", chain, rows)
}

// Source pruning deliberately retains intervals, RPC results and factory rows.
// The canonical native rewind API has a different, broader provenance contract.
func (s *SourceSyncStore) PruneByChain(ctx context.Context, chain uint64) error {
	return s.transaction(ctx, func(tx Transaction) error {
		for _, table := range []string{"logs", "blocks", "traces", "transactions", "transaction_receipts", "factory_addresses"} {
			if e := tx.Exec(ctx, "DELETE FROM "+s.cache.table(table)+" WHERE chain_id=$1", chain); e != nil {
				return e
			}
		}
		return nil
	})
}

type SourceRecoveryBlock struct{ Number, Timestamp *big.Int }

func (s *SourceSyncStore) GetSafeCrashRecoveryBlock(ctx context.Context, chain uint64, timestamp int64) (*SourceRecoveryBlock, error) {
	var result *SourceRecoveryBlock
	err := s.transaction(ctx, func(tx Transaction) error {
		rows, e := tx.Query(ctx, "SELECT number::text AS number,timestamp::text AS timestamp FROM "+s.cache.table("blocks")+" WHERE chain_id=$1 AND timestamp<$2 ORDER BY number DESC LIMIT 1", chain, timestamp)
		if e != nil {
			return e
		}
		if len(rows.Rows) == 0 {
			return nil
		}
		n, ok := new(big.Int).SetString(fmt.Sprint(rows.Rows[0]["number"]), 10)
		if !ok {
			return fmt.Errorf("invalid source recovery block")
		}
		t, ok := new(big.Int).SetString(fmt.Sprint(rows.Rows[0]["timestamp"]), 10)
		if !ok {
			return fmt.Errorf("invalid source recovery timestamp")
		}
		result = &SourceRecoveryBlock{n, t}
		return nil
	})
	return result, err
}
