// Package factory resolves dynamically created contract addresses from logs.
package factory

import (
	"fmt"
	"math"
	"ponder.local/ponder-go/abi"
	"sort"
	"strconv"
	"strings"
)

type Config struct {
	ChainID   uint64    `json:"chainId"`
	SourceID  string    `json:"sourceId"`
	Address   any       `json:"address"`
	Event     abi.Event `json:"event"`
	Parameter *string   `json:"parameter,omitempty"`
	Location  *string   `json:"location,omitempty"`
	FromBlock *uint64   `json:"fromBlock,omitempty"`
	ToBlock   *uint64   `json:"toBlock,omitempty"`
}

type LogFactory struct {
	ID                   string  `json:"id"`
	Type                 string  `json:"type"`
	ChainID              uint64  `json:"chainId"`
	SourceID             string  `json:"sourceId"`
	Address              any     `json:"address,omitempty"`
	EventSelector        string  `json:"eventSelector"`
	ChildAddressLocation string  `json:"childAddressLocation"`
	FromBlock            *uint64 `json:"fromBlock,omitempty"`
	ToBlock              *uint64 `json:"toBlock,omitempty"`
}

func number(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") || strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O") || strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B") {
		n, e := strconv.ParseUint(s, 0, 64)
		return float64(n), e
	}
	return strconv.ParseFloat(s, 64)
}

func Build(config Config) (LogFactory, error) {
	var address any
	addressID := "undefined"
	switch a := config.Address.(type) {
	case nil:
	case string:
		address = strings.ToLower(a)
		addressID = address.(string)
	case []any:
		values := []string{}
		for _, v := range a {
			s, ok := v.(string)
			if !ok {
				return LogFactory{}, fmt.Errorf("factory addresses must be strings")
			}
			values = append(values, s)
		}
		config.Address = values
		return Build(config)
	case []string:
		// Source dedupes before case folding. Preserve that ordering, including
		// the possibility that differently cased inputs become equal strings.
		seen := map[string]bool{}
		values := []string{}
		for _, v := range a {
			if !seen[v] {
				values = append(values, strings.ToLower(v))
				seen[v] = true
			}
		}
		sort.Strings(values)
		address = values
		addressID = strings.Join(values, "_")
	default:
		return LogFactory{}, fmt.Errorf("factory address must be a string or array")
	}
	selector := abi.EventSelector(config.Event)
	location := ""
	if config.Parameter != nil {
		parts := strings.Split(*config.Parameter, ".")
		indexed := []abi.Parameter{}
		nonindexed := []abi.Parameter{}
		for _, p := range config.Event.Inputs {
			if p.Indexed {
				indexed = append(indexed, p)
			} else {
				nonindexed = append(nonindexed, p)
			}
		}
		if len(parts) == 1 {
			for i, p := range indexed {
				if p.Name == parts[0] {
					location = fmt.Sprintf("topic%d", i+1)
					break
				}
			}
		}
		if location == "" {
			position := -1
			for i, p := range nonindexed {
				if p.Name == parts[0] {
					position = i
					break
				}
			}
			if position < 0 {
				names := []string{}
				for _, p := range config.Event.Inputs {
					names = append(names, "'"+p.Name+"'")
				}
				return LogFactory{}, fmt.Errorf("Factory event parameter not found in factory event signature. Got '%s', expected one of [%s].", *config.Parameter, strings.Join(names, ", "))
			}
			p := nonindexed[position]
			if len(parts) == 1 && p.Type != "address" {
				return LogFactory{}, fmt.Errorf("Factory event parameter type is not valid. Got '%s', expected 'address'.", p.Type)
			}
			if len(parts) > 1 && p.Type != "tuple" {
				return LogFactory{}, fmt.Errorf("Factory event parameter type is not valid. Got '%s', expected 'tuple'.", p.Type)
			}
			var offset uint64
			for _, p := range nonindexed[:position] {
				n, e := abi.BytesConsumed(p)
				if e != nil {
					return LogFactory{}, e
				}
				if n > math.MaxUint64-offset {
					return LogFactory{}, fmt.Errorf("factory offset overflows uint64")
				}
				offset += n
			}
			if len(parts) > 1 {
				n, e := abi.NestedOffset(p, parts[1:])
				if e != nil {
					return LogFactory{}, e
				}
				if n > math.MaxUint64-offset {
					return LogFactory{}, fmt.Errorf("factory offset overflows uint64")
				}
				offset += n
			}
			location = fmt.Sprintf("offset%d", offset)
		}
	} else if config.Location != nil {
		location = *config.Location
		if location != "topic1" && location != "topic2" && location != "topic3" {
			valid := false
			if strings.HasPrefix(location, "offset") {
				n, e := number(location[6:])
				valid = e == nil && !math.IsInf(n, 0) && !math.IsNaN(n) && math.Trunc(n) == n
			}
			if !valid {
				return LogFactory{}, fmt.Errorf("Factory location is invalid. Got '%s', expected 'topic1', 'topic2', 'topic3', or 'offset[number]'.", location)
			}
		}
	} else {
		return LogFactory{}, fmt.Errorf("Factory must specify \"parameter\" or \"location\".")
	}
	bound := func(p *uint64) string {
		if p == nil {
			return "undefined"
		}
		return strconv.FormatUint(*p, 10)
	}
	id := fmt.Sprintf("log_%s_%d_%s_%s_%s_%s", addressID, config.ChainID, location, selector, bound(config.FromBlock), bound(config.ToBlock))
	return LogFactory{ID: id, Type: "log", ChainID: config.ChainID, SourceID: config.SourceID, Address: address, EventSelector: selector, ChildAddressLocation: location, FromBlock: config.FromBlock, ToBlock: config.ToBlock}, nil
}

// ChildAddress extracts the source's 20-byte substring from its configured word.
// It rejects truncated/malformed RPC data rather than registering a short address.
func (factory LogFactory) ChildAddress(topics []string, data string) (string, error) {
	location := factory.ChildAddressLocation
	word := ""
	if strings.HasPrefix(location, "offset") {
		n, e := number(location[6:])
		if e != nil || n < 0 || n > float64(len(data))/2 || math.Trunc(n) != n {
			return "", fmt.Errorf("invalid child address offset")
		}
		start := 2 + 24 + int(n)*2
		if !strings.HasPrefix(data, "0x") || start+40 > len(data) {
			return "", fmt.Errorf("factory log data is too short")
		}
		word = data[start : start+40]
	} else {
		index := 3
		if location == "topic1" {
			index = 1
		} else if location == "topic2" {
			index = 2
		} else if location != "topic3" {
			return "", fmt.Errorf("invalid child address topic")
		}
		if index >= len(topics) || !strings.HasPrefix(topics[index], "0x") || len(topics[index]) < 66 {
			return "", fmt.Errorf("factory log topic is too short")
		}
		word = topics[index][26:66]
	}
	for _, c := range word {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return "", fmt.Errorf("factory child address is not hex")
		}
	}
	return "0x" + word, nil
}
