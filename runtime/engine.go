// Package runtime provides the first complete native Go single-chain indexing path.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	protocol "github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/factory"
	selection "github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/finality"
	"github.com/ysh0566/ponder-go/interval"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
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
	LogsBloom    string           `json:"logsBloom,omitempty"`
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
	// ID is the source checkpoint for an indexed chain event.
	ID          string
	Name        string
	Args        any
	Block       Block
	Log         *Log
	Transaction map[string]any
	Receipt     map[string]any
	SetupBlock  uint64
	Trace       map[string]any
	Transfer    map[string]any
	Normalized  *protocol.RawEvent
	Result      any
}
type Context struct {
	Event  Event
	DB     *store.Writer
	Client *rpc.Client
	Chain  config.Chain
}
type Handler func(context.Context, *Context) error
type Engine struct {
	Config        config.Config
	Store         *store.Store
	Handlers      map[string]Handler
	Client        *rpc.Client
	abis          map[string]abi.ABI
	factories     map[string]factory.LogFactory
	blooms        bloomPlan
	filter        string
	chain         config.Chain
	options       Options
	cache         *store.ProtocolCache
	syncMu        sync.Mutex
	endBlock      *uint64
	finalizedHash string
	staged        *stagedState
}
type Options struct {
	DisableCache bool
	Logger       protocol.DebugLogger
}

func New(c config.Config, s *store.Store, handlers map[string]Handler) (*Engine, error) {
	return NewWithOptions(c, s, handlers, Options{})
}
func NewWithOptions(c config.Config, s *store.Store, handlers map[string]Handler, options Options) (*Engine, error) {
	if c.SourcePlan != nil && !c.SourcePlan.CacheRPCRequests {
		options.DisableCache = true
	}
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
		return nil, fmt.Errorf("single-chain engine requires one chain; use NewIndexer to coordinate multiple chains")
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
	for _, account := range c.Accounts {
		if account.Factory == nil {
			continue
		}
		cfg := *account.Factory
		cfg.ChainID, cfg.SourceID = engine.chain.ID, account.Name
		if cfg.ToBlock == nil {
			cfg.ToBlock = account.EndBlock
		}
		if cfg.FromBlock == nil {
			from := account.StartBlock
			cfg.FromBlock = &from
		}
		f, err := factory.Build(cfg)
		if err != nil {
			return nil, err
		}
		engine.factories[account.Name] = f
	}
	if c.SourcePlan != nil {
		for name, factory := range c.SourcePlan.Factories {
			engine.factories[name] = factory
		}
	}
	// A notification endpoint changes when the worker wakes, not its indexed ranges.
	identity := c
	chain := engine.chain
	chain.WS = ""
	identity.Chains = []config.Chain{chain}
	encoded, _ := json.Marshal(identity)
	hash := sha256.Sum256(encoded)
	engine.filter = fmt.Sprintf("runtime:%x", hash)
	engine.Client = rpc.New(engine.chain.RPC...)
	engine.Client.ChainID = engine.chain.ID
	if c.SourcePlan != nil && c.SourcePlan.Client != nil {
		engine.Client = c.SourcePlan.Client
	}
	if err := engine.compileBloomPlan(); err != nil {
		return nil, err
	}
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
		return Block{}, fmt.Errorf("block %v is unavailable: %w", number, rpc.ErrBlockNotFound)
	}
	return *b, nil
}
func (e *Engine) Sync(ctx context.Context) error {
	_, err := e.syncToHead(ctx)
	return err
}

func (e *Engine) syncToHead(ctx context.Context) (bool, error) {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	latest, err := e.acquireHead(ctx)
	if err != nil {
		return false, err
	}
	head, err := Number(latest.Number)
	if err != nil {
		return false, err
	}
	if e.staged != nil {
		if err = e.initializeStaged(ctx, latest); err != nil {
			return false, err
		}
		if !e.options.DisableCache {
			e.cache, err = e.Store.EnableProtocolCache(ctx)
			if err != nil {
				return false, err
			}
			e.Client.Cache = e.cache
		}
	}
	if err = e.checkReorg(ctx, head); err != nil {
		return false, err
	}
	start, err := e.firstSourceBlock()
	if err != nil {
		return false, err
	}
	if e.endBlock != nil {
		head = min(head, *e.endBlock)
	}
	missing, err := e.missingBlocks(ctx, start, head)
	if err != nil {
		return false, err
	}
	if start > head {
		if e.staged != nil {
			return false, e.startRealtime(ctx)
		}
		return false, nil
	}
	for _, gap := range missing {
		var parentHash string
		if e.staged == nil && uint64(gap[0]) > start {
			parent, err := e.Store.SyncedBlock(ctx, e.chain.ID, uint64(gap[0])-1)
			if err != nil {
				return false, err
			}
			if parent == nil {
				return false, fmt.Errorf("missing indexed parent of block %d", gap[0])
			}
			parentHash = parent.Hash
		}
		for n := uint64(gap[0]); n <= uint64(gap[1]); n++ {
			if err = ctx.Err(); err != nil {
				return false, err
			}
			b, err := e.block(ctx, Hex(n))
			if err != nil {
				return false, err
			}
			if e.staged == nil {
				number, err := Number(b.Number)
				if err != nil || number != n || b.Hash == "" {
					return false, fmt.Errorf("invalid RPC header at height %d", n)
				}
				if parentHash != "" && parentHash != b.ParentHash {
					return false, errIndexingFork
				}
				parentHash = b.Hash
			}
			if e.staged != nil && n > e.staged.historyEnd {
				if err = e.startRealtime(ctx); err != nil {
					return false, err
				}
			}
			if err = e.process(ctx, b); err != nil {
				return false, err
			}
		}
	}
	if e.staged != nil {
		if err = e.startRealtime(ctx); err != nil {
			return false, err
		}
		return false, e.finalizeStaged(ctx, latest)
	}
	return e.finalizeIndexed(ctx, latest, head)
}

var errIndexingFork = errors.New("chain changed during indexing")

func (e *Engine) finalizeIndexed(ctx context.Context, latest Block, through uint64) (bool, error) {
	now, err := Number(latest.Timestamp)
	if err != nil {
		return false, err
	}
	window := uint64(e.chain.Window())
	var cutoff uint64
	if now >= window {
		cutoff = now - window
	}
	candidate, err := e.Store.IndexedFinalityCandidate(ctx, e.chain.ID, through, cutoff)
	if err != nil || candidate == nil {
		return false, err
	}
	done := e.endBlock != nil && candidate.Number == *e.endBlock
	if candidate.Hash == e.finalizedHash && e.finalizedHash != "" {
		return done, nil
	}
	canonical, err := e.block(ctx, Hex(candidate.Number))
	if err != nil {
		return false, err
	}
	if candidate.Hash != canonical.Hash {
		return false, errIndexingFork
	}
	if latest.Number != canonical.Number {
		canonical, err = e.block(ctx, latest.Number)
		if err != nil {
			return false, err
		}
	}
	if canonical.Hash != latest.Hash {
		return false, errIndexingFork
	}
	if err := e.Store.FinalizeIndexedBlock(ctx, *candidate); err != nil {
		if errors.Is(err, store.ErrIndexedBlockChanged) {
			return false, errIndexingFork
		}
		return false, err
	}
	e.finalizedHash = candidate.Hash
	return done, nil
}
func (e *Engine) acquireHead(ctx context.Context) (Block, error) {
	if m := devMonitorFrom(ctx); m != nil {
		m.register(e)
	}
	if e.staged == nil && !e.options.DisableCache {
		cache, err := e.Store.EnableProtocolCache(ctx)
		if err != nil {
			return Block{}, err
		}
		e.cache = cache
		e.Client.Cache = cache
	}
	var chainID string
	if err := e.Client.Call(ctx, "eth_chainId", []any{}, &chainID); err != nil {
		return Block{}, err
	}
	id, err := Number(chainID)
	if err != nil || id != e.chain.ID {
		return Block{}, fmt.Errorf("RPC chain ID mismatch: expected %d got %s", e.chain.ID, chainID)
	}
	return e.block(ctx, "latest")
}

func (e *Engine) missingBlocks(ctx context.Context, start, head uint64) ([]interval.Interval, error) {
	var missing []interval.Interval
	var total uint64
	if start <= head {
		var err error
		missing, err = e.Store.Missing(ctx, e.chain.ID, e.filter, interval.Interval{int64(start), int64(head)})
		if err != nil {
			return nil, err
		}
		total = head - start + 1
	}
	if m := devMonitorFrom(ctx); m != nil {
		m.setRange(e.chain.ID, total, total-uint64(interval.Sum(missing)))
	}
	return missing, nil
}

func (e *Engine) firstSourceBlock() (uint64, error) {
	start := ^uint64(0)
	hasSource := false
	for _, c := range e.Config.Contracts {
		start = min(start, c.StartBlock)
		if f, ok := e.factories[c.Name]; ok && f.FromBlock != nil {
			start = min(start, *f.FromBlock)
		}
		hasSource = true
	}
	for _, account := range e.Config.Accounts {
		start = min(start, account.StartBlock)
		if f, ok := e.factories[account.Name]; ok && f.FromBlock != nil {
			start = min(start, *f.FromBlock)
		}
		hasSource = true
	}
	for _, b := range e.Config.Blocks {
		start = min(start, b.StartBlock)
		hasSource = true
	}
	if !hasSource {
		return 0, fmt.Errorf("no indexing sources configured")
	}
	return start, nil
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
	var height uint64
	if cursor != "" {
		cp, err := checkpoint.Decode(cursor)
		if err != nil {
			return err
		}
		height = cp.BlockNumber
	}
	if e.staged == nil {
		saved, ok, err := e.Store.LatestSyncedHeight(ctx, e.chain.ID)
		if err != nil {
			return err
		}
		if cursor != "" && (!ok || saved < height) {
			return fmt.Errorf("indexed progress is missing its persisted block header; shadow rebuild required")
		}
		if !ok {
			return nil
		}
		height = saved
	} else if cursor == "" {
		return nil
	}
	n := min(height, head)
	stored, err := e.Store.SyncedBlock(ctx, e.chain.ID, n)
	if err != nil {
		return err
	}
	if stored == nil && n == height {
		return fmt.Errorf("indexed progress is missing its persisted block header; shadow rebuild required")
	}
	canonical, err := e.block(ctx, Hex(n))
	if err != nil {
		return err
	}
	if stored != nil && canonical.Hash == stored.Hash && head >= height {
		return nil
	}
	for {
		matches := stored != nil && stored.Hash == canonical.Hash
		if stored == nil {
			// The parent of the earliest indexed block is still proven by the
			// child's header when a reorg replaces the first included block.
			child, err := e.Store.SyncedBlock(ctx, e.chain.ID, n+1)
			if err != nil {
				return err
			}
			matches = child != nil && child.ParentHash == canonical.Hash
		}
		if matches {
			timestamp, err := Number(canonical.Timestamp)
			if err != nil {
				return err
			}
			target := checkpoint.Block(timestamp, e.chain.ID, n, true)
			if e.staged != nil {
				err = e.staged.db.Actions().RevertRuntime(ctx, target)
			} else {
				err = e.Store.RevertAndRewind(ctx, target)
			}
			if err == nil {
				if m := devMonitorFrom(ctx); m != nil {
					m.rewind(e.chain.ID, e.Store.Ordering != schema.Isolated)
				}
			}
			return err
		}
		if n == 0 {
			return fmt.Errorf("no common persisted ancestor; shadow rebuild required")
		}
		n--
		stored, err = e.Store.SyncedBlock(ctx, e.chain.ID, n)
		if err != nil {
			return err
		}
		canonical, err = e.block(ctx, Hex(n))
		if err != nil {
			return err
		}
	}
}

type pending struct {
	cursor   checkpoint.Checkpoint
	id       string
	event    Event
	callback protocol.Object
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
	blockBloom := e.blockBloom(b)
	if err = e.discoverFactories(ctx, b, number, blockBloom); err != nil {
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
		if !e.contractLogsMayMatch(blockBloom, contract.Name) {
			continue
		}
		callbacks, callbackErr := e.logCallbacks(contract)
		if callbackErr != nil {
			return callbackErr
		}
		compiled := nativeContractFilter(contract, "")
		requestAddress := compiled["address"]
		childEntries := []selection.ChildAddress{}
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
				childEntries = append(childEntries, selection.ChildAddress{Address: address, Block: children[address]})
			}
			sort.Strings(addresses)
			requestAddress = addresses
			compiled["address"] = nativeFactory(factory)
		}
		query, needed := e.planSourceLogs(callbacks, requestAddress, b.Hash)
		if !needed {
			continue
		}
		var logs []Log
		if err = e.Client.CallCached(ctx, "eth_getLogs", []any{query}, &logs); err != nil {
			return err
		}
		for _, log := range logs {
			if log.BlockHash != "" && !strings.EqualFold(log.BlockHash, b.Hash) {
				return fmt.Errorf("RPC log belongs to a different block")
			}
		}
		if err = e.cacheLogs(ctx, logs); err != nil {
			return err
		}
		for _, log := range logs {
			if log.Removed || len(log.Topics) == 0 {
				continue
			}
			for _, callback := range callbacks {
				compiled = callback.filter
				if factory, ok := e.factories[contract.Name]; ok {
					compiled["address"] = nativeFactory(factory)
				}
				if !selection.IsLogFilterMatched(compiled, nativeLog(log, number)) {
					continue
				}
				if _, dynamic := e.factories[contract.Name]; dynamic && !selection.IsAddressMatched(log.Address, float64(number), childEntries) {
					continue
				}
				eventName := callback.name
				if e.Handlers[eventName] == nil {
					continue
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
				raw, err := normalizedContractLog(b, copyLog, transaction, receipt, cursor)
				if err != nil {
					return err
				}
				events = append(events, pending{cursor: cursor, id: fmt.Sprintf("log:%d:%s:%s:%s", e.chain.ID, b.Hash, contract.Name, log.LogIndex), event: Event{Name: eventName, Block: b, Log: &copyLog, Transaction: transaction, Receipt: receipt, Normalized: raw}, callback: callback.callback})
			}
		}
	}
	accountEvents, accountErr := e.accountEvents(ctx, b, number)
	if accountErr != nil {
		return accountErr
	}
	events = append(events, accountEvents...)
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
	events, err = e.decodePending(events)
	if err != nil {
		return err
	}
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
				if err := invokeHandler(ctx, handler, &Context{Event: event.event, DB: w, Client: client, Chain: e.chain}); err != nil {
					return fmt.Errorf("%s: %w", event.event.Name, err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		return e.completeBlock(ctx, store.SyncBlock{ChainID: e.chain.ID, Number: number, Hash: b.Hash, ParentHash: b.ParentHash, Timestamp: timestamp, Payload: payload})
	}
	for _, event := range events {
		handler := e.Handlers[event.event.Name]
		if handler == nil {
			continue
		}
		if err = e.Store.Apply(ctx, store.Event{ID: event.id, Checkpoint: event.cursor, Name: event.event.Name}, func(w *store.Writer) error {
			return invokeHandler(ctx, handler, &Context{Event: event.event, DB: w, Client: e.eventClient(w), Chain: e.chain})
		}); err != nil {
			return fmt.Errorf("%s: %w", event.event.Name, err)
		}
	}
	// Every completed block gets durable progress even if it has no matching events.
	block := store.SyncBlock{ChainID: e.chain.ID, Number: number, Hash: b.Hash, ParentHash: b.ParentHash, Timestamp: timestamp, Payload: payload}
	if err = e.Store.RecordBlockCompletion(ctx, block); err != nil {
		return err
	}
	return e.completeBlock(ctx, block)
}
func (e *Engine) completeBlock(ctx context.Context, block store.SyncBlock) error {
	if err := e.Store.CompleteBlock(ctx, block, e.filter); err != nil {
		return err
	}
	if m := devMonitorFrom(ctx); m != nil {
		m.completeBlock(e.chain.ID)
	}
	return nil
}

func (e *Engine) Run(ctx context.Context) error { return e.RunIndexing(ctx, func(bool) {}) }

// RunIndexing resumes from committed progress after transient RPC outages.
func (e *Engine) RunIndexing(ctx context.Context, ready func(bool)) error {
	return pollIndexer(ctx, e.pollingInterval(), ready, e.syncToHead, e.chain.WS)
}

func (e *Engine) pollingInterval() time.Duration {
	interval := time.Duration(e.chain.PollingIntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = time.Second
	}
	return interval
}

func pollIndexer(ctx context.Context, interval time.Duration, ready func(bool), sync func(context.Context) (bool, error), endpoints ...string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	heads := headNotifications(ctx, endpoints...)
	retry := min(interval, time.Second)
	for {
		done, err := sync(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := interval
		notify := heads
		if err != nil {
			notify = nil
			ready(false)
			if !rpc.IsTransient(err) && !errors.Is(err, errIndexingFork) {
				return err
			}
			delay = retry
			retry = min(retry*2, 30*time.Second)
		} else {
			ready(true)
			if done {
				return nil
			}
			retry = min(interval, time.Second)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-notify:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// FinalizedBlock delegates to the source-equivalent timestamp search.
func (e *Engine) FinalizedBlock(ctx context.Context, latest Block) (Block, error) {
	light := &finality.Block{Hash: latest.Hash, ParentHash: latest.ParentHash, Number: latest.Number, Timestamp: latest.Timestamp}
	found, err := finality.GetFinalizedBlock(ctx, finality.Chain{ReorgWindow: e.chain.Window(), BlockTimeMS: e.chain.BlockTimeMS}, func(ctx context.Context, r finality.Request) (*finality.Block, error) {
		var b *finality.Block
		options := rpc.RequestOptions{RetryNullBlockRequest: r.RetryNullBlockRequest}
		err := e.Client.CallWithOptions(ctx, "eth_getBlockByNumber", []any{Hex(r.Number), r.IncludeTransactions}, &b, options)
		if err != nil && !errors.Is(err, rpc.ErrBlockNotFound) {
			return nil, err
		}
		if b == nil {
			return nil, fmt.Errorf("block %d is unavailable: %w", r.Number, rpc.ErrBlockNotFound)
		}
		return b, nil
	}, light, nil)
	if err != nil {
		return Block{}, err
	}
	if found.Number == latest.Number && found.Hash == latest.Hash {
		return latest, nil
	}
	return Block{Hash: found.Hash, ParentHash: found.ParentHash, Number: found.Number, Timestamp: found.Timestamp}, nil
}
