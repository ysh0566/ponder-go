// Package bloom implements the pinned source's Ethereum log bloom predicates.
package bloom

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"ponder.local/ponder-go/filter"
)

const SizeBytes = 256

var ZeroLogsBloom = "0x" + strings.Repeat("00", SizeBytes)

type Bits struct {
	Byte0 int `json:"byte0"`
	Mask0 int `json:"mask0"`
	Byte1 int `json:"byte1"`
	Mask1 int `json:"mask1"`
	Byte2 int `json:"byte2"`
	Mask2 int `json:"mask2"`
}

type LogFilterBloom struct {
	// nil is wildcard; explicit empty is an impossible alternative list.
	Address []Bits
	Topics  [4][]Bits
}
type FactoryBloom struct {
	Address       []Bits
	EventSelector Bits
}

func GetBloomBits(input string) (Bits, error) {
	data := []byte(input)
	if strings.HasPrefix(input, "0x") {
		value := input[2:]
		if len(value)%2 != 0 {
			value = "0" + value
		}
		decoded, err := hex.DecodeString(value)
		if err != nil {
			return Bits{}, fmt.Errorf("invalid bloom hex input: %w", err)
		}
		data = decoded
	}
	// Mirrors viem's non-strict hex detection: non-0x strings are UTF-8 input.
	hash := crypto.Keccak256(data)
	var index, mask [3]int
	for i := range index {
		bit := (int(hash[i*2])<<8 | int(hash[i*2+1])) & 0x7ff
		index[i] = SizeBytes - 1 - bit/8
		mask[i] = 1 << (bit % 8)
	}
	return Bits{Byte0: index[0], Mask0: mask[0], Byte1: index[1], Mask1: mask[1], Byte2: index[2], Mask2: mask[2]}, nil
}
func IsBloomBitsInBloom(bloom []byte, bits Bits) bool {
	has := func(index, mask int) bool { return index >= 0 && index < len(bloom) && int(bloom[index])&mask != 0 }
	return has(bits.Byte0, bits.Mask0) && has(bits.Byte1, bits.Mask1) && has(bits.Byte2, bits.Mask2)
}
func IsInBloom(bloom []byte, input string) (bool, error) {
	bits, err := GetBloomBits(input)
	if err != nil {
		return false, err
	}
	return IsBloomBitsInBloom(bloom, bits), nil
}
func isAnyInBloom(bloom []byte, bits []Bits) bool {
	if bits == nil {
		return true
	}
	for _, value := range bits {
		if IsBloomBitsInBloom(bloom, value) {
			return true
		}
	}
	return false
}
func toBloomBits(input any) ([]Bits, error) {
	if input == nil || filter.IsUndefined(input) {
		return nil, nil
	}
	var values []string
	switch value := input.(type) {
	case string:
		values = []string{value}
	case []string:
		values = value
	case []any:
		values = make([]string, len(value))
		for i, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("bloom alternatives must be hex strings")
			}
			values[i] = text
		}
	default:
		return nil, fmt.Errorf("bloom value must be a hex string or array")
	}
	result := make([]Bits, len(values))
	for i, value := range values {
		bits, err := GetBloomBits(value)
		if err != nil {
			return nil, err
		}
		result[i] = bits
	}
	return result, nil
}
func value(object filter.Object, key string) any {
	if v, ok := object[key]; ok {
		return v
	}
	return filter.Undefined
}
func GetLogFilterBloom(input filter.LogFilter) (LogFilterBloom, error) {
	return GetLogFilterBloomObject(input.Object())
}

// GetLogFilterBloomObject retains the explicit legacy dynamic input route;
// source tests use it to execute topic0=undefined with their original directive.
func GetLogFilterBloomObject(input filter.Object) (LogFilterBloom, error) {
	var result LogFilterBloom
	address := value(input, "address")
	empty := false
	switch xs := address.(type) {
	case []string:
		empty = len(xs) == 0
	case []any:
		empty = len(xs) == 0
	}
	if !filter.IsAddressFactory(address) && !empty {
		bits, err := toBloomBits(address)
		if err != nil {
			return LogFilterBloom{}, err
		}
		result.Address = bits
	}
	for i, key := range []string{"topic0", "topic1", "topic2", "topic3"} {
		bits, err := toBloomBits(value(input, key))
		if err != nil {
			return LogFilterBloom{}, err
		}
		result.Topics[i] = bits
	}
	return result, nil
}
func IsFilterInBloom(bloom []byte, input LogFilterBloom) bool {
	if !isAnyInBloom(bloom, input.Address) {
		return false
	}
	for _, topic := range input.Topics {
		if !isAnyInBloom(bloom, topic) {
			return false
		}
	}
	return true
}
func GetFactoryBloom(input filter.Object) (FactoryBloom, error) {
	address, err := toBloomBits(value(input, "address"))
	if err != nil {
		return FactoryBloom{}, err
	}
	selector, ok := value(input, "eventSelector").(string)
	if !ok {
		return FactoryBloom{}, fmt.Errorf("factory event selector must be a hex string")
	}
	bits, err := GetBloomBits(selector)
	if err != nil {
		return FactoryBloom{}, err
	}
	return FactoryBloom{Address: address, EventSelector: bits}, nil
}
func IsFactoryInBloom(bloom []byte, input FactoryBloom) bool {
	return isAnyInBloom(bloom, input.Address) && IsBloomBitsInBloom(bloom, input.EventSelector)
}
