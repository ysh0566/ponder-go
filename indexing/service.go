package indexing

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync/atomic"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

// Service dispatches handlers inside a caller-owned IndexingStore transaction.
// It deliberately does not own the outer historical runner, transaction commit,
// multi-chain cutover, readiness, or realtime synchronization. A service permits
// one active dispatch call; callbacks must finish before returning.
type Service struct {
	options   Options
	args      *HandlerArgs
	clients   map[string]*Client
	contracts map[string]Object
	lastChain string
	proxies   map[string]*PropertyView
	filters   map[string][]Object
	resolved  map[uintptr]bool

	active atomic.Bool
}

func NewService(o Options) (*Service, error) {
	if o.Clients == nil || o.Database == nil || !o.Database.configured() {
		return nil, fmt.Errorf("indexing service requires real clients and indexing store")
	}
	if o.Errors == nil {
		return nil, fmt.Errorf("indexing service requires shared retryable error handler")
	}
	if o.Database.errors != o.Errors {
		return nil, fmt.Errorf("indexing database and service must share retryable error handler")
	}
	if len(o.Build.Chains) != len(o.Build.Contracts) {
		return nil, fmt.Errorf("indexing chain/contracts count mismatch")
	}
	names := []string{}
	for _, f := range o.Build.IndexingFunctions {
		n, _ := f["name"].(string)
		names = append(names, n)
	}
	if o.ColumnAccessPattern == nil {
		o.ColumnAccessPattern = NewColumnAccessPattern(names)
	}
	if o.EventCount == nil {
		o.EventCount = NewEventCount(names)
	}
	if o.Metrics == nil {
		o.Metrics = &Metrics{}
	}
	if o.Metrics.Completed == nil {
		o.Metrics.Completed = map[string]uint64{}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Yield == nil {
		o.Yield = func(ctx context.Context) error { runtime.Gosched(); return ctx.Err() }
	}
	if o.IsKilled == nil {
		o.IsKilled = func() bool { return false }
	}
	s := &Service{options: o, args: &HandlerArgs{Context: &Context{Chain: Object{}, DB: o.Database}}, clients: map[string]*Client{}, contracts: map[string]Object{}, proxies: map[string]*PropertyView{}, filters: map[string][]Object{}, resolved: map[uintptr]bool{}}
	for i, chain := range o.Build.Chains {
		key, e := clientChainKey(chain)
		if e != nil {
			return nil, e
		}
		c, e := o.Clients.GetClient(chain)
		if e != nil {
			return nil, e
		}
		s.clients[key] = c
		s.contracts[key] = o.Build.Contracts[i]
	}
	for _, group := range o.Build.EventCallbacks {
		for _, cb := range group {
			name, _ := cb["name"].(string)
			f, ok := cb["filter"].(Object)
			if !ok {
				return nil, fmt.Errorf("callback %q lacks filter", name)
			}
			if o.ColumnAccessPattern[name] == nil {
				return nil, fmt.Errorf("callback %q lacks column profile", name)
			}
			s.filters[name] = append(s.filters[name], f)
			s.resolved[objectID(f)] = false
		}
	}
	for _, component := range []string{"block", "transaction", "transactionReceipt", "trace"} {
		p, e := NewPropertyView(component, o.ColumnAccessPattern, o.Errors, s.resetFilterInclude)
		if e != nil {
			return nil, e
		}
		s.proxies[component] = p
	}
	return s, nil
}
func objectID(o Object) uintptr                             { return reflect.ValueOf(o).Pointer() }
func (s *Service) Metrics() *Metrics                        { return s.options.Metrics }
func (s *Service) EventCount() map[string]uint64            { return s.options.EventCount }
func (s *Service) ColumnAccessPattern() ColumnAccessPattern { return s.options.ColumnAccessPattern }
func (s *Service) begin(ctx context.Context) error {
	if e := s.options.Database.checkBinding(ctx); e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if !s.active.CompareAndSwap(false, true) {
		return fmt.Errorf("indexing service dispatch is already active")
	}
	return nil
}
func (s *Service) resetFilterInclude(name string) {
	filters := s.filters[name]
	if len(filters) == 0 {
		return
	}
	kind, _ := filters[0]["type"].(string)
	receipts, _ := filters[0]["hasTransactionReceipt"].(bool)
	include := DefaultFilterInclude(kind, receipts)
	for _, f := range filters {
		s.resolved[objectID(f)] = false
		f["include"] = include
	}
	s.options.ColumnAccessPattern[name].Count = 0
}
func (s *Service) setChain(chain Object) error {
	key, e := clientChainKey(chain)
	if e != nil {
		return e
	}
	if key == s.lastChain {
		return nil
	}
	c, ok := s.clients[key]
	if !ok {
		return fmt.Errorf("unknown indexing chain %s", key)
	}
	s.args.Context.Chain["id"] = chain["id"]
	s.args.Context.Chain["name"] = chain["name"]
	s.args.Context.Contracts = s.contracts[key]
	s.args.Context.Client = c
	s.lastChain = key
	return nil
}
func callbackOf(event Object, setup bool) (Object, string, Handler, error) {
	key := "eventCallback"
	if setup {
		key = "setupCallback"
	}
	cb, ok := event[key].(Object)
	if !ok {
		return nil, "", nil, fmt.Errorf("event lacks %s", key)
	}
	name, _ := cb["name"].(string)
	fn, ok := cb["fn"].(Handler)
	if !ok {
		if f, valid := cb["fn"].(func(context.Context, *HandlerArgs) error); valid {
			fn = Handler(f)
		} else {
			return cb, name, nil, fmt.Errorf("callback %q has no handler", name)
		}
	}
	return cb, name, fn, nil
}
func (s *Service) takeSticky() error {
	e := s.options.Errors.GetRetryableError()
	if e != nil {
		s.options.Errors.ClearRetryableError()
	}
	return e
}
func knownHandlerError(err error) bool {
	if _, ok := err.(interface{ sourceBaseError() }); ok {
		return true
	}
	switch err.(type) {
	case *store.NonRetryableUserError, *store.RecordNotFoundError, *store.UniqueConstraintError, *store.NotNullConstraintError, *store.BigIntSerializationError, *store.InvalidStoreAccessError:
		return true
	}
	return false
}
func (s *Service) execute(ctx context.Context, event Object, view EventView, setup bool) error {
	_, name, fn, err := callbackOf(event, setup)
	if err == nil {
		chain, _ := event["chain"].(Object)
		err = s.setChain(chain)
	}
	if err == nil {
		if !setup {
			s.args.Event = view
		}
		start := s.options.Now()
		err = fn(ctx, s.args)
		// Setup checks the sticky error BEFORE duration; events check it AFTER.
		if err == nil && setup {
			err = s.takeSticky()
		}
		if err == nil {
			s.options.Metrics.Durations = append(s.options.Metrics.Durations, DurationObservation{Event: name, Seconds: s.options.Now().Sub(start).Seconds()})
		}
		if err == nil && !setup {
			err = s.takeSticky()
		}
	}
	if err == nil {
		return nil
	}
	if sticky := s.takeSticky(); sticky != nil {
		err = sticky
	}
	if s.options.IsKilled() {
		return &ShutdownError{}
	}
	var invalid *InvalidEventAccessError
	if !setup && errors.As(err, &invalid) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if s.options.OnError != nil {
		s.options.OnError(ErrorObservation{Event: event, Err: err})
	}
	s.options.Metrics.HasError = true
	if !knownHandlerError(err) {
		return &IndexingFunctionError{Message: err.Error()}
	}
	return err
}
func (s *Service) ProcessSetupEvents(ctx context.Context) error {
	if e := s.begin(ctx); e != nil {
		return e
	}
	defer s.active.Store(false)
	for _, group := range s.options.Build.SetupCallbacks {
		for _, cb := range group {
			if e := ctx.Err(); e != nil {
				return e
			}
			chain, ok := cb["chain"].(Object)
			if !ok {
				return fmt.Errorf("setup callback lacks chain")
			}
			id, e := clientInteger(chain["id"])
			if e != nil {
				return e
			}
			blockValue := cb["block"]
			if blockValue == nil || filter.IsUndefined(blockValue) {
				blockValue = 0
			}
			block, e := clientInteger(blockValue)
			if e != nil {
				return e
			}
			if !id.IsUint64() || !block.IsUint64() {
				return fmt.Errorf("setup checkpoint exceeds uint64")
			}
			cp, e := checkpoint.Encode(checkpoint.Checkpoint{ChainID: id.Uint64(), BlockNumber: block.Uint64()})
			if e != nil {
				return e
			}
			event := Object{"type": "setup", "chain": chain, "setupCallback": cb, "checkpoint": cp, "block": block}
			s.options.Clients.SetEvent(event)
			s.options.Clients.SetEventCounts(s.options.EventCount)
			if e = s.execute(ctx, event, nil, true); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Service) historicalView(event Object, name string) (EventView, error) {
	body, ok := event["event"].(Object)
	if !ok {
		return nil, fmt.Errorf("event body is not an object")
	}
	clone := Object{}
	for key, value := range body {
		clone[key] = value
	}
	kind, _ := event["type"].(string)
	components := []string{"block"}
	if kind != "block" {
		components = append(components, "transaction", "transactionReceipt")
	}
	if kind == "trace" || kind == "transfer" {
		components = append(components, "trace")
	}
	for _, component := range components {
		raw, present := body[component]
		underlying, ok := raw.(Object)
		optional := component == "transactionReceipt" || (kind == "log" && component == "transaction")
		if optional && !present {
			continue
		}
		// Undefined optional fields are absent for proxy binding in JavaScript.
		if optional && filter.IsUndefined(raw) {
			continue
		}
		if !ok {
			return nil, fmt.Errorf("event %s is not an object", component)
		}
		p := s.proxies[component]
		p.Bind(name, underlying)
		clone[component] = p
	}
	return EventView(clone), nil
}
func (s *Service) ProcessHistoricalEvents(ctx context.Context, events []Object, updateIndexingSeconds func(Object, Object)) error {
	if e := s.begin(ctx); e != nil {
		return e
	}
	defer s.active.Store(false)
	lastLoop, lastMetrics := s.options.Now(), s.options.Now()
	for _, event := range events {
		if e := ctx.Err(); e != nil {
			return e
		}
		_, name, _, e := callbackOf(event, false)
		if e != nil {
			return e
		}
		profile := s.options.ColumnAccessPattern[name]
		if profile == nil {
			return fmt.Errorf("event %q lacks column profile", name)
		}
		s.options.Clients.SetEvent(event)
		s.options.Clients.SetEventCounts(s.options.EventCount)
		chain, _ := event["chain"].(Object)
		id, e := clientInteger(chain["id"])
		if e != nil {
			return e
		}
		body, _ := event["event"].(Object)
		kind, _ := event["type"].(string)
		var fieldOrder map[string][]string
		if s.options.FieldOrder != nil {
			fieldOrder = s.options.FieldOrder(event)
		}
		s.options.Clients.SetProfileFieldOrder(fieldOrder)
		if e = s.options.Database.SetProfileEventContext(ctx, store.ProfileEvent{Callback: name, Count: s.options.EventCount[name], Type: kind, ChainID: id.Uint64(), Data: store.Row(body), FieldOrder: fieldOrder}); e != nil {
			return e
		}
		view, e := s.historicalView(event, name)
		if e != nil {
			return e
		}
		if e = s.execute(ctx, event, view, false); e != nil {
			return e
		}
		s.options.Metrics.Completed[name]++
		profile.Count++
		s.options.EventCount[name]++
		s.options.Clients.SetEventCounts(s.options.EventCount)
		now := s.options.Now()
		if now.Sub(lastLoop) > 25*time.Millisecond {
			lastLoop = now
			if e = s.options.Yield(ctx); e != nil {
				return e
			}
		}
		if now.Sub(lastMetrics) > 100*time.Millisecond {
			lastMetrics = now
			if updateIndexingSeconds != nil {
				updateIndexingSeconds(event, chain)
			}
		}
	}
	before, after := true, true
	for _, group := range s.options.Build.EventCallbacks {
		for _, cb := range group {
			f := cb["filter"].(Object)
			if s.resolved[objectID(f)] {
				continue
			}
			before = false
			name := cb["name"].(string)
			p := s.options.ColumnAccessPattern[name]
			if p.Count < 100 {
				after = false
				continue
			}
			s.resolved[objectID(f)] = true
			p.Resolved = true
			kind, _ := f["type"].(string)
			receipts, _ := f["hasTransactionReceipt"].(bool)
			f["include"] = ResolveProfileInclude(p, kind, receipts)
		}
	}
	if !before && after && s.options.OnResolved != nil {
		counts := map[string]int{}
		total := 0
		for _, component := range []string{"block", "transaction", "transactionReceipt", "trace"} {
			seen := map[string]bool{}
			for _, p := range s.options.ColumnAccessPattern {
				for _, key := range *p.columns(component) {
					seen[key] = true
				}
			}
			counts[component] = len(seen)
			total += len(seen)
		}
		counts["total"] = total
		s.options.OnResolved(counts)
	}
	if e := s.options.Yield(ctx); e != nil {
		return e
	}
	if len(events) > 0 && updateIndexingSeconds != nil {
		event := events[len(events)-1]
		chain, _ := event["chain"].(Object)
		updateIndexingSeconds(event, chain)
	}
	return nil
}
func (s *Service) ProcessRealtimeEvents(ctx context.Context, events []Object) error {
	if e := s.begin(ctx); e != nil {
		return e
	}
	defer s.active.Store(false)
	for _, event := range events {
		if e := ctx.Err(); e != nil {
			return e
		}
		_, name, _, e := callbackOf(event, false)
		if e != nil {
			return e
		}
		s.options.Clients.SetEvent(event)
		s.options.Clients.SetEventCounts(s.options.EventCount)
		if s.options.FieldOrder != nil {
			s.options.Clients.SetProfileFieldOrder(s.options.FieldOrder(event))
		}
		body, ok := event["event"].(Object)
		if !ok {
			return fmt.Errorf("event body is not an object")
		}
		view := EventView(body)
		if e = s.execute(ctx, event, view, false); e != nil {
			return e
		}
		s.options.Metrics.Completed[name]++
		s.options.EventCount[name]++
		s.options.Clients.SetEventCounts(s.options.EventCount)
	}
	return nil
}
