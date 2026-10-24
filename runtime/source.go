package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

// NewFromSource binds actual native handlers and the compiled source filters
// to one selected chain. Multi-chain scheduling remains separate.
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
	for name, event := range a.Events {
		item, ok := a.EventItem(name)
		if !ok {
			return nil, fmt.Errorf("missing original ABI event %s:%s", source.Name, name)
		}
		eventName := source.Name + ":" + name
		out = append(out, nativeLogCallback{name: eventName, item: item, filter: nativeContractFilter(source, event.ID.Hex()), callback: protocol.ContractCallback[abi.Event]{CallbackBase: protocol.CallbackBase{Name: eventName, Chain: protocol.Object{"id": e.chain.ID, "name": e.chain.Name}, Filter: protocol.Object{"type": "log"}}, ABIItem: item}.CallbackObject()})
	}
	return out, nil
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
