package events

import "ponder.local/ponder-go/abi"

// EventCallback is the closed contract/account/block callback union. Contract
// ABI metadata is available after narrowing to ContractCallback, matching the
// source EventCallback union's property boundary.
type EventCallback interface {
	CallbackObject() Object
	eventCallback()
}

type CallbackBase struct {
	Filter Object
	Name   string
	Fn     any
	Chain  Object
}

func (base CallbackBase) callbackObject(kind string) Object {
	result := Object{"type": kind, "filter": base.Filter, "name": base.Name, "chain": base.Chain}
	if base.Fn != nil {
		result["fn"] = base.Fn
	}
	return result
}

type ContractABI interface{ abi.Event | abi.Function }
type ContractCallback[Item ContractABI] struct {
	CallbackBase
	ABIItem  Item
	Metadata Object
}

func (ContractCallback[Item]) eventCallback() {}
func (callback ContractCallback[Item]) CallbackObject() Object {
	result := callback.CallbackBase.callbackObject("contract")
	result["abiItem"] = callback.ABIItem
	if callback.Metadata != nil {
		result["metadata"] = callback.Metadata
	}
	return result
}

type AccountCallback struct {
	CallbackBase
	Direction string
}

func (AccountCallback) eventCallback() {}
func (callback AccountCallback) CallbackObject() Object {
	result := callback.CallbackBase.callbackObject("account")
	result["direction"] = callback.Direction
	return result
}

type BlockCallback struct{ CallbackBase }

func (BlockCallback) eventCallback() {}
func (callback BlockCallback) CallbackObject() Object {
	return callback.CallbackBase.callbackObject("block")
}

func CallbackObjects(callbacks []EventCallback) []Object {
	result := make([]Object, len(callbacks))
	for i, callback := range callbacks {
		result[i] = callback.CallbackObject()
	}
	return result
}
