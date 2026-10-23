// Package abi adapts Ethereum ABI decoding to Ponder's lowercase-address convention.
package abi

import (
	"encoding/hex"
	"fmt"
	ethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"reflect"
	"strings"
)

type ABI struct{ ethabi.ABI }

func Parse(data string) (ABI, error) { a, e := ethabi.JSON(strings.NewReader(data)); return ABI{a}, e }
func bytes(s string) ([]byte, error) { return hex.DecodeString(strings.TrimPrefix(s, "0x")) }
func normalize(v any) any {
	switch x := v.(type) {
	case common.Address:
		return strings.ToLower(x.Hex())
	case common.Hash:
		return x.Hex()
	}
	r := reflect.ValueOf(v)
	if r.IsValid() && (r.Kind() == reflect.Array || r.Kind() == reflect.Slice) {
		a := make([]any, r.Len())
		for i := range a {
			a[i] = normalize(r.Index(i).Interface())
		}
		return a
	}
	return v
}

// DecodeEvent decodes named or unnamed ABI inputs. Dynamic indexed values remain hashes.
func (a ABI) DecodeEvent(name string, topics []string, data string) (any, error) {
	e, ok := a.Events[name]
	if !ok {
		return nil, fmt.Errorf("unknown ABI event %q", name)
	}
	raw, err := bytes(data)
	if err != nil {
		return nil, err
	}
	non := e.Inputs.NonIndexed()
	values, err := non.Unpack(raw)
	if err != nil {
		return nil, err
	}
	args := make([]any, len(e.Inputs))
	named := true
	j, k := 0, 1
	if e.Anonymous {
		k = 0
	}
	for i, p := range e.Inputs {
		if p.Name == "" {
			named = false
		}
		if !p.Indexed {
			args[i] = normalize(values[j])
			j++
			continue
		}
		if k >= len(topics) {
			return nil, fmt.Errorf("missing indexed topic for %s", p.Name)
		}
		b, err := bytes(topics[k])
		k++
		if err != nil || len(b) != 32 {
			return nil, fmt.Errorf("invalid topic for %s", p.Name)
		}
		switch p.Type.T {
		case ethabi.StringTy, ethabi.BytesTy, ethabi.SliceTy, ethabi.ArrayTy, ethabi.TupleTy:
			args[i] = "0x" + hex.EncodeToString(b)
		default:
			v, err := (ethabi.Arguments{{Type: p.Type}}).Unpack(b)
			if err != nil {
				return nil, err
			}
			args[i] = normalize(v[0])
		}
	}
	if len(args) == 0 {
		return nil, nil
	}
	if !named {
		return args, nil
	}
	m := map[string]any{}
	for i, p := range e.Inputs {
		m[p.Name] = args[i]
	}
	return m, nil
}
