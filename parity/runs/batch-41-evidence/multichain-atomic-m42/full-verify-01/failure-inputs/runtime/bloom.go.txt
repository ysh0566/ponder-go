package runtime

import (
	"encoding/hex"
	"fmt"

	"ponder.local/ponder-go/bloom"
	selection "ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/finality"
)

type bloomPlan struct {
	logs      map[string][]bloom.LogFilterBloom
	factories map[string]bloom.FactoryBloom
}

// Compile source predicates once when constructing the engine, using the public
// typed log filter. The runtime Object dispatch adapter remains independent.
func (e *Engine) compileBloomPlan() error {
	e.blooms = bloomPlan{logs: map[string][]bloom.LogFilterBloom{}, factories: map[string]bloom.FactoryBloom{}}
	for _, source := range e.Config.Contracts {
		e.blooms.logs[source.Name] = []bloom.LogFilterBloom{}
		address := nativeContractFilter(source, "")["address"]
		if f, ok := e.factories[source.Name]; ok {
			address = nativeFactory(f)
		}

		if e.Config.SourcePlan != nil {
			callbacks, err := e.logCallbacks(source)
			if err != nil {
				return err
			}
			for _, callback := range callbacks {
				f := callback.filter
				compiled, err := bloom.GetLogFilterBloom(selection.LogFilter{ChainID: e.chain.ID, SourceID: source.Name, Address: address, Topic0: selection.HexTopic(f["topic0"].(string)), Topic1: f["topic1"], Topic2: f["topic2"], Topic3: f["topic3"]})
				if err != nil {
					return fmt.Errorf("log bloom %s: %w", callback.name, err)
				}
				e.blooms.logs[source.Name] = append(e.blooms.logs[source.Name], compiled)
			}
			continue
		}
		from := source.StartBlock
		for name, item := range e.abis[source.Name].Events {
			compiled, err := bloom.GetLogFilterBloom(selection.LogFilter{
				ChainID: e.chain.ID, SourceID: source.Name, Address: address,
				Topic0: selection.HexTopic(item.ID.Hex()), FromBlock: &from, ToBlock: source.EndBlock,
				HasTransactionReceipt: source.IncludeTransactionReceipts,
			})
			if err != nil {
				return fmt.Errorf("log bloom %s:%s: %w", source.Name, name, err)
			}
			e.blooms.logs[source.Name] = append(e.blooms.logs[source.Name], compiled)
		}
	}
	for _, f := range e.factories {
		if _, found := e.blooms.factories[f.ID]; found {
			continue
		}
		compiled, err := bloom.GetFactoryBloom(nativeFactory(f))
		if err != nil {
			return fmt.Errorf("factory bloom %s: %w", f.ID, err)
		}
		e.blooms.factories[f.ID] = compiled
	}
	return nil
}

// The pinned realtime implementation treats an all-zero bloom as missing and
// bypasses blooms on async execution chains. Preserve permissive old envelopes:
// absent, short or malformed blooms also fall back to requesting logs.
func (e *Engine) blockBloom(b Block) []byte {
	if finality.IsAsyncExecutionChain(e.chain.ID) || len(b.LogsBloom) != 2+2*bloom.SizeBytes || b.LogsBloom[:2] != "0x" {
		return nil
	}
	decoded, err := hex.DecodeString(b.LogsBloom[2:])
	if err != nil {
		return nil
	}
	for _, value := range decoded {
		if value != 0 {
			return decoded
		}
	}
	return nil
}
func (e *Engine) contractLogsMayMatch(blockBloom []byte, name string) bool {
	if blockBloom == nil {
		return true
	}
	filters, ok := e.blooms.logs[name]
	if !ok {
		return true
	}
	for _, compiled := range filters {
		if bloom.IsFilterInBloom(blockBloom, compiled) {
			return true
		}
	}
	return false
}
func (e *Engine) factoryLogsMayMatch(blockBloom []byte, id string) bool {
	if blockBloom == nil {
		return true
	}
	compiled, ok := e.blooms.factories[id]
	return !ok || bloom.IsFactoryInBloom(blockBloom, compiled)
}
