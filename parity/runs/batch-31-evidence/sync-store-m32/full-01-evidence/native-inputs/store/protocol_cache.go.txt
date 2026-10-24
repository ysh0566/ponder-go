package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync/atomic"

	"ponder.local/ponder-go/schema"
)

// ProtocolCache uses the exact versioned source tables, independently of the
// indexing journal. This native adapter is not the full source SyncStore query
// planner or a claim of old database takeover.
type ProtocolCache struct {
	s         *Store
	namespace string
	closed    atomic.Bool
}

func (s *Store) EnableProtocolCache(ctx context.Context) (*ProtocolCache, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cache := s.protocol.Load(); cache != nil {
		if e := cache.active(); e != nil {
			return nil, e
		}
		return cache, nil
	}
	m, e := NewSyncMigrator(s.Backend, s.Namespace)
	if e != nil {
		return nil, e
	}
	if e = m.Migrate(ctx); e != nil {
		return nil, e
	}
	cache := &ProtocolCache{s: s, namespace: m.LatestSchema()}
	s.protocol.Store(cache)
	return cache, nil
}
func (c *ProtocolCache) Namespace() string { return c.namespace }
func (c *ProtocolCache) Close() error      { c.closed.Store(true); return nil }
func (c *ProtocolCache) active() error {
	if c.closed.Load() {
		return fmt.Errorf("protocol cache is closed")
	}
	return nil
}
func (c *ProtocolCache) table(name string) string {
	return schema.Quote(c.namespace) + "." + schema.Quote(name)
}
func (c *ProtocolCache) transaction(ctx context.Context, fn func(Transaction) error) error {
	if e := c.active(); e != nil {
		return e
	}
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if e := c.active(); e != nil {
		return e
	}
	tx, e := c.s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = c.s.lock(ctx, tx); e != nil {
		return e
	}
	if e = c.s.requireLease(ctx, tx); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// RPC field catalog mirrors sync-store/encode.ts. Encoding validates required
// quantities rather than silently filling malformed RPC responses with zeros.
// A suffix ? permits NULL, # converts hex to exact decimal, ! lowercases an
// address, @ JSON encodes accessList, and [] binds traceAddress as integer[].
var protocolFields = map[string][]string{
	"blocks":               {"number#", "timestamp#", "hash", "parentHash", "logsBloom", "miner!", "gasUsed#", "gasLimit#", "baseFeePerGas#?", "nonce?", "mixHash?", "stateRoot", "receiptsRoot", "transactionsRoot", "sha3Uncles?", "size#", "difficulty#", "totalDifficulty#?", "extraData"},
	"logs":                 {"blockNumber#", "logIndex#", "transactionIndex#", "blockHash", "transactionHash", "address!", "topic0?", "topic1?", "topic2?", "topic3?", "data"},
	"transactions":         {"blockNumber#", "transactionIndex#", "hash", "blockHash", "from!", "to!?", "input", "value#", "nonce#", "r?", "s?", "v#?", "type?", "gas#", "gasPrice#?", "maxFeePerGas#?", "maxPriorityFeePerGas#?", "accessList@?"},
	"transaction_receipts": {"blockNumber#", "transactionIndex#", "transactionHash", "blockHash", "from!", "to!?", "contractAddress!?", "logsBloom", "gasUsed#", "cumulativeGasUsed#", "effectiveGasPrice#", "status", "type"},
	"traces":               {"blockNumber#", "transactionIndex#", "traceAddress[]", "from!", "to!?", "input", "output?", "value#?", "type", "gas#", "gasUsed#"},
}

func snakeField(name string) string {
	var out strings.Builder
	for _, ch := range name {
		if ch >= 'A' && ch <= 'Z' {
			out.WriteByte('_')
			out.WriteRune(ch + 32)
		} else {
			out.WriteRune(ch)
		}
	}
	return out.String()
}

// EncodeProtocolRow exposes deterministic source-table encoding for differential
// testing. It accepts raw RPC hex quantities, never float conversions.
func EncodeProtocolRow(table string, chain uint64, input map[string]any) (Row, error) {
	fields, ok := protocolFields[table]
	if !ok {
		return nil, fmt.Errorf("unknown protocol relation %q", table)
	}
	if chain > math.MaxInt64 {
		return nil, fmt.Errorf("protocol chain ID exceeds source bigint")
	}
	out := Row{"chain_id": strconv.FormatUint(chain, 10)}
	for _, spec := range fields {
		name := strings.TrimRight(spec, "#?!@[]")
		value, present := input[name]
		if strings.HasPrefix(name, "topic") {
			if topics, ok := input["topics"].([]any); ok {
				i := int(name[5] - '0')
				if i < len(topics) {
					value, present = topics[i], true
				}
			}
			if topics, ok := input["topics"].([]string); ok {
				i := int(name[5] - '0')
				if i < len(topics) {
					value, present = topics[i], true
				}
			}
		}
		if table == "transactions" && name == "type" && (!present || value == nil || value == "") {
			value, present = "0x0", true
		}
		if !present || value == nil || value == "" {
			if !strings.HasSuffix(spec, "?") {
				return nil, fmt.Errorf("missing RPC %s.%s", table, name)
			}
			out[snakeField(name)] = nil
			continue
		}
		if strings.Contains(spec, "#") {
			text, ok := value.(string)
			if !ok || !strings.HasPrefix(text, "0x") {
				return nil, fmt.Errorf("RPC %s.%s must be a hex quantity", table, name)
			}
			n, ok := new(big.Int).SetString(text[2:], 16)
			if !ok || n.Sign() < 0 {
				return nil, fmt.Errorf("invalid RPC quantity %s.%s", table, name)
			}
			value = n.String()
			if name == "transactionIndex" || name == "logIndex" || name == "nonce" && table == "transactions" {
				if !n.IsInt64() || n.Int64() > math.MaxInt32 {
					return nil, fmt.Errorf("RPC %s.%s exceeds source integer", table, name)
				}
				value = n.Int64()
			}
		} else if strings.Contains(spec, "!") {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("RPC %s.%s must be an address", table, name)
			}
			value = strings.ToLower(text)
		} else if strings.Contains(spec, "@") {
			data, e := json.Marshal(value)
			if e != nil {
				return nil, e
			}
			value = string(data)
		} else if strings.Contains(spec, "[]") {
			data, e := json.Marshal(value)
			if e != nil {
				return nil, e
			}
			var numbers []int32
			if e = json.Unmarshal(data, &numbers); e != nil {
				return nil, e
			}
			parts := make([]string, len(numbers))
			for i, n := range numbers {
				if n < 0 {
					return nil, fmt.Errorf("negative trace address")
				}
				parts[i] = strconv.FormatInt(int64(n), 10)
			}
			value = "{" + strings.Join(parts, ",") + "}"
		}
		out[snakeField(name)] = value
	}
	return out, nil
}
func (c *ProtocolCache) insert(ctx context.Context, tx Transaction, table string, chain uint64, inputs []map[string]any) error {
	for _, input := range inputs {
		row, e := EncodeProtocolRow(table, chain, input)
		if e != nil {
			return e
		}
		columns, params, args := []string{`"chain_id"`}, []string{"$1"}, []any{row["chain_id"]}
		for _, spec := range protocolFields[table] {
			name := snakeField(strings.TrimRight(spec, "#?!@[]"))
			columns = append(columns, schema.Quote(name))
			args = append(args, row[name])
			params = append(params, fmt.Sprintf("$%d", len(args)))
		}
		if e = tx.Exec(ctx, "INSERT INTO "+c.table(table)+" ("+strings.Join(columns, ",")+") VALUES ("+strings.Join(params, ",")+") ON CONFLICT DO NOTHING", args...); e != nil {
			return e
		}
	}
	return nil
}
func (c *ProtocolCache) Insert(ctx context.Context, table string, chain uint64, inputs []map[string]any) error {
	if _, ok := protocolFields[table]; !ok {
		return fmt.Errorf("unknown protocol relation %q", table)
	}
	return c.transaction(ctx, func(tx Transaction) error { return c.insert(ctx, tx, table, chain, inputs) })
}

// Rows returns exact source-table decimal/text columns over a block range.
// Full source filtering, include projections and pagination remain separate.
func (c *ProtocolCache) Rows(ctx context.Context, table string, chain, from, to uint64) (Result, error) {
	if e := c.active(); e != nil {
		return Result{}, e
	}
	if _, ok := protocolFields[table]; !ok {
		return Result{}, fmt.Errorf("unknown protocol relation %q", table)
	}
	column := "block_number"
	if table == "blocks" {
		column = "number"
	}
	return c.s.Backend.Query(ctx, "SELECT row_to_json(t)::text AS row FROM "+c.table(table)+" t WHERE chain_id=$1 AND "+column+">=$2 AND "+column+"<=$3 ORDER BY row_to_json(t)::text", chain, from, to)
}
func (c *ProtocolCache) saveBlock(ctx context.Context, tx Transaction, block SyncBlock) error {
	if e := c.active(); e != nil {
		return e
	}
	var data map[string]any
	if e := json.Unmarshal(block.Payload, &data); e != nil {
		return e
	}
	prior, e := tx.Query(ctx, "SELECT hash FROM "+c.table("blocks")+" WHERE chain_id=$1 AND number=$2", block.ChainID, block.Number)
	if e != nil {
		return e
	}
	if len(prior.Rows) > 0 && prior.Rows[0]["hash"] != block.Hash {
		return fmt.Errorf("canonical block hash changed without cache rewind")
	}
	if e := c.insert(ctx, tx, "blocks", block.ChainID, []map[string]any{data}); e != nil {
		return e
	}
	transactions, ok := data["transactions"].([]any)
	if !ok && data["transactions"] != nil {
		return fmt.Errorf("protocol cache requires full RPC transactions")
	}
	for _, value := range transactions {
		transaction, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("protocol cache requires full RPC transactions")
		}
		if e := c.insert(ctx, tx, "transactions", block.ChainID, []map[string]any{transaction}); e != nil {
			return e
		}
	}
	return nil
}
func (c *ProtocolCache) rewind(ctx context.Context, tx Transaction, chain, number uint64) error {
	for _, table := range []string{"blocks", "logs", "transactions", "transaction_receipts", "traces", "factory_addresses", "rpc_request_results"} {
		column := "block_number"
		if table == "blocks" {
			column = "number"
		}
		where := column + ">$2"
		if table == "rpc_request_results" {
			where = "(" + where + " OR block_number IS NULL)"
		}
		if e := tx.Exec(ctx, "DELETE FROM "+c.table(table)+" WHERE chain_id=$1 AND "+where, chain, number); e != nil {
			return e
		}
	}
	// Source fragment IDs use closed type/chain prefixes and store end+1.
	// Native completion descriptors retain their point-interval boundary.
	if e := tx.Exec(ctx, "UPDATE "+c.table("intervals")+" SET blocks=blocks * nummultirange(numrange(0,CASE WHEN fragment_id ~ '^(block|log|transaction|transfer|trace|factory_log)_-?[0-9]+_' THEN $2::numeric+1 ELSE $2::numeric END,'[]')) WHERE chain_id=$1", chain, number); e != nil {
		return e
	}
	return nil
}

func (c *ProtocolCache) complete(ctx context.Context, tx Transaction, chain, number uint64, fragment string) error {
	return tx.Exec(ctx, "INSERT INTO "+c.table("intervals")+"(fragment_id,chain_id,blocks) VALUES($1,$2,nummultirange(numrange($3::numeric,$3::numeric,'[]'))) ON CONFLICT(fragment_id) DO UPDATE SET blocks="+c.table("intervals")+".blocks + excluded.blocks", fragment, chain, number)
}
func (c *ProtocolCache) factoryChildren(ctx context.Context, tx Transaction, id string, chain, block uint64, children []FactoryChild) error {
	// The native API permits the same caller ID on different chains. Source
	// factory fragment IDs already embed their chain; mirror that separation
	// explicitly rather than colliding on (factory_id,address).
	id = fmt.Sprintf("native:%d:%s", chain, id)
	r, e := tx.Query(ctx, "INSERT INTO "+c.table("factories")+"(fragment_id) VALUES($1) ON CONFLICT(fragment_id) DO UPDATE SET fragment_id=excluded.fragment_id RETURNING id", id)
	if e != nil {
		return e
	}
	factoryID := r.Rows[0]["id"]
	if e = tx.Exec(ctx, "DELETE FROM "+c.table("factory_addresses")+" WHERE factory_id=$1 AND chain_id=$2 AND block_number=$3", factoryID, chain, block); e != nil {
		return e
	}
	for _, child := range children {
		if e = tx.Exec(ctx, "INSERT INTO "+c.table("factory_addresses")+"(factory_id,chain_id,block_number,address) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", factoryID, chain, block, child.Address); e != nil {
			return e
		}
	}
	return nil
}

// A disabled/cold runtime must still invalidate an already owned disk cache.
// Discover it in the rewind transaction without enabling cache reads/writes or
// creating schemas. Ownership and completeness are verified before any DELETE.
func (s *Store) rewindProtocolCache(ctx context.Context, tx Transaction, chain, number uint64) error {
	cache := s.protocol.Load()
	if cache == nil {
		m, e := NewSyncMigrator(s.Backend, s.Namespace)
		if e != nil {
			return e
		}
		exists, e := m.owned(ctx, tx, m.LatestSchema(), m.latestOwner())
		if e != nil {
			return e
		}
		if !exists {
			return nil
		}
		if e = m.validateLatest(ctx, tx); e != nil {
			return e
		}
		cache = &ProtocolCache{s: s, namespace: m.LatestSchema()}
	}
	return cache.rewind(ctx, tx, chain, number)
}

// Cache keys here are native JSON-RPC request keys, hashed with SHA256 and
// scoped by chain. This is not the original request-hash algorithm or a claim
// that already stored TS-client RPC cache entries are reusable.
func protocolRequest(key string) (uint64, string, error) {
	var parts []json.RawMessage
	if e := json.Unmarshal([]byte(key), &parts); e != nil || len(parts) != 3 {
		return 0, "", fmt.Errorf("invalid native RPC request key")
	}
	var chain uint64
	if e := json.Unmarshal(parts[0], &chain); e != nil || chain > math.MaxInt64 {
		return 0, "", fmt.Errorf("invalid RPC chain ID")
	}
	return chain, fmt.Sprintf("%x", sha256.Sum256([]byte(key))), nil
}
func (c *ProtocolCache) CacheGet(ctx context.Context, key string) (json.RawMessage, bool, error) {
	if e := c.active(); e != nil {
		return nil, false, e
	}
	chain, hash, e := protocolRequest(key)
	if e != nil {
		return nil, false, e
	}
	r, e := c.s.Backend.Query(ctx, "SELECT result FROM "+c.table("rpc_request_results")+" WHERE chain_id=$1 AND request_hash=$2", chain, hash)
	if e != nil || len(r.Rows) == 0 {
		return nil, false, e
	}
	return json.RawMessage(fmt.Sprint(r.Rows[0]["result"])), true, nil
}
func (c *ProtocolCache) CachePut(ctx context.Context, key string, value json.RawMessage) error {
	chain, hash, e := protocolRequest(key)
	if e != nil {
		return e
	}
	if !json.Valid(value) {
		return fmt.Errorf("RPC result must be JSON")
	}
	return c.transaction(ctx, func(tx Transaction) error {
		return tx.Exec(ctx, "INSERT INTO "+c.table("rpc_request_results")+"(chain_id,request_hash,block_number,result) VALUES($1,$2,NULL,$3) ON CONFLICT(chain_id,request_hash) DO UPDATE SET result=excluded.result", chain, hash, string(value))
	})
}
