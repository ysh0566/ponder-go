package store

import (
	"context"
	"encoding/json"
	"fmt"
	"ponder.local/ponder-go/interval"
	"strconv"
)

type SyncBlock struct {
	ChainID    uint64          `json:"chainId"`
	Number     uint64          `json:"number"`
	Hash       string          `json:"hash"`
	ParentHash string          `json:"parentHash"`
	Timestamp  uint64          `json:"timestamp"`
	Payload    json.RawMessage `json:"payload"`
}

func (s *Store) SyncedBlock(ctx context.Context, chain, number uint64) (*SyncBlock, error) {
	r, e := s.Backend.Query(ctx, "SELECT hash,parent_hash,timestamp::text AS timestamp,payload::text AS payload FROM "+s.syncTable("blocks")+" WHERE chain_id=$1 AND number=$2", chain, number)
	if e != nil {
		return nil, e
	}
	if len(r.Rows) == 0 {
		return nil, nil
	}
	row := r.Rows[0]
	timestamp, e := strconv.ParseUint(fmt.Sprint(row["timestamp"]), 10, 64)
	if e != nil {
		return nil, e
	}
	return &SyncBlock{ChainID: chain, Number: number, Hash: fmt.Sprint(row["hash"]), ParentHash: fmt.Sprint(row["parent_hash"]), Timestamp: timestamp, Payload: json.RawMessage(fmt.Sprint(row["payload"]))}, nil
}
func (s *Store) CompleteBlock(ctx context.Context, b SyncBlock, filter string) error {
	tx, e := s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = s.lock(ctx, tx); e != nil {
		return e
	}
	if e = tx.Exec(ctx, "INSERT INTO "+s.syncTable("blocks")+" VALUES ($1,$2,$3,$4,$5,$6::jsonb) ON CONFLICT(chain_id,number) DO UPDATE SET hash=excluded.hash,parent_hash=excluded.parent_hash,timestamp=excluded.timestamp,payload=excluded.payload", b.ChainID, b.Number, b.Hash, b.ParentHash, b.Timestamp, string(b.Payload)); e != nil {
		return e
	}
	r, e := tx.Query(ctx, "SELECT start_block::text AS start,end_block::text AS end FROM "+s.syncTable("intervals")+" WHERE chain_id=$1 AND filter=$2", b.ChainID, filter)
	if e != nil {
		return e
	}
	xs := []interval.Interval{{int64(b.Number), int64(b.Number)}}
	for _, row := range r.Rows {
		a, _ := strconv.ParseInt(fmt.Sprint(row["start"]), 10, 64)
		z, _ := strconv.ParseInt(fmt.Sprint(row["end"]), 10, 64)
		xs = append(xs, interval.Interval{a, z})
	}
	if e = tx.Exec(ctx, "DELETE FROM "+s.syncTable("intervals")+" WHERE chain_id=$1 AND filter=$2", b.ChainID, filter); e != nil {
		return e
	}
	for _, x := range interval.Union(xs) {
		if e = tx.Exec(ctx, "INSERT INTO "+s.syncTable("intervals")+" VALUES ($1,$2,$3,$4)", b.ChainID, filter, x[0], x[1]); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) Missing(ctx context.Context, chain uint64, filter string, wanted interval.Interval) ([]interval.Interval, error) {
	r, e := s.Backend.Query(ctx, "SELECT start_block::text AS start,end_block::text AS end FROM "+s.syncTable("intervals")+" WHERE chain_id=$1 AND filter=$2 ORDER BY start_block", chain, filter)
	if e != nil {
		return nil, e
	}
	xs := []interval.Interval{}
	for _, row := range r.Rows {
		a, _ := strconv.ParseInt(fmt.Sprint(row["start"]), 10, 64)
		z, _ := strconv.ParseInt(fmt.Sprint(row["end"]), 10, 64)
		xs = append(xs, interval.Interval{a, z})
	}
	return interval.Difference([]interval.Interval{wanted}, interval.Union(xs)), nil
}
func (s *Store) RewindSync(ctx context.Context, chain, number uint64) error {
	tx, e := s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = tx.Exec(ctx, "DELETE FROM "+s.syncTable("blocks")+" WHERE chain_id=$1 AND number>$2", chain, number); e != nil {
		return e
	}
	if e = tx.Exec(ctx, "DELETE FROM "+s.syncTable("intervals")+" WHERE chain_id=$1 AND start_block>$2", chain, number); e != nil {
		return e
	}
	if e = tx.Exec(ctx, "UPDATE "+s.syncTable("intervals")+" SET end_block=LEAST(end_block,$2) WHERE chain_id=$1", chain, number); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	r, e := s.Backend.Query(ctx, "SELECT value::text AS value FROM "+s.syncTable("rpc_cache")+" WHERE key=$1", key)
	if e != nil || len(r.Rows) == 0 {
		return nil, false, e
	}
	return json.RawMessage(fmt.Sprint(r.Rows[0]["value"])), true, nil
}
func (s *Store) CachePut(ctx context.Context, key string, value json.RawMessage) error {
	return s.Backend.Exec(ctx, "INSERT INTO "+s.syncTable("rpc_cache")+" VALUES ($1,$2::jsonb) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(value))
}
