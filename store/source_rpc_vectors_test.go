package store

import (
	"encoding/json"
	"math"
	"math/big"
	"os"
	"testing"

	"ponder.local/ponder-go/filter"
)

func restoreSourceRPCValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if x["$undefined"] == true {
			return filter.Undefined
		}
		if b, ok := x["$bigint"].(string); ok {
			n, _ := new(big.Int).SetString(b, 10)
			return n
		}
		if n, ok := x["$number"].(string); ok {
			switch n {
			case "NaN":
				return math.NaN()
			case "Infinity":
				return math.Inf(1)
			case "-Infinity":
				return math.Inf(-1)
			case "-0":
				return math.Copysign(0, -1)
			}
		}
		pairs, ok := x["$object"].([]any)
		if !ok {
			panic("unrecognized source wire object")
		}
		out := map[string]any{}
		for _, p := range pairs {
			pair := p.([]any)
			out[pair[0].(string)] = restoreSourceRPCValue(pair[1])
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = restoreSourceRPCValue(item)
		}
		return out
	default:
		return v
	}
}
func TestSourceRPCVectors(t *testing.T) {
	raw, err := os.ReadFile("../testdata/rpc/source-cache.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name                        string
			Request                     any
			Serialized, Lowercase, Hash string
			NumericBlock                *uint64 `json:"numeric_block"`
			Error                       *struct{ Name, Message string }
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 40 {
		t.Fatal("missing source vectors")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			value := restoreSourceRPCValue(c.Request)
			encoded, e := sourceOrderedJSON(value)
			hash, hashError := SourceRPCRequestHash(value)
			if c.Hash == "" {
				if hashError == nil {
					t.Fatal("source order/stringify error unexpectedly accepted", hash)
				}
				return
			}
			if e != nil || hashError != nil || string(encoded) != c.Serialized || hash != c.Hash {
				t.Fatalf("source hash/serialization mismatch\n native=%s/%s/%v/%v\n source=%s/%s", encoded, hash, e, hashError, c.Serialized, c.Hash)
			}
			block, e := sourceRequestBlock(value)
			if c.Error != nil {
				if e == nil {
					t.Fatal("source block extraction error unexpectedly accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if c.NumericBlock == nil {
				if block != nil {
					t.Fatal("source has no numeric block", block)
				}
			} else if block == nil || *block != *c.NumericBlock {
				t.Fatal("block mismatch", block, c.NumericBlock)
			}
		})
	}
}
