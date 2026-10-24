package abi

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"

	"ponder.local/ponder-go/filter"

	ethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// EventTopics retains OR alternatives and null wildcards independently.
type EventTopics struct {
	Topic0 string `json:"topic0"`
	Topic1 any    `json:"topic1"`
	Topic2 any    `json:"topic2"`
	Topic3 any    `json:"topic3"`
}

// EncodeEventTopics encodes indexed positions, matching the source helper.
// Tuple/array ABI parameter filters are explicitly unsupported by that helper.
func EncodeEventTopics(event Event, arguments any) (EventTopics, error) {
	result := EventTopics{Topic0: EventSelector(event)}
	indexed := []Parameter{}
	for _, input := range event.Inputs {
		if input.Indexed {
			indexed = append(indexed, input)
		}
	}
	values := []any{}
	switch args := arguments.(type) {
	case nil:
	case []any:
		values = args
	case map[string]any:
		if len(args) > 0 {
			for _, input := range indexed {
				values = append(values, args[input.Name])
			}
		}
	default:
		return result, fmt.Errorf("event filter arguments must be indexed positions or named arguments")
	}
	if len(values) == 0 {
		return result, nil
	}
	topics := [3]any{}
	for i, parameter := range indexed {
		if i >= len(topics) {
			break
		}
		if i >= len(values) || values[i] == nil || filter.IsUndefined(values[i]) {
			continue
		}
		value := values[i]
		if alternatives, ok := value.([]any); ok {
			encoded := make([]string, len(alternatives))
			for j, item := range alternatives {
				text, err := encodeIndexedArgument(parameter, item)
				if err != nil {
					return result, err
				}
				encoded[j] = text
			}
			topics[i] = encoded
		} else {
			encoded, err := encodeIndexedArgument(parameter, value)
			if err != nil {
				return result, err
			}
			topics[i] = encoded
		}
	}
	result.Topic1, result.Topic2, result.Topic3 = topics[0], topics[1], topics[2]
	return result, nil
}

func encodeIndexedArgument(parameter Parameter, value any) (string, error) {
	if parameter.Type == "tuple" || strings.Contains(parameter.Type, "[") {
		return "", fmt.Errorf("event filter parameter type %q is not supported", parameter.Type)
	}
	if parameter.Type == "string" || parameter.Type == "bytes" {
		text, ok := value.(string)
		if !ok {
			// The pinned helper passes array elements directly to toBytes.
			// TextEncoder coerces null to "null" and undefined to empty bytes.
			// Scalar nullish values are handled as wildcards by the caller.
			switch {
			case value == nil:
				text = "null"
			case filter.IsUndefined(value):
				text = ""
			default:
				return "", fmt.Errorf("indexed %s argument must be a string", parameter.Type)
			}
		}
		data := []byte(text)
		if strings.HasPrefix(text, "0x") {
			tail := text[2:]
			if len(tail)%2 != 0 {
				tail = "0" + tail
			}
			var err error
			data, err = hex.DecodeString(tail)
			if err != nil {
				return "", err
			}
		}
		return crypto.Keccak256Hash(data).Hex(), nil
	}
	typeName := parameter.Type
	if typeName == "uint" || typeName == "int" {
		typeName += "256"
	}
	typ, err := ethabi.NewType(typeName, "", nil)
	if err != nil {
		return "", err
	}
	var encodedValue any
	switch typ.T {
	case ethabi.AddressTy:
		text, ok := value.(string)
		if !ok || !strings.HasPrefix(text, "0x") || len(text) != 42 {
			return "", fmt.Errorf("indexed address argument is invalid")
		}
		if _, err := hex.DecodeString(text[2:]); err != nil {
			return "", err
		}
		encodedValue = common.HexToAddress(text)
	case ethabi.BoolTy:
		boolean, ok := value.(bool)
		if !ok {
			return "", fmt.Errorf("indexed bool argument is invalid")
		}
		encodedValue = boolean
	case ethabi.FixedBytesTy, ethabi.FunctionTy:
		text, ok := value.(string)
		if !ok || !strings.HasPrefix(text, "0x") {
			return "", fmt.Errorf("indexed bytes argument is invalid")
		}
		data, err := hex.DecodeString(text[2:])
		if err != nil || len(data) != typ.Size {
			return "", fmt.Errorf("indexed bytes argument size must be %d", typ.Size)
		}
		array := reflect.New(typ.GetType()).Elem()
		for i, b := range data {
			array.Index(i).SetUint(uint64(b))
		}
		encodedValue = array.Interface()
	case ethabi.IntTy, ethabi.UintTy:
		integer, err := indexedInteger(value)
		if err != nil {
			return "", err
		}
		if typ.T == ethabi.UintTy {
			if integer.Sign() < 0 || integer.BitLen() > typ.Size {
				return "", fmt.Errorf("indexed unsigned integer exceeds %s", typeName)
			}
		} else {
			bound := new(big.Int).Lsh(big.NewInt(1), uint(typ.Size-1))
			if integer.Cmp(bound) >= 0 || integer.Cmp(new(big.Int).Neg(bound)) < 0 {
				return "", fmt.Errorf("indexed signed integer exceeds %s", typeName)
			}
		}
		if typ.Size > 64 {
			encodedValue = integer
		} else {
			number := reflect.New(typ.GetType()).Elem()
			if typ.T == ethabi.UintTy {
				number.SetUint(integer.Uint64())
			} else {
				number.SetInt(integer.Int64())
			}
			encodedValue = number.Interface()
		}
	default:
		return "", fmt.Errorf("indexed ABI type %q is not supported", parameter.Type)
	}
	data, err := (ethabi.Arguments{{Type: typ}}).Pack(encodedValue)
	if err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(data), nil
}

func indexedInteger(value any) (*big.Int, error) {
	if integer, ok := value.(*big.Int); ok {
		return new(big.Int).Set(integer), nil
	}
	if number, ok := value.(float64); ok {
		if math.IsInf(number, 0) || math.IsNaN(number) || math.Trunc(number) != number {
			return nil, fmt.Errorf("indexed integer argument is not an integer")
		}
		integer, _ := new(big.Float).SetFloat64(number).Int(nil)
		return integer, nil
	}
	text := fmt.Sprint(value)
	if number, ok := value.(json.Number); ok {
		text = number.String()
	}
	base := 10
	if strings.HasPrefix(text, "0x") {
		text, base = text[2:], 16
	}
	integer, ok := new(big.Int).SetString(text, base)
	if !ok {
		return nil, fmt.Errorf("indexed integer argument is invalid")
	}
	return integer, nil
}
