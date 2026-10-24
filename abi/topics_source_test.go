package abi

import (
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"testing"

	"ponder.local/ponder-go/filter"
)

func restoreTopicSourceValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		if v["$undefined"] == true {
			return filter.Undefined
		}
		if text, ok := v["$bigint"].(string); ok {
			n, ok := new(big.Int).SetString(text, 10)
			if !ok {
				panic("invalid captured bigint")
			}
			return n
		}
		out := map[string]any{}
		for key, item := range v {
			out[key] = restoreTopicSourceValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = restoreTopicSourceValue(item)
		}
		return out
	default:
		return value
	}
}

// Actual pinned encoder values and error occurrence; Go and JS error classes
// are recorded separately. These vectors add zero original test mappings.
func TestIndexedTopicsSource(t *testing.T) {
	raw, err := os.ReadFile("../testdata/abi/topics-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name   string      `json:"name"`
			Event  Event       `json:"event"`
			Args   any         `json:"args"`
			Topics EventTopics `json:"topics"`
			Error  *struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 50 {
		t.Fatal("missing complete source vectors", len(fixture.Cases))
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			actual, err := EncodeEventTopics(c.Event, restoreTopicSourceValue(c.Args))
			if c.Error != nil {
				if err == nil {
					t.Fatalf("source %s: %s; native unexpectedly accepted", c.Error.Name, c.Error.Message)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			gotBytes, _ := json.Marshal(actual)
			wantBytes, _ := json.Marshal(c.Topics)
			var got, want any
			if err := json.Unmarshal(gotBytes, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(wantBytes, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("native=%s source=%s", gotBytes, wantBytes)
			}
		})
	}
}
