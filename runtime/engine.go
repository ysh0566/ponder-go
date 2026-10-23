// Package runtime provides the first complete native Go single-chain indexing path.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/factory"
	"ponder.local/ponder-go/finality"
	"ponder.local/ponder-go/interval"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Block struct {
	Number       string           `json:"number"`
	Hash         string           `json:"hash"`
	ParentHash   string           `json:"parentHash"`
	Timestamp    string           `json:"timestamp"`
	Transactions []map[string]any `json:"transactions"`
	raw          json.RawMessage
}

func (b *Block) UnmarshalJSON(data []byte) error {
	type blockAlias Block
	var value blockAlias
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*b = Block(value)
	b.raw = append(json.RawMessage{}, data...)
	return nil
}
func (b Block) MarshalJSON() ([]byte, error) {
	if len(b.raw) > 0 {
		return append([]byte{}, b.raw...), nil
	}
	type blockAlias Block
	return json.Marshal(blockAlias(b))
}

type Log struct {
	BlockNumber      string   `json:"blockNumber"`
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	BlockHash        string   `json:"blockHash"`
	TransactionHash  string   `json:"transactionHash"`
	TransactionIndex string   `json:"transactionIndex"`
	LogIndex         string   `json:"logIndex"`
	Removed          bool     `json:"removed"`
}
type Event struct {
	Name        string
	Args        any
	Block       Block
	Log         *Log
	Transaction map[string]any
	Receipt     map[string]any
	SetupBlock  uint64
}
type Context struct {
	Event  Event
	DB     *store.Writer
	Client *rpc.Client
	Chain  config.Chain
}
type Handler func(context.Context, *Context) error
type Engine struct {
	Config    config.Config
	Store     *store.Store
	Handlers  map[string]Handler
	Client    *rpc.Client
	abis      map[string]abi.ABI
	factories map[string]factory.LogFactory
	filter    string
	chain     config.Chain
	options   Options
	cache     *store.ProtocolCache
	syncMu    sync.Mutex
	staged    *stagedState
}
type Options struct{ DisableCache bool }

func New(c config.Config, s *store.Store, handlers map[string]Handler) (*Engine, error) {
	return NewWithOptions(c, s, handlers, Options{})
}
func NewWithOptions(c config.Config, s *store.Store, handlers map[string]Handler, options Options) (*Engine, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	ordering := c.Ordering
	if ordering == "" {
		ordering = schema.Multichain
	}
	if ordering != s.Ordering {
		return nil, fmt.Errorf("runtime and store ordering differ")
	}
	if len(c.Chains) != 1 {
		return nil, fmt.Errorf("multi-chain runtime scheduling is not implemented yet; storage rollback modes are implemented")
	}
	engine := &Engine{Config: c, Store: s, Handlers: handlers, chain: c.Chains[0], abis: map[string]abi.ABI{}, factories: map[string]factory.LogFactory{}, options: options}
	for _, contract := range c.Contracts {
		a, e := abi.Parse(string(contract.ABI))
		if e != nil {
			return nil, e
		}
		engine.abis[contract.Name] = a
		if contract.Factory != nil {
			cfg := *contract.Factory
			cfg.ChainID = engine.chain.ID
			cfg.SourceID = contract.Name
			if cfg.FromBlock == nil {
				n := contract.StartBlock
				cfg.FromBlock = &n
			}
			f, e := factory.Build(cfg)
			if e != nil {
				return nil, e
			}
			engine.factories[contract.Name] = f
		}
	}
	encoded, _ := json.Marshal(c)
	hash := sha256.Sum256(encoded)
	engine.filter = fmt.Sprintf("runtime:%x", hash)
	engine.Client = rpc.New(engine.chain.RPC...)
	engine.Client.ChainID = engine.chain.ID
	return engine, nil
}
func Number(hex string) (uint64, error) {
	return strconv.ParseUint(strings.TrimPrefix(hex, "0x"), 16, 64)
}
func Hex(n uint64) string { return fmt.Sprintf("0x%x", n) }
func (e *Engine) block(ctx context.Context, number any) (Block, error) {
	var b *Block
	err := e.Client.Call(ctx, "eth_getBlockByNumber", []any{number, true}, &b)
	if err != nil {
		return Block{}, err
	}
	if b == nil {
		return Block{}, fmt.Errorf("block %v is unavailable", number)
	}
	return *b, nil
}
func (e *Engine) Sync(ctx context.Context) error {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	if e.staged == nil && !e.options.DisableCache {
		cache, err := e.Store.EnableProtocolCache(ctx)
		if err != nil {
			return err
		}
		e.cache = cache
		e.Client.Cache = cache
	}
	var chainID string
	if err := e.Client.Call(ctx, "eth_chainId", []any{}, &chainID); err != nil {
		return err
	}
	id, err := Number(chainID)
	if err != nil || id != e.chain.ID {
		return fmt.Errorf("RPC chain ID mismatch: expected %d got %s", e.chain.ID, chainID)
	}
	latest, err := e.block(ctx, "latest")
	if err != nil {
		return err
	}
	head, err := Number(latest.Number)
	if err != nil {
		return err
	}
	if e.staged != nil {
		if err = e.initializeStaged(ctx, latest); err != nil {
			return err
		}
		if !e.options.DisableCache {
			e.cache, err = e.Store.EnableProtocolCache(ctx)
			if err != nil {
				return err
			}
			e.Client.Cache = e.cache
		}
	}
	if err = e.checkReorg(ctx, head); err != nil {
		return err
	}
	start := ^uint64(0)
	hasSource := false
	for _, c := range e.Config.Contracts {
		start = min(start, c.StartBlock)
		if f, ok := e.factories[c.Name]; ok && f.FromBlock != nil {
			start = min(start, *f.FromBlock)
		}
		hasSource = true
	}
	for _, b := range e.Config.Blocks {
		start = min(start, b.StartBlock)
		hasSource = true
	}
	if !hasSource {
		return fmt.Errorf("no indexing sources configured")
	}
	if start > head {
		if e.staged != nil {
			return e.startRealtime(ctx)
		}
		return nil
	}
	missing, err := e.Store.Missing(ctx, e.chain.ID, e.filter, interval.Interval{int64(start), int64(head)})
	if err != nil {
		return err
	}
	for _, gap := range missing {
		for n := uint64(gap[0]); n <= uint64(gap[1]); n++ {
			if err = ctx.Err(); err != nil {
				return err
			}
			b, err := e.block(ctx, Hex(n))
			if err != nil {
				return err
			}
			if e.staged != nil && n > e.staged.historyEnd {
				if err = e.startRealtime(ctx); err != nil {
					return err
				}
			}
			if err = e.process(ctx, b); err != nil {
				return err
			}
		}
	}
	if e.staged != nil {
		if err = e.startRealtime(ctx); err != nil {
			return err
		}
		return e.finalizeStaged(ctx, latest)
	}
	return nil
}
func (e *Engine) checkReorg(ctx context.Context, head uint64) error {
	// Find the most recent completed block, including when the canonical tip shrinks.
	progress, err := e.Store.Progress(ctx)
	if err != nil {
		return err
	}
	cursor := progress[strconv.FormatUint(e.chain.ID, 10)]
	if e.staged != nil {
		cursor, err = e.staged.db.Actions().RuntimeCheckpoint(ctx, e.chain.ID)
		if err != nil {
			return err
		}
	}
	if cursor == "" {
		return nil
	}
	cp, err := checkpoint.Decode(cursor)
	if err != nil {
		return err
	}
	n := min(cp.BlockNumber, head)
	stored, err := e.Store.SyncedBlock(ctx, e.chain.ID, n)
	if err != nil {
		return err
	}
	if stored == nil {
		return fmt.Errorf("indexed progress is missing its persisted block header; shadow rebuild required")
	}
	canonical, err := e.block(ctx, Hex(n))
	if err != nil {
		return err
	}
	if canonical.Hash == stored.Hash && head >= cp.BlockNumber {
		return nil
	}
	for {
		stored, err = e.Store.SyncedBlock(ctx, e.chain.ID, n)
		if err != nil {
			return err
		}
		canonical, err = e.block(ctx, Hex(n))
		if err != nil {
			return err
		}
		if stored != nil && stored.Hash == canonical.Hash {
			timestamp, err := Number(canonical.Timestamp)
			if err != nil {
				return err
			}
			target := checkpoint.Block(timestamp, e.chain.ID, n, true)
			if e.staged != nil {
				return e.staged.db.Actions().RevertRuntime(ctx, target)
			}
			return e.Store.RevertAndRewind(ctx, target)
		}
		if n == 0 {
			return fmt.Errorf("no common persisted ancestor; shadow rebuild required")
		}
		n--
	}
}

type pending struct {
	cursor checkpoint.Checkpoint
	id     string
	event  Event
}

func (e *Engine) process(ctx context.Context, b Block) error {
	number, err := Number(b.Number)
	if err != nil {
		return err
	}
	timestamp, err := Number(b.Timestamp)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if err = e.Store.SaveBlock(ctx, store.SyncBlock{ChainID: e.chain.ID, Number: number, Hash: b.Hash, ParentHash: b.ParentHash, Timestamp: timestamp, Payload: payload}); err != nil {
		return err
	}
	base := checkpoint.Block(timestamp, e.chain.ID, number, false)
	if err = e.discoverFactories(ctx, b, number); err != nil {
		return err
	}
	events := []pending{}
	receipts := map[string]map[string]any{}
	for _, contract := range e.Config.Contracts {
		if number < contract.StartBlock || contract.EndBlock != nil && number > *contract.EndBlock {
			continue
		}
		if e.staged == nil && number == contract.StartBlock {
			events = append(events, pending{cursor: base, id: fmt.Sprintf("setup:%d:%s:%s", e.chain.ID, contract.Name, b.Hash), event: Event{Name: contract.Name + ":setup", Block: b}})
		}
		filter := map[string]any{"blockHash": b.Hash}
		if factory, ok := e.factories[contract.Name]; ok {
			children, e2 := e.Store.FactoryChildren(ctx, factory.ID, e.chain.ID, number)
			if e2 != nil {
				return e2
			}
			if len(children) == 0 {
				continue
			}
			addresses := make([]string, 0, len(children))
			for address := range children {
				addresses = append(addresses, address)
			}
			sort.Strings(addresses)
			filter["address"] = addresses
		} else if len(contract.Address) > 0 {
			filter["address"] = contract.Address
		}
		var logs []Log
		if err = e.Client.CallCached(ctx, "eth_getLogs", []any{filter}, &logs); err != nil {
			return err
		}
		if err = e.cacheLogs(ctx, logs); err != nil {
			return err
		}
		a := e.abis[contract.Name]
		for _, log := range logs {
			if log.Removed || len(log.Topics) == 0 {
				continue
			}
			for name, item := range a.Events {
				if !strings.EqualFold(item.ID.Hex(), log.Topics[0]) {
					continue
				}
				eventName := contract.Name + ":" + name
				if e.Handlers[eventName] == nil {
					continue
				}
				args, err := a.DecodeEvent(name, log.Topics, log.Data)
				if err != nil {
					return fmt.Errorf("%s decoding: %w", eventName, err)
				}
				txIndex, err := Number(log.TransactionIndex)
				if err != nil {
					return err
				}
				index, err := Number(log.LogIndex)
				if err != nil {
					return err
				}
				cursor := base
				cursor.TransactionIndex = txIndex
				cursor.EventType = checkpoint.Logs
				cursor.EventIndex = index
				var transaction map[string]any
				for _, tx := range b.Transactions {
					if tx["hash"] == log.TransactionHash {
						transaction = tx
						break
					}
				}
				var receipt map[string]any
				if contract.IncludeTransactionReceipts {
					receipt = receipts[log.TransactionHash]
					if receipt == nil {
						if err = e.Client.Call(ctx, "eth_getTransactionReceipt", []any{log.TransactionHash}, &receipt); err != nil {
							return err
						}
						receipts[log.TransactionHash] = receipt
						if e.cache != nil {
							if err = e.cache.Insert(ctx, "transaction_receipts", e.chain.ID, []map[string]any{receipt}); err != nil {
								return err
							}
						}
					}
				}
				copyLog := log
				events = append(events, pending{cursor: cursor, id: fmt.Sprintf("log:%d:%s:%s:%s", e.chain.ID, b.Hash, contract.Name, log.LogIndex), event: Event{Name: eventName, Args: args, Block: b, Log: &copyLog, Transaction: transaction, Receipt: receipt}})
			}
		}
	}
	for _, source := range e.Config.Blocks {
		if number < source.StartBlock || source.EndBlock != nil && number > *source.EndBlock || (number-source.StartBlock)%source.Interval != 0 {
			continue
		}
		cursor := base
		cursor.TransactionIndex = checkpoint.Max.TransactionIndex
		cursor.EventType = checkpoint.Blocks
		cursor.EventIndex = 0
		events = append(events, pending{cursor: cursor, id: fmt.Sprintf("block:%d:%s:%s", e.chain.ID, b.Hash, source.Name), event: Event{Name: source.Name + ":block", Block: b}})
	}
	sort.SliceStable(events, func(i, j int) bool {
		return checkpoint.MustEncode(events[i].cursor) < checkpoint.MustEncode(events[j].cursor)
	})
	if e.staged != nil {
		ordered := make([]OrderedEvent, len(events))
		for i, event := range events {
			ordered[i] = OrderedEvent{ChainID: e.chain.ID, Checkpoint: checkpoint.MustEncode(event.cursor), Event: event.event}
		}
		groups, splitErr := SplitEvents(ordered)
		if splitErr != nil {
			return splitErr
		}
		target := checkpoint.Block(timestamp, e.chain.ID, number, true)
		if len(groups) > 0 {
			if len(groups) != 1 {
				return fmt.Errorf("single block dispatch produced multiple hash groups")
			}
			target, err = checkpoint.Decode(groups[0].Checkpoint)
			if err != nil {
				return err
			}
		}
		_, err = e.staged.db.Actions().ProcessRuntimeBlock(ctx, target, !e.staged.realtime.Load(), func(w *store.Writer) error {
			client := e.eventClient(w)
			for _, event := range events {
				handler := e.Handlers[event.event.Name]
				if handler == nil {
					continue
				}
				if err := handler(ctx, &Context{Event: event.event, DB: w, Client: client, Chain: e.chain}); err != nil {
					return fmt.Errorf("%s: %w", event.event.Name, err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		return e.Store.CompleteBlock(ctx, store.SyncBlock{ChainID: e.chain.ID, Number: number, Hash: b.Hash, ParentHash: b.ParentHash, Timestamp: timestamp, Payload: payload}, e.filter)
	}
	for _, event := range events {
		handler := e.Handlers[event.event.Name]
		if handler == nil {
			continue
		}
		if err = e.Store.Apply(ctx, store.Event{ID: event.id, Checkpoint: event.cursor, Name: event.event.Name}, func(w *store.Writer) error {
			return handler(ctx, &Context{Event: event.event, DB: w, Client: e.eventClient(w), Chain: e.chain})
		}); err != nil {
			return fmt.Errorf("%s: %w", event.event.Name, err)
		}
	}
	// Every completed block gets durable progress even if it has no matching events.
	if err = e.Store.Apply(ctx, store.Event{ID: fmt.Sprintf("complete:%d:%s", e.chain.ID, b.Hash), Checkpoint: checkpoint.Block(timestamp, e.chain.ID, number, true)}, func(*store.Writer) error { return nil }); err != nil {
		return err
	}
	return e.Store.CompleteBlock(ctx, store.SyncBlock{ChainID: e.chain.ID, Number: number, Hash: b.Hash, ParentHash: b.ParentHash, Timestamp: timestamp, Payload: payload}, e.filter)
}
func (e *Engine) Run(ctx context.Context) error {
	delay := time.Duration(e.chain.PollingIntervalMS) * time.Millisecond
	if delay <= 0 {
		delay = time.Second
	}
	ticker := time.NewTicker(delay)
	defer ticker.Stop()
	for {
		if err := e.Sync(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// FinalizedBlock delegates to the source-equivalent timestamp search.
func (e *Engine) FinalizedBlock(ctx context.Context, latest Block) (Block, error) {
	light := &finality.Block{Hash: latest.Hash, ParentHash: latest.ParentHash, Number: latest.Number, Timestamp: latest.Timestamp}
	found, err := finality.GetFinalizedBlock(ctx, finality.Chain{ReorgWindow: e.chain.Window(), BlockTimeMS: e.chain.BlockTimeMS}, func(ctx context.Context, r finality.Request) (*finality.Block, error) {
		var b *finality.Block
		// The search requests headers (false), and retries null results at the RPC boundary.
		for attempt := 0; ; attempt++ {
			if err := e.Client.Call(ctx, "eth_getBlockByNumber", []any{Hex(r.Number), r.IncludeTransactions}, &b); err != nil {
				return nil, err
			}
			if b != nil {
				return b, nil
			}
			if !r.RetryNullBlockRequest || attempt >= e.Client.Retries {
				return nil, fmt.Errorf("block %d is unavailable", r.Number)
			}
			timer := time.NewTimer(25 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}, light, nil)
	if err != nil {
		return Block{}, err
	}
	if found.Number == latest.Number && found.Hash == latest.Hash {
		return latest, nil
	}
	return Block{Hash: found.Hash, ParentHash: found.ParentHash, Number: found.Number, Timestamp: found.Timestamp}, nil
}
