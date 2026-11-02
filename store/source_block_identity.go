package store

import (
	"context"
	"fmt"
	"strconv"
)

type SourceBlockIdentity struct {
	Number, Timestamp uint64
	Hash, ParentHash  string
}

// GetBlockIdentity reads the raw source header independently of learned handler
// projections. It adds no columns to the public decoded event.
func (s *SourceSyncStore) GetBlockIdentity(ctx context.Context, chain, number uint64) (out *SourceBlockIdentity, err error) {
	err = s.transaction(ctx, func(tx Transaction) error {
		rows, e := tx.Query(ctx, "SELECT number::text AS number,timestamp::text AS timestamp,hash,parent_hash FROM "+s.cache.table("blocks")+" WHERE chain_id=$1 AND number=$2", chain, number)
		if e != nil {
			return e
		}
		if len(rows.Rows) == 0 {
			return nil
		}
		if len(rows.Rows) != 1 {
			return fmt.Errorf("source header identity is not unique")
		}
		r := rows.Rows[0]
		n, e := strconv.ParseUint(fmt.Sprint(r["number"]), 10, 64)
		if e != nil {
			return e
		}
		t, e := strconv.ParseUint(fmt.Sprint(r["timestamp"]), 10, 64)
		if e != nil {
			return e
		}
		out = &SourceBlockIdentity{Number: n, Timestamp: t, Hash: fmt.Sprint(r["hash"]), ParentHash: fmt.Sprint(r["parent_hash"])}
		return nil
	})
	return
}
