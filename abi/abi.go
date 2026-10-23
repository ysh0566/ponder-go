// Package abi adapts Ethereum ABI metadata and Ponder's decoding conventions.
package abi

import (
	"encoding/json"
	"fmt"
	ethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/crypto"
	"strings"
)

type ABI struct {
	ethabi.ABI
	originalEvents    map[string]Event
	originalFunctions map[string]Function
	functionEntries   []Function
}

func signatureType(p Parameter) string {
	typ := p.Type
	if strings.HasPrefix(typ, "tuple") {
		parts := []string{}
		for _, c := range p.Components {
			parts = append(parts, signatureType(c))
		}
		return "(" + strings.Join(parts, ",") + ")" + strings.TrimPrefix(typ, "tuple")
	}
	base := typ
	if i := strings.IndexByte(base, '['); i >= 0 {
		base = base[:i]
	}
	if base == "uint" || base == "int" {
		typ = base + "256" + strings.TrimPrefix(typ, base)
	}
	return typ
}
func signature(e Event) string {
	parts := []string{}
	for _, p := range e.Inputs {
		parts = append(parts, signatureType(p))
	}
	return e.Name + "(" + strings.Join(parts, ",") + ")"
}

// EventSelector computes topic zero using canonical Solidity parameter types.
func EventSelector(e Event) string { return crypto.Keccak256Hash([]byte(signature(e))).Hex() }
func Parse(data string) (ABI, error) {
	a, e := ethabi.JSON(strings.NewReader(data))
	if e != nil {
		return ABI{}, e
	}
	var entries []json.RawMessage
	if e = json.Unmarshal([]byte(data), &entries); e != nil {
		return ABI{}, e
	}
	events := map[string]Event{}
	functions := map[string]Function{}
	functionEntries := []Function{}
	for _, entry := range entries {
		var event Event
		if e = json.Unmarshal(entry, &event); e != nil {
			return ABI{}, e
		}
		if event.Type == "function" {
			var function Function
			if e = json.Unmarshal(entry, &function); e != nil {
				return ABI{}, e
			}
			functions[FunctionSelector(function)] = function
			functionEntries = append(functionEntries, function)
			continue
		}
		if event.Type != "event" {
			continue
		}
		for name, metadata := range a.Events {
			if metadata.Sig == signature(event) {
				events[name] = event
			}
		}
	}
	return ABI{ABI: a, originalEvents: events, originalFunctions: functions, functionEntries: functionEntries}, nil
}
func (a ABI) DecodeEvent(name string, topics []string, data string) (any, error) {
	event, ok := a.originalEvents[name]
	if !ok {
		return nil, fmt.Errorf("unknown ABI event %q", name)
	}
	return DecodeEventLog(event, topics, data)
}
