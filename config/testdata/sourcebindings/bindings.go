// Code generated from actual app chains/ABI; DO NOT EDIT.
package sourcebindings

import (
	"encoding/json"
	"fmt"
	"ponder.local/ponder-go/config"
)

type Chain struct{ index int }
type Event struct{ index int }

var Chains = struct{ Mainnet Chain }{Mainnet: Chain{index: 1}}
var Events = struct{ Event0 Event }{Event0: Event{index: 1}}

func (value Chain) selection() (config.SourceChainSelection, error) {
	switch value.index {
	case 1:
		return config.SourceOnChain("mainnet"), nil
	}
	return nil, fmt.Errorf("undefined generated chain reference")
}
func (value Event) name() (string, error) {
	switch value.index {
	case 1:
		return "Event0", nil
	}
	return "", fmt.Errorf("undefined generated ABI event reference")
}

type Filter struct {
	Event Event
	Args  any
}
type Contract[S, E any] struct {
	Chain                      Chain
	Address                    config.SourceOption[any]
	StartBlock                 S
	EndBlock                   E
	Filter                     *Filter
	IncludeTransactionReceipts config.SourceOption[bool]
	IncludeCallTraces          config.SourceOption[bool]
}

func Create[S, E config.SourceBlockBound](base *config.SourceConfiguration, name string, value Contract[S, E]) (config.SourceTypedConfiguration[config.SourceTypedContract[S, E]], error) {
	var zero config.SourceTypedConfiguration[config.SourceTypedContract[S, E]]
	chain, err := value.Chain.selection()
	if err != nil {
		return zero, err
	}
	contract := config.SourceTypedContract[S, E]{Chain: chain, ABI: json.RawMessage("[\n    {\n      \"name\": \"Event0\",\n      \"type\": \"event\",\n      \"inputs\": [\n        {\n          \"type\": \"bytes32\",\n          \"name\": \"arg\",\n          \"indexed\": true\n        }\n      ]\n    }\n  ]"), Address: value.Address, StartBlock: value.StartBlock, EndBlock: value.EndBlock, IncludeTransactionReceipts: value.IncludeTransactionReceipts, IncludeCallTraces: value.IncludeCallTraces}
	if value.Filter != nil {
		event, err := value.Filter.Event.name()
		if err != nil {
			return zero, err
		}
		contract.Filter = config.SourceSome[any](map[string]any{"event": event, "args": value.Filter.Args})
	}
	return config.CreateTypedSourceConfiguration(base, name, contract), nil
}
