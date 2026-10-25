package runtime

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

type SourceHistoricalSync struct {
	base           *SourceInMemoryHistoricalSync
	chain          uint64
	logsMu         sync.Mutex
	estimatedRange uint64
	confirmedRange *uint64
}

// Historical log chunk learning belongs to this synchronizer. Receipt fallback
// remains on the supplied RPC actions, shared with its memory synchronizer.
func NewSourceHistoricalSync(chain uint64, requester rpc.Requester, actions *rpc.Actions, children *SourceHistoricalChildren, options SourceHistoricalOptions) (*SourceHistoricalSync, error) {
	base, err := NewSourceInMemoryHistoricalSync(requester, actions, children, options)
	if err != nil {
		return nil, err
	}
	return &SourceHistoricalSync{base: base, chain: chain, estimatedRange: 500}, nil
}

func sourceHistoricalAll(tasks []func() error) error {
	var wg sync.WaitGroup
	var first error
	var once sync.Once
	for _, task := range tasks {
		wg.Add(1)
		go func(task func() error) {
			defer wg.Done()
			if err := task(); err != nil {
				once.Do(func() { first = err })
			}
		}(task)
	}
	wg.Wait()
	return first
}

func (s *SourceHistoricalSync) syncLogsDynamic(ctx context.Context, input filter.Object) ([]filter.Object, error) {
	from, err := sourceHistoricalNumber(input["fromBlock"])
	if err != nil {
		return nil, err
	}
	to, err := sourceHistoricalNumber(input["toBlock"])
	if err != nil {
		return nil, err
	}
	s.logsMu.Lock()
	size := s.estimatedRange
	if s.confirmedRange != nil {
		size = *s.confirmedRange
	}
	if s.base.options.EthGetLogsBlockRange != nil {
		size = *s.base.options.EthGetLogsBlockRange
	}
	s.logsMu.Unlock()
	if size == 0 {
		return nil, fmt.Errorf("historical eth_getLogs block range must be positive")
	}
	chunks := []store.SourceInterval{}
	for cursor := from; cursor <= to; {
		end := to
		if size-1 < to-cursor {
			end = cursor + size - 1
		}
		chunks = append(chunks, store.SourceInterval{float64(cursor), float64(end)})
		if end == to {
			break
		}
		cursor = end + 1
	}
	results := make([][]filter.Object, len(chunks))
	tasks := []func() error{}
	for i, span := range chunks {
		tasks = append(tasks, func() error {
			query := filter.Object{"address": sourceHistoricalProperty(input, "address"), "topics": sourceHistoricalProperty(input, "topics"), "fromBlock": Hex(uint64(span[0])), "toBlock": Hex(uint64(span[1]))}
			query = filter.RPCValue(query).(filter.Object)
			logs, err := s.base.actions.GetLogs(ctx, query)
			if err == nil {
				results[i] = logs
				return nil
			}
			if s.base.options.EthGetLogsBlockRange != nil {
				return err
			}
			plan, planErr := rpc.GetLogsRetryHelper(query, err)
			if planErr != nil {
				return planErr
			}
			if !plan.ShouldRetry {
				return err
			}
			start, rangeErr := sourceHistoricalNumber(plan.Ranges[0].FromBlock)
			if rangeErr != nil {
				return rangeErr
			}
			finish, rangeErr := sourceHistoricalNumber(plan.Ranges[0].ToBlock)
			if rangeErr != nil {
				return rangeErr
			}
			size := finish - start
			// The pinned persistent implementation uses the endpoint difference here.
			// The RPC-owned memory pagination helper uses its distinct inclusive size.
			s.logsMu.Lock()
			s.estimatedRange = size
			s.confirmedRange = nil
			if plan.IsSuggestedRange {
				hint := size
				s.confirmedRange = &hint
			}
			s.logsMu.Unlock()
			if s.base.options.Logger != nil {
				s.base.options.Logger.Debug(map[string]any{"msg": "Updated eth_getLogs range", "chain_id": s.chain, "range": size})
			}
			results[i], err = s.syncLogsDynamic(ctx, query)
			return err
		})
	}
	if err = sourceHistoricalAll(tasks); err != nil {
		return nil, err
	}
	s.logsMu.Lock()
	if s.confirmedRange == nil {
		s.estimatedRange = uint64(math.Floor(float64(s.estimatedRange)*1.05 + 0.5))
	}
	s.logsMu.Unlock()
	logs := []filter.Object{}
	for _, chunk := range results {
		logs = append(logs, chunk...)
	}
	return logs, nil
}

type sourceHistoricalFragmentChildren struct {
	order    []string
	children map[string][]store.SourceChildAddress
	indices  map[string]map[string]int
}

func newSourceHistoricalFragmentChildren() *sourceHistoricalFragmentChildren {
	return &sourceHistoricalFragmentChildren{order: []string{}, children: map[string][]store.SourceChildAddress{}, indices: map[string]map[string]int{}}
}
func (c *sourceHistoricalFragmentChildren) upsert(fragment, address string, number uint64) {
	if _, ok := c.children[fragment]; !ok {
		c.order = append(c.order, fragment)
		c.children[fragment] = []store.SourceChildAddress{}
		c.indices[fragment] = map[string]int{}
	}
	if index, ok := c.indices[fragment][address]; ok {
		if c.children[fragment][index].BlockNumber > number {
			c.children[fragment][index].BlockNumber = number
		}
		return
	}
	c.indices[fragment][address] = len(c.children[fragment])
	c.children[fragment] = append(c.children[fragment], store.SourceChildAddress{Address: address, BlockNumber: number})
}
func (c *sourceHistoricalFragmentChildren) entries() []store.SourceFactoryChildren {
	result := []store.SourceFactoryChildren{}
	for _, id := range c.order {
		result = append(result, store.SourceFactoryChildren{FragmentID: id, Children: c.children[id]})
	}
	return result
}

func (s *SourceHistoricalSync) syncAddressFactory(ctx context.Context, factory filter.Object, span store.SourceInterval) ([]store.SourceFactoryChildren, error) {
	id, _ := factory["id"].(string)
	if _, ok := s.base.children.Entries(id); !ok {
		return nil, fmt.Errorf("source factory child map not initialized: %s", id)
	}
	query := filter.Object{"address": sourceHistoricalProperty(factory, "address"), "topics": []any{factory["eventSelector"]}, "fromBlock": Hex(uint64(span[0])), "toBlock": Hex(uint64(span[1]))}
	logs, err := s.syncLogsDynamic(ctx, query)
	if err != nil {
		return nil, err
	}
	children := newSourceHistoricalFragmentChildren()
	failures, successes := 0, 0
	for _, log := range logs {
		if !filter.IsLogFactoryMatched(factory, log) {
			continue
		}
		address, err := filter.GetSourceChildAddress(log, factory)
		if err != nil {
			if !filter.IsUndefined(sourceHistoricalProperty(factory, "address")) {
				return nil, err
			}
			failures++
			if failures == 1 && s.base.options.Logger != nil {
				s.base.options.Logger.Debug(map[string]any{"msg": "Failed to extract child address from log matched by factory using the provided ABI item", "factory": factory["sourceId"], "block_number": log["blockNumber"], "log_index": log["logIndex"], "data": log["data"], "topics": log["topics"]})
			}
			continue
		}
		successes++
		number, err := sourceHistoricalNumber(log["blockNumber"])
		if err != nil {
			return nil, err
		}
		parent, _ := log["address"].(string)
		children.upsert(filter.FactoryFragmentID(factory, parent), address, number)
		s.base.children.Upsert(id, filter.ChildAddress{Address: address, Block: number})
	}
	if failures > 0 && s.base.options.Logger != nil {
		s.base.options.Logger.Debug(map[string]any{"msg": "Logs matched by factory contained child addresses that could not be extracted", "failure_count": failures, "success_count": successes})
	}
	return children.entries(), nil
}

func (s *SourceHistoricalSync) SyncBlockRangeData(ctx context.Context, span store.SourceInterval, required []store.SourceFilterInterval, factories []store.SourceFactoryInterval, database *store.SourceSyncStore) ([]filter.Object, error) {
	if database == nil {
		return nil, fmt.Errorf("source historical sync store required")
	}
	if _, _, err := sourceHistoricalBounds(span); err != nil {
		return nil, err
	}
	order := []string{}
	merged := map[string]store.SourceFactoryInterval{}
	for _, item := range factories {
		if _, _, err := sourceHistoricalBounds(item.Interval); err != nil {
			return nil, err
		}
		id, _ := item.Factory["id"].(string)
		if previous, ok := merged[id]; ok {
			previous.Interval = store.SourceInterval{math.Min(previous.Interval[0], item.Interval[0]), math.Max(previous.Interval[1], item.Interval[1])}
			merged[id] = previous
		} else {
			order = append(order, id)
			merged[id] = item
		}
	}
	children := newSourceHistoricalFragmentChildren()
	var childrenMu sync.Mutex
	tasks := []func() error{}
	for _, id := range order {
		item := merged[id]
		tasks = append(tasks, func() error {
			entries, err := s.syncAddressFactory(ctx, item.Factory, item.Interval)
			if err != nil {
				return err
			}
			childrenMu.Lock()
			defer childrenMu.Unlock()
			for _, entry := range entries {
				for _, child := range entry.Children {
					children.upsert(entry.FragmentID, child.Address, child.BlockNumber)
				}
			}
			return nil
		})
	}
	if err := sourceHistoricalAll(tasks); err != nil {
		return nil, err
	}
	intervals := []filter.RequiredInterval{}
	for _, item := range required {
		from, to, err := sourceHistoricalBounds(item.Interval)
		if err != nil {
			return nil, err
		}
		intervals = append(intervals, filter.RequiredInterval{Filter: item.Filter, Interval: [2]int64{int64(from), int64(to)}})
	}
	childMap := map[string][]filter.ChildAddress{}
	for _, item := range s.base.children.Snapshot() {
		childMap[item.ID] = item.Entries
	}
	requests, err := filter.MergeLogFiltersToRequests(intervals, childMap, s.base.options.FactoryAddressCountThreshold)
	if err != nil {
		return nil, err
	}
	logs := []filter.Object{}
	var logsMu sync.Mutex
	tasks = []func() error{}
	for _, request := range requests {
		query := request["params"].([]any)[0].(map[string]any)
		tasks = append(tasks, func() error {
			result, err := s.syncLogsDynamic(ctx, query)
			if err != nil {
				return err
			}
			logsMu.Lock()
			logs = append(logs, result...)
			logsMu.Unlock()
			return nil
		})
	}
	if err = sourceHistoricalAll(tasks); err != nil {
		return nil, err
	}
	deduped := []filter.Object{}
	seen := map[string]bool{}
	for _, log := range logs {
		key := fmt.Sprint(log["blockNumber"]) + "_" + fmt.Sprint(log["logIndex"])
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, log)
		if log["transactionHash"] == "0x"+strings.Repeat("0", 64) && s.base.options.Logger != nil {
			s.base.options.Logger.Warn(map[string]any{"msg": "Detected log with empty transaction hash. This is expected for some chains like ZKsync.", "action": "fetch_block_data", "chain_id": s.chain, "number": log["blockNumber"], "hash": log["blockHash"], "logIndex": log["logIndex"]})
		}
	}
	if err = database.InsertChildAddresses(ctx, s.chain, children.entries()); err != nil {
		return nil, err
	}
	return deduped, nil
}

func (s *SourceHistoricalSync) SyncBlockData(ctx context.Context, span store.SourceInterval, required []store.SourceFilterInterval, logs []filter.Object, database *store.SourceSyncStore) (filter.Object, error) {
	if database == nil {
		return nil, fmt.Errorf("source historical sync store required")
	}
	from, to, err := sourceHistoricalBounds(span)
	if err != nil {
		return nil, err
	}
	if len(required) == 0 {
		return nil, nil
	}
	filters := []filter.Object{}
	for _, item := range required {
		filters = append(filters, item.Filter)
	}
	perBlock := map[uint64][]filter.Object{}
	for _, log := range logs {
		number, err := sourceHistoricalNumber(log["blockNumber"])
		if err != nil {
			return nil, err
		}
		perBlock[number] = append(perBlock[number], log)
	}
	var closest filter.Object
	var closestNumber uint64
	var closestMu sync.Mutex
	options := &rpc.ActionOptions{EthGetLogsBlockRange: s.base.options.EthGetLogsBlockRange, Logger: s.base.options.Logger}
	syncBlock := func(number uint64) error {
		data, err := fetchSourceHistoricalAcquisition(ctx, s.base.requester, s.base.actions, s.base.children, filters, number, perBlock[number], options, false)
		if err != nil {
			return err
		}
		if data.RawBlock == nil {
			return nil
		}
		data.RawBlock["transactions"] = data.RawTransactions
		closestMu.Lock()
		if closest == nil || number > closestNumber {
			closest, closestNumber = data.RawBlock, number
		}
		closestMu.Unlock()
		return sourceHistoricalAll([]func() error{
			func() error { return database.InsertBlocks(ctx, s.chain, []filter.Object{data.RawBlock}) },
			func() error { return database.InsertTransactions(ctx, s.chain, data.RawTransactions) },
			func() error { return database.InsertTransactionReceipts(ctx, s.chain, data.RawTransactionReceipts) },
			func() error { return database.InsertTraces(ctx, s.chain, data.RawTraces) },
			func() error { return database.InsertLogs(ctx, s.chain, data.RawLogs) },
		})
	}
	jobs := make(chan uint64)
	var wg sync.WaitGroup
	var first error
	var once sync.Once
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for number := range jobs {
				if err := syncBlock(number); err != nil {
					once.Do(func() { first = err })
				}
			}
		}()
	}
	for number := from; number <= to; number++ {
		select {
		case jobs <- number:
		case <-ctx.Done():
			once.Do(func() { first = ctx.Err() })
			close(jobs)
			wg.Wait()
			return nil, first
		}
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		return nil, first
	}
	return closest, nil
}
