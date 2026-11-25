package indexing

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/ysh0566/ponder-go/filter"
)

// ContractABI retains raw ABI metadata when a contract has a runtime declaration.
type ContractABI = json.RawMessage

// ContractRecord retrieves the contract metadata bound by the service for this chain.
func (ctx *Context) ContractRecord(name string) (Object, error) {
	if ctx != nil {
		if value, ok := ctx.Contracts[name].(Object); ok && value != nil {
			return value, nil
		}
	}
	return nil, fmt.Errorf("contract %q is unavailable on the current chain", name)
}

// ContractABIValue returns the current ABI and, for static declarations, checks
// item order and all metadata before exposing literal item types.
func ContractABIValue(record Object, expected string) (ContractABI, error) {
	raw, err := json.Marshal(record["abi"])
	if err != nil {
		return nil, err
	}
	var actual []any
	if err = json.Unmarshal(raw, &actual); err != nil || actual == nil {
		return nil, fmt.Errorf("invalid callback contract ABI")
	}
	if expected != "" {
		var want []any
		if err = json.Unmarshal([]byte(expected), &want); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(actual, want) {
			return nil, fmt.Errorf("callback contract ABI differs from generated configuration")
		}
	}
	return raw, nil
}

func ContractProperty(record Object, key string) (any, bool) {
	value, present := record[key]
	return value, present && value != nil && !filter.IsUndefined(value)
}

// ContractBlockNumber normalizes the numeric block bound stored by either backend.
func ContractBlockNumber(value any) (uint64, error) {
	n, err := clientInteger(value)
	if err != nil || !n.IsUint64() {
		return 0, fmt.Errorf("invalid callback contract block %v", value)
	}
	return n.Uint64(), nil
}
