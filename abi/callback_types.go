package abi

import (
	"fmt"
	"reflect"
	"strings"
)

// Hex is a 0x-prefixed value decoded from an Ethereum ABI. Addresses use the same wire type.
type Hex string

// NoValue represents the absent arguments or result of a zero-arity ABI function.
type NoValue interface{ noABIValue() }

// CallbackResultType describes a function result, including its single-output unwrapping.
func CallbackResultType(params []Parameter) (string, error) {
	if len(params) == 1 {
		return goParameterType(functionParameters(params)[0], "abi.Hex")
	}
	return CallbackArgsType(params, false)
}

// CallbackArgsType derives the Go type of decoded event or call arguments.
// Indexed reference values contain the topic hash. Calls always use positions.
func CallbackArgsType(params []Parameter, event bool) (string, error) {
	if len(params) == 0 {
		if event {
			return "[0]struct{}", nil
		}
		return "abi.NoValue", nil
	}
	// Nested tuples follow viem's convention that an empty name denotes a position.
	params = functionParameters(params)
	named := event
	for i, p := range params {
		named = named && p.Name != ""
		if event && p.Indexed && (p.Type == "tuple" || p.Type == "bytes" || p.Type == "string" || strings.Contains(p.Type, "[")) {
			params[i].Type = "bytes32"
		}
	}
	if named {
		return goTupleType(params, true, "abi.Hex")
	}
	first, err := goParameterType(params[0], "abi.Hex")
	if err != nil {
		return "", err
	}
	for _, p := range params[1:] {
		typ, err := goParameterType(p, "abi.Hex")
		if err != nil {
			return "", err
		}
		if typ != first {
			return goTupleType(params, false, "abi.Hex")
		}
	}
	return fmt.Sprintf("[%d]%s", len(params), first), nil
}

// ProjectDecoded converts an already decoded ABI value into the generated caller type.
func ProjectDecoded[T any](value any) (T, error) {
	var result T
	if err := assignDecoded(reflect.ValueOf(&result).Elem(), value); err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}
