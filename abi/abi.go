// Package abi adapts Ethereum ABI metadata and Ponder's decoding conventions.
package abi

import (
	"encoding/json"
	"fmt"
	"strings"

	ethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/crypto"
)

type ABI struct {
	ethabi.ABI
	eventIndex          map[string]int
	eventSelectorCounts map[string]int
	eventNameCounts     map[string]int
	originalFunctions   map[string]Function
	functionNameCounts  map[string]int
	functionEntries     []Function
	eventEntries        []Event
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
	eventIndex := map[string]int{}
	selectorCounts := map[string]int{}
	eventNameCounts := map[string]int{}
	functions := map[string]Function{}
	functionCounts := map[string]int{}
	functionEntries := []Function{}
	eventEntries := []Event{}
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
			functionCounts[function.Name]++
			functionEntries = append(functionEntries, function)
			continue
		}
		if event.Type != "event" {
			continue
		}
		name := ethabi.ResolveNameConflict(event.Name, func(name string) bool {
			_, exists := eventIndex[name]
			return exists
		})
		eventIndex[name] = len(eventEntries)
		selectorCounts[a.Events[name].ID.Hex()]++
		if !event.Anonymous {
			eventNameCounts[event.Name]++
		}
		eventEntries = append(eventEntries, event)
	}
	return ABI{
		ABI:                 a,
		eventIndex:          eventIndex,
		eventSelectorCounts: selectorCounts,
		eventNameCounts:     eventNameCounts,
		originalFunctions:   functions,
		functionNameCounts:  functionCounts,
		functionEntries:     functionEntries,
		eventEntries:        eventEntries,
	}, nil
}
func (a ABI) DecodeEvent(name string, topics []string, data string) (any, error) {
	event, ok := a.EventItem(name)
	if !ok {
		return nil, fmt.Errorf("unknown ABI event %q", name)
	}
	return DecodeEventLog(event, topics, data)
}

// EventItem returns the original ABI metadata for an event's parsed name,
// including the distinct names assigned to overloaded events.
func (a ABI) EventItem(name string) (Event, bool) {
	i, ok := a.eventIndex[name]
	if !ok {
		return Event{}, false
	}
	return a.eventEntries[i], true
}

// ParsedEvents returns go-ethereum's event names together with the declaration
// that produced each one, in ABI order. A shared topic need not share a layout.
func (a ABI) ParsedEvents() []NamedItem[Event] {
	items := make([]NamedItem[Event], len(a.eventEntries))
	for name, i := range a.eventIndex {
		items[i] = NamedItem[Event]{Name: name, Item: a.eventEntries[i]}
	}
	return items
}

// SharedEventSelector reports whether multiple ABI declarations have this topic.
func (a ABI) SharedEventSelector(topic string) bool {
	return a.eventSelectorCounts[strings.ToLower(topic)] > 1
}
