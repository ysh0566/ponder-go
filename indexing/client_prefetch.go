package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"ponder.local/ponder-go/rpc"
	"strings"
)

// ProfileEntry is a snapshot of a sampled RPC pattern (not database row profiling).
type ProfileEntry struct {
	Key      string
	Recorded *RecordedProfile
	Count    uint64
}
type clientProfile struct {
	entries          map[string]*ProfileEntry
	order, constants []string
}
type prefetchValue struct {
	raw    json.RawMessage
	err    error
	done   chan struct{}
	future bool
}
type predictedCall struct {
	client *Client
	params []any
	score  float64
	key    string
}

// SetEventCounts snapshots the caller-owned counts. Service updates this from its
// real successful-dispatch counters. Explicit clients must update it themselves,
// just as the source caller owns eventCount; SetEvent never increments a count.
func (s *ClientSet) SetEventCounts(counts map[string]uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventCounts = map[string]uint64{}
	for k, v := range counts {
		s.eventCounts[k] = v
	}
}
func (s *ClientSet) SetProfileFieldOrder(order map[string][]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profileFieldOrder = order
}
func (s *ClientSet) Profiles(name string) []ProfileEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ProfileEntry
	if p := s.profiles[name]; p != nil {
		for _, k := range p.order {
			out = append(out, *p.entries[k])
		}
	}
	return out
}
func removeProfileKey(keys []string, key string) []string {
	for i, k := range keys {
		if k == key {
			return append(keys[:i], keys[i+1:]...)
		}
	}
	return keys
}
func (c *Client) recordContracts(contracts []ContractCall, options BlockOptions, multicall bool) error {
	s := c.set
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.event["type"] == "setup" || options.BlockNumber != nil {
		return nil
	}
	cb, _ := s.event["eventCallback"].(Object)
	name, _ := cb["name"].(string)
	count, exists := s.eventCounts[name]
	if !exists || count%10 != 1 {
		return nil
	}
	if s.profiles == nil {
		s.profiles = map[string]*clientProfile{}
	}
	p := s.profiles[name]
	if p == nil {
		p = &clientProfile{entries: map[string]*ProfileEntry{}}
		s.profiles[name] = p
	}
	if multicall && len(contracts) >= 10 {
		return nil
	}
	for _, contract := range contracts {
		hints := make([]*RecordedProfile, 0, len(p.order))
		for _, k := range p.order {
			hints = append(hints, p.entries[k].Recorded)
		}
		cache := options.Cache
		if multicall {
			cache = ""
		} // source records each child, not outer cache option.
		recorded, err := RecordProfilePattern(s.event, contract, cache, hints, s.profileFieldOrder)
		if err != nil {
			return err
		}
		if recorded == nil {
			continue
		}
		key, err := GetProfilePatternKey(recorded.Pattern)
		if err != nil {
			return err
		}
		if entry := p.entries[key]; entry != nil {
			entry.Count++
			if recorded.HasConstant {
				p.constants = append(removeProfileKey(p.constants, key), key)
			}
		} else {
			p.entries[key] = &ProfileEntry{key, recorded, 1}
			p.order = append(p.order, key)
			if recorded.HasConstant {
				p.constants = append(p.constants, key)
				if len(p.constants) > 10 {
					old := p.constants[0]
					p.constants = p.constants[1:]
					delete(p.entries, old)
					p.order = removeProfileKey(p.order, old)
				}
			}
		}
	}
	return nil
}

// Clear discards ephemeral prefetched values only. Learned profiles and their LRU
// survive; in-flight work remains owned and joined by Close (it is not reinserted).
func (s *ClientSet) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clients {
		c.prefetched = map[string]*prefetchValue{}
	}
}
func canonicalPredictionKey(params any) (string, error) {
	b, e := json.Marshal(Object{"method": "eth_call", "params": params})
	return strings.ToLower(string(b)), e
}

// Prefetch recovers requests against the supplied future events. Completion means
// database lookup and RPC admission, NOT completion of admitted RPCs. First request
// wins deduplication BEFORE chain grouping, intentionally ignoring chain ID like
// the pinned source. Persistent-cache disabling does not disable RPC warming.
func (s *ClientSet) Prefetch(ctx context.Context, events []Object) error {
	ctx, finish, admissionErr := s.managedRoots.begin(ctx)
	if admissionErr != nil {
		return admissionErr
	}
	defer finish()
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return fmt.Errorf("indexing client is closed")
	}
	predictions := []predictedCall{}
	seen := map[string]bool{}
	for _, event := range events {
		cb, _ := event["eventCallback"].(Object)
		name, _ := cb["name"].(string)
		p := s.profiles[name]
		if p == nil {
			continue
		}
		for _, key := range p.order {
			entry := p.entries[key]
			request, err := RecoverProfilePattern(entry.Recorded.Pattern, event)
			if err != nil {
				s.mu.RUnlock()
				return err
			}
			data, _, err := encodeContractCall(request.ContractCall)
			if err != nil {
				s.mu.RUnlock()
				return err
			}
			tag := "latest"
			if request.BlockNumber != "latest" {
				n, e := clientInteger(request.BlockNumber)
				if e != nil {
					s.mu.RUnlock()
					return e
				}
				tag = "0x" + n.Text(16)
			}
			params := []any{Object{"to": request.Address, "data": data}, tag}
			key, err := canonicalPredictionKey(params)
			if err != nil {
				s.mu.RUnlock()
				return err
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			chain, err := clientInteger(request.ChainID)
			if err != nil {
				s.mu.RUnlock()
				return err
			}
			client := s.clients[chain.String()]
			if client == nil {
				s.mu.RUnlock()
				return fmt.Errorf("unknown prediction chain %s", chain)
			}
			count, ok := s.eventCounts[name]
			score := math.NaN()
			if ok {
				score = float64(entry.Count) * 10 / float64(count)
			}
			cacheKey, err := client.cacheKey("eth_call", params)
			if err != nil {
				s.mu.RUnlock()
				return err
			}
			predictions = append(predictions, predictedCall{client, params, score, cacheKey})
		}
	}
	s.mu.RUnlock()
	// Prepare chain jobs in configured order, then initiate all independently.
	// A blocked chain's synchronous CacheGet must not delay another chain's RPC
	// admission. Actual CacheGet invocation order depends on adapter scheduling;
	// no goroutine-start handshake is presented as synchronous DB admission.
	jobs := make([][]predictedCall, len(s.chainOrder))
	for i, chain := range s.chainOrder {
		for _, prediction := range predictions {
			if prediction.client == s.clients[chain] && prediction.score > 0.2 {
				jobs[i] = append(jobs[i], prediction)
			}
		}
	}
	settled := make(chan error, len(jobs))
	type lookupJob struct {
		id      uint64
		ctx     context.Context
		cancel  context.CancelFunc
		calls   []predictedCall
		futures []<-chan rpc.CacheLookupResult
	}
	admitted := make([]lookupJob, 0, len(jobs))
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("indexing client is closed")
	}
	if len(s.lookupPending) == 0 && len(jobs) > 0 {
		s.lookupDrained = make(chan struct{})
	}
	for _, job := range jobs {
		s.nextLookup++
		child, cancel := context.WithCancel(ctx)
		s.lookupPending[s.nextLookup] = cancel
		admitted = append(admitted, lookupJob{id: s.nextLookup, ctx: child, cancel: cancel, calls: job})
	}
	s.mu.Unlock()
	// Optional readers perform genuine synchronous prepared-job admission. Admit
	// every configured chain before waiting for any result; synchronous legacy
	// adapters remain compatible but have weaker invocation-prefix guarantees.
	for i := range admitted {
		job := &admitted[i]
		job.futures = make([]<-chan rpc.CacheLookupResult, len(job.calls))
		for j, p := range job.calls {
			if p.client.cacheEnabled && p.client.cache != nil {
				if reader, ok := p.client.cache.(rpc.AsyncCacheReader); ok {
					job.futures[j] = reader.BeginCacheGet(job.ctx, p.key)
				}
			}
		}
	}
	for _, job := range admitted {
		go func(job lookupJob) {
			consumed := make([]bool, len(job.futures))
			err := s.prefetchChain(job.ctx, ctx, job.calls, job.futures, consumed)
			s.managedRoots.recordFailure(err)
			if err != nil {
				settled <- err
			}
			job.cancel()
			// Per-key reader futures are real adapter-owned jobs. Preserve our
			// lookup ownership until all admitted jobs settle even on first error.
			for i, future := range job.futures {
				if future != nil && !consumed[i] {
					<-future
				}
			}
			s.mu.Lock()
			delete(s.lookupPending, job.id)
			if len(s.lookupPending) == 0 {
				close(s.lookupDrained)
			}
			s.mu.Unlock()
			if err == nil {
				settled <- nil
			}
		}(job)
	}
	for range jobs {
		if err := <-settled; err != nil {
			// Promise.all rejects on the first error without cancelling siblings.
			// Remaining real jobs retain ownership and can still admit source-like
			// effects. Close cancels/joins them; WaitPrefetch explicitly joins.
			return err
		}
	}
	return nil
}

func (s *ClientSet) prefetchChain(ctx, rpcContext context.Context, predictions []predictedCall, futures []<-chan rpc.CacheLookupResult, consumed []bool) error {
	type lookup struct {
		raw json.RawMessage
		hit bool
		key string
	}
	lookups := make([]lookup, len(predictions))
	// Source awaits the whole chain's batch lookup before admitting any misses.
	// The native Cache API is per-key, but preserves that completion boundary.
	for i, p := range predictions {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw json.RawMessage
		var hit bool
		var err error
		if p.client.cacheEnabled && p.client.cache != nil {
			if _, ok := p.client.cache.(rpc.AsyncCacheReader); ok {
				if futures[i] == nil {
					return fmt.Errorf("cache reader returned nil lookup completion")
				}
				result, ok := <-futures[i]
				consumed[i] = true
				if !ok {
					return fmt.Errorf("cache reader closed without lookup result")
				}
				raw, hit, err = result.Value, result.Hit, result.Err
			} else {
				raw, hit, err = p.client.cached(ctx, "eth_call", p.params)
			}
		}
		if err != nil {
			return err
		}
		lookups[i] = lookup{raw, hit, p.key}
	}
	for i, p := range predictions {
		value := lookups[i]
		if value.hit {
			s.mu.Lock()
			if !s.closed {
				p.client.prefetched[value.key] = &prefetchValue{raw: append(json.RawMessage(nil), value.raw...)}
			}
			s.mu.Unlock()
		} else if p.score > 0.8 {
			if err := p.client.admitPrefetch(rpcContext, value.key, p.params); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) admitPrefetch(ctx context.Context, key string, params any) error {
	s := c.set
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("indexing client is closed")
	}
	child, cancel := context.WithCancel(ctx)
	s.nextPrefetch++
	id := s.nextPrefetch
	if len(s.prefetchPending) == 0 {
		s.prefetchDrained = make(chan struct{})
	}
	s.prefetchPending[id] = cancel
	v := &prefetchValue{done: make(chan struct{}), future: true}
	c.prefetched[key] = v
	s.mu.Unlock()
	var completion <-chan error
	if a, ok := c.requester.(rpc.AsyncRequester); ok {
		completion = a.BeginCall(child, "eth_call", params, &v.raw)
	} else {
		ch := make(chan error, 1)
		completion = ch
		go func() { ch <- c.requester.Call(child, "eth_call", params, &v.raw); close(ch) }()
	}
	go func() {
		if completion == nil {
			v.err = fmt.Errorf("requester returned nil prefetch completion")
			s.managedRoots.recordFailure(v.err)
		} else {
			var ok bool
			v.err, ok = <-completion
			if !ok && s.managedRoots != nil {
				v.err = fmt.Errorf("requester closed without prefetch completion")
				s.managedRoots.recordFailure(v.err)
			}
		}
		// A valid speculative RPC failure may fall back to an eventual RPC
		// (notably multicall). Do not poison a successful supported fallback.
		close(v.done)
		cancel()
		s.mu.Lock()
		delete(s.prefetchPending, id)
		if len(s.prefetchPending) == 0 {
			close(s.prefetchDrained)
		}
		s.mu.Unlock()
	}()
	return nil
}
func (s *ClientSet) WaitPrefetch(ctx context.Context) error {
	s.mu.RLock()
	lookups := s.lookupDrained
	s.mu.RUnlock()
	select {
	case <-lookups:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.RLock()
	done := s.prefetchDrained
	s.mu.RUnlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// prefetchedResult does not consume entries. Failed futures are thrown by single
// calls, but multicall ignores them and falls through to eventual transport.
func (c *Client) prefetchedResult(ctx context.Context, method string, params any, multicall bool) (json.RawMessage, bool, error) {
	key, err := c.cacheKey(method, params)
	if err != nil {
		return nil, false, err
	}
	c.set.mu.RLock()
	v := c.prefetched[key]
	c.set.mu.RUnlock()
	return c.prefetchedValueResult(ctx, key, v, multicall)
}
func (c *Client) prefetchedValueResult(ctx context.Context, key string, v *prefetchValue, multicall bool) (json.RawMessage, bool, error) {
	if v == nil {
		return nil, false, nil
	}
	if v.future {
		select {
		case <-v.done:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
		if v.err != nil {
			if multicall {
				return nil, false, nil
			}
			return nil, true, v.err
		}
		// Source compares JSON.stringify(result) against raw ["0x", null], so even
		// serialized empty values pass this check. Preserve that prefetch-only quirk.
		if !multicall && c.cacheEnabled && c.cache != nil && string(v.raw) != "0x" {
			c.set.putAsync(ctx, c.cache, key, v.raw)
		}
	}
	return append(json.RawMessage(nil), v.raw...), true, nil
}
