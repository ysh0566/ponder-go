package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/config"
	protocol "github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/store"
)

// NewFromSource binds actual native handlers and the compiled source filters
// to one selected chain.
func NewFromSource(build *config.SourceIndexingBuild, chain string, s *store.Store, options Options) (*Engine, error) {
	cfg, err := build.NativeConfig(chain)
	if err != nil {
		return nil, err
	}
	handlers, err := sourceHandlers(cfg.SourcePlan)
	if err != nil {
		return nil, err
	}
	return NewWithOptions(cfg, s, handlers, options)
}

// NewSourceIndexer compiles source declarations for a single store. Only
// explicit "latest" bounds need RPC during compilation. With Isolated
// ordering the workers can then start and recover independently.
func NewSourceIndexer(ctx context.Context, project *config.SourceConfiguration, buildOptions config.SourceBuildOptions, functions []config.SourceIndexingFunction, db *store.Store, options Options) (ApplicationIndexer, error) {
	if db == nil {
		return nil, fmt.Errorf("indexer requires a store")
	}
	configs, err := config.BuildNativeSourceConfigs(ctx, project, buildOptions, functions)
	if err != nil {
		return nil, err
	}
	return newSourceIndexer(configs, db, options)
}

// NewIndexerFromSource starts from the complete compiled build. Its registered
// callbacks use runtime.Handler. Serve or run the returned indexer with the same
// application entry points used by NewIndexer.
func NewIndexerFromSource(build *config.SourceIndexingBuild, db *store.Store, options Options) (ApplicationIndexer, error) {
	if db == nil {
		return nil, fmt.Errorf("indexer requires a store")
	}
	configs, err := build.NativeConfigs()
	if err != nil {
		return nil, err
	}
	return newSourceIndexer(configs, db, options)
}

func newSourceIndexer(configs []config.Config, db *store.Store, options Options) (ApplicationIndexer, error) {
	var workers isolatedIndexer
	for _, cfg := range configs {
		handlers, err := sourceHandlers(cfg.SourcePlan)
		if err != nil {
			return nil, err
		}
		// Each deployment shares the compiled transport and owns its store cache.
		cfg.SourcePlan.Client = cfg.SourcePlan.Client.WithCache(nil)
		worker, err := newChainWorker(cfg, db, handlers, options)
		if err != nil {
			return nil, err
		}
		workers = append(workers, worker)
	}
	if len(workers) == 1 {
		return workers[0], nil
	}
	return coordinateWorkers(workers, db.Ordering), nil
}

func sourceHandlers(plan *config.SourceRuntimePlan) (map[string]Handler, error) {
	handlers := map[string]Handler{}
	bind := func(name string, fn any) error {
		switch handler := fn.(type) {
		case Handler:
			handlers[name] = handler
		case func(context.Context, *Context) error:
			handlers[name] = Handler(handler)
		default:
			return fmt.Errorf("source callback %q must bind a native runtime.Handler", name)
		}
		if handlers[name] == nil {
			return fmt.Errorf("source callback %q must bind a non-nil native runtime.Handler", name)
		}
		return nil
	}
	for _, callback := range plan.Callbacks {
		if err := bind(callback.Name, callback.Fn); err != nil {
			return nil, err
		}
	}
	for _, setup := range plan.Setup {
		if err := bind(setup.Name, setup.Fn); err != nil {
			return nil, err
		}
	}
	return handlers, nil
}

type nativeLogCallback struct {
	name     string
	item     abi.Event
	filter   filter.Object
	callback protocol.Object
}

func (e *Engine) logCallbacks(source config.Contract) ([]nativeLogCallback, error) {
	out := []nativeLogCallback{}
	if plan := e.Config.SourcePlan; plan != nil {
		for _, callback := range plan.Callbacks {
			f := callback.Filter.Value()
			if callback.Filter.Kind() != "log" || f["sourceId"] != source.Name {
				continue
			}
			var item abi.Event
			if err := json.Unmarshal(callback.ABIItem, &item); err != nil {
				return nil, err
			}
			out = append(out, nativeLogCallback{name: callback.Name, item: item, filter: f, callback: callback.Value()})
		}
		return out, nil
	}
	a := e.abis[source.Name]
	seen := map[string]bool{}
	for _, entry := range a.ParsedEvents() {
		item := entry.Item
		eventName := source.Name + ":" + a.EventSafeName(item)
		if e.Handlers[eventName] == nil {
			eventName = source.Name + ":" + entry.Name
		}
		if seen[eventName] {
			continue
		}
		seen[eventName] = true
		topic := a.Events[entry.Name].ID.Hex()
		out = append(out, nativeLogCallback{name: eventName, item: item, filter: nativeContractFilter(source, topic), callback: protocol.ContractCallback[abi.Event]{CallbackBase: protocol.CallbackBase{Name: eventName, Chain: protocol.Object{"id": e.chain.ID, "name": e.chain.Name}, Filter: protocol.Object{"type": "log"}}, ABIItem: item}.CallbackObject()})
	}
	return out, nil
}

// A source and log index identify an ordinary receipt. If multiple ABI
// declarations share the topic, each matched callback needs its own receipt.
func (e *Engine) logReceiptID(hash, source, name string, log Log) string {
	if len(log.Topics) > 0 && e.abis[source].SharedEventSelector(log.Topics[0]) {
		return fmt.Sprintf("log-callback:%d:%s:%s:%s", e.chain.ID, hash, name, log.LogIndex)
	}
	return fmt.Sprintf("log:%d:%s:%s:%s", e.chain.ID, hash, source, log.LogIndex)
}

func (e *Engine) planSourceLogs(callbacks []nativeLogCallback, address any, hash string) (filter.Object, bool) {
	if e.Config.SourcePlan == nil {
		return plannedBlockLogs(address, hash)
	}
	predicates := []filter.Object{}
	for _, callback := range callbacks {
		predicate := filter.Object{}
		for key, value := range callback.filter {
			predicate[key] = value
		}
		predicate["address"] = address
		predicates = append(predicates, predicate)
	}
	value := filter.MergeLogFiltersToBlockRequest(predicates, nil)
	if filter.IsUndefined(value) {
		return nil, false
	}
	query := filter.RPCValue(value).(map[string]any)
	query["blockHash"] = hash
	return query, true
}
func (e *Engine) sourceAccountCallbacks(ctx context.Context, number uint64) ([]protocol.Object, bool, map[string][]filter.ChildAddress, error) {
	callbacks := []protocol.Object{}
	needsTrace := false
	children := map[string][]filter.ChildAddress{}
	for _, callback := range e.Config.SourcePlan.Callbacks {
		kind := callback.Filter.Kind()
		if kind == "log" || kind == "block" || e.Handlers[callback.Name] == nil {
			continue
		}
		value := callback.Filter.Value()
		if from, ok := value["fromBlock"].(float64); ok && float64(number) < from {
			continue
		}
		if to, ok := value["toBlock"].(float64); ok && float64(number) > to {
			continue
		}
		for _, key := range []string{"fromAddress", "toAddress"} {
			factory, ok := value[key].(map[string]any)
			if !ok {
				continue
			}
			id, _ := factory["id"].(string)
			if _, loaded := children[id]; loaded {
				continue
			}
			found, err := e.Store.FactoryChildren(ctx, id, e.chain.ID, number)
			if err != nil {
				return nil, false, nil, err
			}
			entries := []filter.ChildAddress{}
			for address, birth := range found {
				entries = append(entries, filter.ChildAddress{Address: address, Block: birth})
			}
			children[id] = entries
		}
		callbacks = append(callbacks, callback.Value())
		needsTrace = needsTrace || kind == "trace" || kind == "transfer"
	}
	return callbacks, needsTrace, children, nil
}
