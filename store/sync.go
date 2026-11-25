package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ysh0566/ponder-go/interval"
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

// LatestSyncedHeight includes a header persisted before its callbacks committed.
func (s *Store) LatestSyncedHeight(ctx context.Context, chain uint64) (uint64, bool, error) {
	r, err := s.Backend.Query(ctx, "SELECT number::text AS number FROM "+s.syncTable("blocks")+" WHERE chain_id=$1 ORDER BY number::numeric DESC LIMIT 1", chain)
	if err != nil || len(r.Rows) == 0 {
		return 0, false, err
	}
	n, err := strconv.ParseUint(fmt.Sprint(r.Rows[0]["number"]), 10, 64)
	return n, true, err
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

// IndexedFinalityCandidate returns the newest indexed header within the reorg
// cutoff. Genesis, when indexed, is immutable even during the initial window.
func (s *Store) IndexedFinalityCandidate(ctx context.Context, chain, through, cutoff uint64) (*SyncBlock, error) {
	const fields = "SELECT number::text AS height,timestamp::text AS time,hash FROM "
	rows, err := s.Backend.Query(ctx, fields+s.syncTable("blocks")+" WHERE chain_id=$1 AND number<=$2 AND (timestamp<=$3 OR number=0) ORDER BY number DESC LIMIT 1", chain, through, cutoff)
	if err != nil || len(rows.Rows) == 0 {
		return nil, err
	}
	row := rows.Rows[0]
	height, err := strconv.ParseUint(fmt.Sprint(row["height"]), 10, 64)
	if err != nil {
		return nil, err
	}
	timestamp, err := strconv.ParseUint(fmt.Sprint(row["time"]), 10, 64)
	if err != nil {
		return nil, err
	}
	return &SyncBlock{ChainID: chain, Number: height, Timestamp: timestamp, Hash: fmt.Sprint(row["hash"])}, nil
}

func (s *Store) CompleteBlock(ctx context.Context, b SyncBlock, filter string) error {
	if s.managedLive != nil {
		return s.managedLive.rejectLegacy(ctx)
	}
	tx, e := s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = s.lock(ctx, tx); e != nil {
		return e
	}
	if e = s.requireLease(ctx, tx); e != nil {
		return e
	}
	if e = s.saveBlock(ctx, tx, b); e != nil {
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
	if cache := s.protocol.Load(); cache != nil {
		if e = cache.complete(ctx, tx, b.ChainID, b.Number, filter); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

// SaveBlock persists provenance before indexing commits, without marking a
// history interval complete. A restart can then recognize an orphaned handler
// commit even if the process exited before CompleteBlock.
func (s *Store) SaveBlock(ctx context.Context, b SyncBlock) error {
	if s.managedLive != nil {
		return s.managedLive.rejectLegacy(ctx)
	}
	tx, e := s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = s.lock(ctx, tx); e != nil {
		return e
	}
	if e = s.requireLease(ctx, tx); e != nil {
		return e
	}
	if e = s.saveBlock(ctx, tx, b); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Store) saveBlock(ctx context.Context, tx Transaction, b SyncBlock) error {
	if cache := s.protocol.Load(); cache != nil {
		if e := cache.saveBlock(ctx, tx, b); e != nil {
			return e
		}
	}
	return tx.Exec(ctx, "INSERT INTO "+s.syncTable("blocks")+" VALUES ($1,$2,$3,$4,$5,$6::jsonb) ON CONFLICT(chain_id,number) DO UPDATE SET hash=excluded.hash,parent_hash=excluded.parent_hash,timestamp=excluded.timestamp,payload=excluded.payload", b.ChainID, b.Number, b.Hash, b.ParentHash, b.Timestamp, string(b.Payload))
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
	if s.managedLive != nil {
		return s.managedLive.rejectLegacy(ctx)
	}
	tx, e := s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = s.lock(ctx, tx); e != nil {
		return e
	}
	if e = s.requireLease(ctx, tx); e != nil {
		return e
	}
	if e = s.rewindSync(ctx, tx, chain, number); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Store) rewindSync(ctx context.Context, tx Transaction, chain, number uint64) error {
	return s.rewindSyncThrough(ctx, tx, chain, strconv.FormatUint(number, 10))
}

// keep is the inclusive last height; -1 clears the chain back through genesis.
func (s *Store) rewindSyncThrough(ctx context.Context, tx Transaction, chain uint64, keep string) error {
	if e := s.rewindProtocolCache(ctx, tx, chain, keep); e != nil {
		return e
	}
	var e error
	if e = tx.Exec(ctx, "DELETE FROM "+s.syncTable("blocks")+" WHERE chain_id=$1 AND number>$2", chain, keep); e != nil {
		return e
	}
	if e = tx.Exec(ctx, "DELETE FROM "+s.syncTable("factory_addresses")+" WHERE chain_id=$1 AND block_number>$2", chain, keep); e != nil {
		return e
	}
	if e = tx.Exec(ctx, "DELETE FROM "+s.syncTable("intervals")+" WHERE chain_id=$1 AND start_block>$2", chain, keep); e != nil {
		return e
	}
	if e = tx.Exec(ctx, "UPDATE "+s.syncTable("intervals")+" SET end_block=LEAST(end_block,$2) WHERE chain_id=$1", chain, keep); e != nil {
		return e
	}
	return nil
}

type FactoryChild struct {
	Address  string
	LogIndex uint64
}

// RecordFactoryChildren replaces one canonical block's discoveries atomically.
// Sync provenance survives handler failures and is replay-safe; it is kept out
// of the indexing journal and removed when the sync store rewinds a fork.
func (s *Store) RecordFactoryChildren(ctx context.Context, id string, chain, block uint64, hash string, children []FactoryChild) error {
	if s.managedLive != nil {
		return s.managedLive.rejectLegacy(ctx)
	}
	tx, e := s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = s.lock(ctx, tx); e != nil {
		return e
	}
	if e = s.requireLease(ctx, tx); e != nil {
		return e
	}
	if cache := s.protocol.Load(); cache != nil {
		if e = cache.factoryChildren(ctx, tx, id, chain, block, children); e != nil {
			return e
		}
	}
	if e = tx.Exec(ctx, "DELETE FROM "+s.syncTable("factory_addresses")+" WHERE factory_id=$1 AND chain_id=$2 AND block_number=$3", id, chain, block); e != nil {
		return e
	}
	for _, child := range children {
		if e = tx.Exec(ctx, "INSERT INTO "+s.syncTable("factory_addresses")+" VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING", id, chain, child.Address, block, hash, child.LogIndex); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) FactoryChildren(ctx context.Context, id string, chain, through uint64) (map[string]uint64, error) {
	r, e := s.Backend.Query(ctx, "SELECT address,MIN(block_number)::text AS block FROM "+s.syncTable("factory_addresses")+" WHERE factory_id=$1 AND chain_id=$2 AND block_number<=$3 GROUP BY address ORDER BY address", id, chain, through)
	if e != nil {
		return nil, e
	}
	children := map[string]uint64{}
	for _, row := range r.Rows {
		n, e := strconv.ParseUint(fmt.Sprint(row["block"]), 10, 64)
		if e != nil {
			return nil, e
		}
		children[fmt.Sprint(row["address"])] = n
	}
	return children, nil
}
func (s *Store) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	r, e := s.Backend.Query(ctx, "SELECT value::text AS value FROM "+s.syncTable("rpc_cache")+" WHERE key=$1", key)
	if e != nil || len(r.Rows) == 0 {
		return nil, false, e
	}
	return json.RawMessage(fmt.Sprint(r.Rows[0]["value"])), true, nil
}
func (s *Store) CachePut(ctx context.Context, key string, value json.RawMessage) error {
	if s.managedLive != nil {
		return s.managedLive.rejectLegacy(ctx)
	}
	if s.leaseToken != "" {
		tx, e := s.Backend.Begin(ctx)
		if e != nil {
			return e
		}
		defer tx.Rollback(context.Background())
		if e = s.lock(ctx, tx); e != nil {
			return e
		}
		if e = s.requireLease(ctx, tx); e != nil {
			return e
		}
		if e = tx.Exec(ctx, "INSERT INTO "+s.syncTable("rpc_cache")+" VALUES ($1,$2::jsonb) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(value)); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if pin := s.deploymentSession; pin != nil {
		// Cache writes may run inside a callback that already holds the namespace
		// lock. Check the session in the INSERT itself on the cache connection.
		rows, err := s.Backend.Query(ctx, "INSERT INTO "+s.syncTable("rpc_cache")+" SELECT $1,$2::jsonb WHERE EXISTS(SELECT FROM pg_stat_get_activity($3) WHERE backend_start=$4) ON CONFLICT(key) DO UPDATE SET value=excluded.value RETURNING key", key, string(value), pin.pid, pin.started)
		if err != nil {
			return err
		}
		if len(rows.Rows) == 0 {
			return errDeploymentDisconnected
		}
		return nil
	}
	return s.Backend.Exec(ctx, "INSERT INTO "+s.syncTable("rpc_cache")+" VALUES ($1,$2::jsonb) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(value))
}
